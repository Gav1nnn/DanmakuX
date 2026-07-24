package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/broker"
	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/Gav1nnn/DanmakuX/internal/limiter"
	"github.com/Gav1nnn/DanmakuX/internal/metrics"
	"github.com/Gav1nnn/DanmakuX/internal/model"
	"github.com/Gav1nnn/DanmakuX/internal/repository"
	"github.com/Gav1nnn/DanmakuX/internal/room"
	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	// ErrInvalidContent 表示消息内容为空或超长。
	ErrInvalidContent = errors.New("invalid content")
)

// RateLimitError 表示命中限流，并携带建议重试时间。
type RateLimitError struct {
	Scope      string
	Reason     string
	RetryAfter time.Duration
}

func (e RateLimitError) Error() string {
	return fmt.Sprintf("%s: retry after %s", e.Reason, e.RetryAfter)
}

// SendRequest 是发送弹幕时所需的业务输入参数。
type SendRequest struct {
	UserID  string
	RoomID  string
	IP      string
	Content string
}

// MessageService 负责弹幕主流程：校验、限流、发布、持久化。
type MessageService struct {
	nodeID string
	cfg    config.LimitConfig

	log     *zap.Logger
	hub     *room.Hub
	broker  broker.Broker
	repo    repository.DanmakuRepository
	limiter limiter.Limiter
	metrics *metrics.Metrics

	persistCh chan model.Danmaku

	persistMu        sync.RWMutex
	persistClosed    bool
	persistStartOnce sync.Once
	persistWorkers   sync.WaitGroup

	subMu         sync.Mutex
	subscriptions map[string]*roomSubscription
	closeOnce     sync.Once
}

const persistenceWriteTimeout = 3 * time.Second

type roomSubscription struct {
	refs        int
	unsubscribe func() error
}

// NewMessageService 创建消息服务实例。
func NewMessageService(
	nodeID string,
	cfg config.LimitConfig,
	log *zap.Logger,
	hub *room.Hub,
	br broker.Broker,
	repo repository.DanmakuRepository,
	lim limiter.Limiter,
	metricsCollector *metrics.Metrics,
) *MessageService {
	return &MessageService{
		nodeID:        nodeID,
		cfg:           cfg,
		log:           log,
		hub:           hub,
		broker:        br,
		repo:          repo,
		limiter:       lim,
		metrics:       metricsCollector,
		persistCh:     make(chan model.Danmaku, 4096),
		subscriptions: make(map[string]*roomSubscription),
	}
}

// AcquireRoomSubscription 获取一个房间订阅引用，首个引用负责创建 Redis 订阅。
func (s *MessageService) AcquireRoomSubscription(ctx context.Context, roomID string) error {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	if subscription, ok := s.subscriptions[roomID]; ok {
		subscription.refs++
		return nil
	}

	// 收到跨节点消息后，仅负责向本节点连接广播。
	unsubscribe, err := s.broker.Subscribe(ctx, roomID, func(_ context.Context, message protocol.BroadcastMessage) error {
		localClients := s.hub.RoomSize(roomID)
		count := s.hub.BroadcastLocal(roomID, protocol.WSOutboundMessage{
			Type:      "danmaku",
			MessageID: message.MessageID,
			RoomID:    message.RoomID,
			UserID:    message.UserID,
			Content:   message.Content,
			NodeID:    message.NodeID,
			Timestamp: message.Timestamp,
		})
		if s.metrics != nil {
			s.metrics.AddMessageBroadcast(count)
			s.metrics.AddMessageBroadcastDrop(localClients - count)
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.subscriptions[roomID] = &roomSubscription{
		refs:        1,
		unsubscribe: unsubscribe,
	}
	s.log.Info("room subscription ready", zap.String("room_id", roomID))
	return nil
}

// ReleaseRoomSubscription 释放一个房间订阅引用，最后一个引用负责取消 Redis 订阅。
func (s *MessageService) ReleaseRoomSubscription(roomID string) error {
	s.subMu.Lock()
	subscription, ok := s.subscriptions[roomID]
	if !ok {
		s.subMu.Unlock()
		return nil
	}

	subscription.refs--
	if subscription.refs > 0 {
		s.subMu.Unlock()
		return nil
	}

	delete(s.subscriptions, roomID)
	s.subMu.Unlock()

	err := subscription.unsubscribe()
	if err == nil {
		s.log.Info("room subscription released", zap.String("room_id", roomID))
	}
	return err
}

// StartPersistenceWorker 启动异步批量落库 worker。
func (s *MessageService) StartPersistenceWorker(batchSize int, flushInterval time.Duration) {
	if batchSize <= 0 {
		batchSize = 50
	}
	if flushInterval <= 0 {
		flushInterval = 500 * time.Millisecond
	}

	s.persistStartOnce.Do(func() {
		s.persistWorkers.Add(1)
		go func() {
			defer s.persistWorkers.Done()
			ticker := time.NewTicker(flushInterval)
			defer ticker.Stop()

			batch := make([]model.Danmaku, 0, batchSize)
			flush := func() {
				if len(batch) == 0 {
					return
				}

				writeCtx, cancel := context.WithTimeout(context.Background(), persistenceWriteTimeout)
				err := s.repo.SaveBatch(writeCtx, batch)
				cancel()
				if err != nil {
					if s.metrics != nil {
						s.metrics.IncPersistFailed()
					}
					s.log.Error("persist batch failed", zap.Error(err), zap.Int("size", len(batch)))
				}
				batch = batch[:0]
			}

			for {
				select {
				case <-ticker.C:
					flush()
				case item, ok := <-s.persistCh:
					if !ok {
						flush()
						return
					}
					batch = append(batch, item)
					if len(batch) >= batchSize {
						flush()
					}
				}
			}
		}()
	})
}

// Send 处理一条弹幕发送请求并发布到广播链路。
func (s *MessageService) Send(ctx context.Context, req SendRequest) (protocol.BroadcastMessage, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" || len(content) > 200 {
		if s.metrics != nil {
			s.metrics.IncMessageInvalid()
		}
		return protocol.BroadcastMessage{}, ErrInvalidContent
	}

	if err := s.checkLimit(ctx, req); err != nil {
		var rateLimitErr RateLimitError
		if errors.As(err, &rateLimitErr) && s.metrics != nil {
			s.metrics.IncMessageRateLimited(rateLimitErr.Scope)
		}
		return protocol.BroadcastMessage{}, err
	}
	if s.metrics != nil {
		s.metrics.IncMessageAccepted()
	}

	msg := protocol.BroadcastMessage{
		MessageID: uuid.NewString(),
		RoomID:    req.RoomID,
		UserID:    req.UserID,
		Content:   content,
		NodeID:    s.nodeID,
		Timestamp: time.Now(),
	}

	if err := s.broker.Publish(ctx, msg); err != nil {
		return protocol.BroadcastMessage{}, err
	}
	if s.metrics != nil {
		s.metrics.IncMessagePublished()
	}

	s.enqueuePersistence(model.Danmaku{
		ID:        msg.MessageID,
		RoomID:    msg.RoomID,
		UserID:    msg.UserID,
		Content:   msg.Content,
		CreatedAt: msg.Timestamp,
	})
	return msg, nil
}

func (s *MessageService) enqueuePersistence(message model.Danmaku) {
	s.persistMu.RLock()
	defer s.persistMu.RUnlock()

	if s.persistClosed {
		if s.metrics != nil {
			s.metrics.IncPersistDropped()
		}
		s.log.Warn("persistence is closed, dropping message", zap.String("message_id", message.ID))
		return
	}

	select {
	case s.persistCh <- message:
		if s.metrics != nil {
			s.metrics.IncPersistQueued()
		}
	default:
		if s.metrics != nil {
			s.metrics.IncPersistDropped()
		}
		// 持久化通道满时保护实时链路，记录告警并丢弃落库。
		s.log.Warn("persist channel full, dropping message", zap.String("message_id", message.ID))
	}
}

// ListHistory 查询指定房间历史弹幕。
func (s *MessageService) ListHistory(ctx context.Context, roomID string, limit int, before time.Time) ([]model.Danmaku, error) {
	return s.repo.ListByRoom(ctx, roomID, limit, before)
}

// checkLimit 在一次 Redis Lua 调用中原子执行用户/IP/房间三级限流。
func (s *MessageService) checkLimit(ctx context.Context, req SendRequest) error {
	roomSlot := base64.RawURLEncoding.EncodeToString([]byte(req.RoomID))
	decision, err := s.limiter.Allow(ctx, []limiter.Rule{
		{
			Scope:  "user",
			Key:    fmt.Sprintf("lim:{%s}:user:%s", roomSlot, req.UserID),
			Limit:  s.cfg.UserCount,
			Window: s.cfg.UserWindow,
		},
		{
			Scope:  "ip",
			Key:    fmt.Sprintf("lim:{%s}:ip:%s", roomSlot, req.IP),
			Limit:  s.cfg.IPCount,
			Window: s.cfg.IPWindow,
		},
		{
			Scope:  "room",
			Key:    fmt.Sprintf("lim:{%s}:room", roomSlot),
			Limit:  s.cfg.RoomCount,
			Window: s.cfg.RoomWindow,
		},
	})
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}

	reason := decision.RejectedScope + " limit exceeded"
	switch decision.RejectedScope {
	case "user", "ip", "room":
	default:
		return limiter.ErrInvalidLimiterResult
	}
	return RateLimitError{
		Scope:      decision.RejectedScope,
		Reason:     reason,
		RetryAfter: decision.RetryAfter,
	}
}

// Close 关闭持久化 worker 与所有房间订阅。
func (s *MessageService) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		s.persistMu.Lock()
		s.persistClosed = true
		close(s.persistCh)
		s.persistMu.Unlock()
		s.persistWorkers.Wait()

		s.subMu.Lock()
		defer s.subMu.Unlock()
		for roomID, subscription := range s.subscriptions {
			if err := subscription.unsubscribe(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("unsubscribe %s: %w", roomID, err)
			}
		}
		s.subscriptions = make(map[string]*roomSubscription)
		if err := s.broker.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	})
	return firstErr
}
