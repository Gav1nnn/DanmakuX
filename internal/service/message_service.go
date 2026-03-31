package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/broker"
	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/Gav1nnn/DanmakuX/internal/limiter"
	"github.com/Gav1nnn/DanmakuX/internal/model"
	"github.com/Gav1nnn/DanmakuX/internal/repository"
	"github.com/Gav1nnn/DanmakuX/internal/room"
	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	ErrInvalidContent = errors.New("invalid content")
)

type RateLimitError struct {
	Reason     string
	RetryAfter time.Duration
}

func (e RateLimitError) Error() string {
	return fmt.Sprintf("%s: retry after %s", e.Reason, e.RetryAfter)
}

type SendRequest struct {
	UserID  string
	RoomID  string
	IP      string
	Content string
}

type MessageService struct {
	nodeID string
	cfg    config.LimitConfig

	log     *zap.Logger
	hub     *room.Hub
	broker  broker.Broker
	repo    repository.DanmakuRepository
	limiter limiter.Limiter

	persistCh chan model.Danmaku

	subMu          sync.Mutex
	subscriptions  map[string]func() error
	closeOnce      sync.Once
	persistWorkers sync.WaitGroup
}

func NewMessageService(
	nodeID string,
	cfg config.LimitConfig,
	log *zap.Logger,
	hub *room.Hub,
	br broker.Broker,
	repo repository.DanmakuRepository,
	lim limiter.Limiter,
) *MessageService {
	return &MessageService{
		nodeID:        nodeID,
		cfg:           cfg,
		log:           log,
		hub:           hub,
		broker:        br,
		repo:          repo,
		limiter:       lim,
		persistCh:     make(chan model.Danmaku, 4096),
		subscriptions: make(map[string]func() error),
	}
}

func (s *MessageService) EnsureRoomSubscription(ctx context.Context, roomID string) error {
	s.subMu.Lock()
	if _, ok := s.subscriptions[roomID]; ok {
		s.subMu.Unlock()
		return nil
	}
	s.subMu.Unlock()

	unsub, err := s.broker.Subscribe(ctx, roomID, func(_ context.Context, message protocol.BroadcastMessage) error {
		s.hub.BroadcastLocal(roomID, protocol.WSOutboundMessage{
			Type:      "danmaku",
			MessageID: message.MessageID,
			RoomID:    message.RoomID,
			UserID:    message.UserID,
			Content:   message.Content,
			NodeID:    message.NodeID,
			Timestamp: message.Timestamp,
		})
		return nil
	})
	if err != nil {
		return err
	}

	s.subMu.Lock()
	defer s.subMu.Unlock()
	if _, ok := s.subscriptions[roomID]; ok {
		_ = unsub()
		return nil
	}
	s.subscriptions[roomID] = unsub
	s.log.Info("room subscription ready", zap.String("room_id", roomID))
	return nil
}

func (s *MessageService) StartPersistenceWorker(ctx context.Context, batchSize int, flushInterval time.Duration) {
	if batchSize <= 0 {
		batchSize = 50
	}
	if flushInterval <= 0 {
		flushInterval = 500 * time.Millisecond
	}

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
			if err := s.repo.SaveBatch(ctx, batch); err != nil {
				s.log.Error("persist batch failed", zap.Error(err), zap.Int("size", len(batch)))
			}
			batch = batch[:0]
		}

		for {
			select {
			case <-ctx.Done():
				flush()
				return
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
}

func (s *MessageService) Send(ctx context.Context, req SendRequest) (protocol.BroadcastMessage, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" || len(content) > 200 {
		return protocol.BroadcastMessage{}, ErrInvalidContent
	}

	if err := s.checkLimit(ctx, req); err != nil {
		return protocol.BroadcastMessage{}, err
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

	select {
	case s.persistCh <- model.Danmaku{
		ID:        msg.MessageID,
		RoomID:    msg.RoomID,
		UserID:    msg.UserID,
		Content:   msg.Content,
		CreatedAt: msg.Timestamp,
	}:
	default:
		s.log.Warn("persist channel full, dropping message", zap.String("message_id", msg.MessageID))
	}
	return msg, nil
}

func (s *MessageService) ListHistory(ctx context.Context, roomID string, limit int, before time.Time) ([]model.Danmaku, error) {
	return s.repo.ListByRoom(ctx, roomID, limit, before)
}

func (s *MessageService) checkLimit(ctx context.Context, req SendRequest) error {
	ok, retryAfter, err := s.limiter.Allow(ctx, "lim:user:"+req.UserID, s.cfg.UserCount, s.cfg.UserWindow)
	if err != nil {
		return err
	}
	if !ok {
		return RateLimitError{Reason: "user limit exceeded", RetryAfter: retryAfter}
	}

	ok, retryAfter, err = s.limiter.Allow(ctx, "lim:ip:"+req.IP, s.cfg.IPCount, s.cfg.IPWindow)
	if err != nil {
		return err
	}
	if !ok {
		return RateLimitError{Reason: "ip limit exceeded", RetryAfter: retryAfter}
	}

	ok, retryAfter, err = s.limiter.Allow(ctx, "lim:room:"+req.RoomID, s.cfg.RoomCount, s.cfg.RoomWindow)
	if err != nil {
		return err
	}
	if !ok {
		return RateLimitError{Reason: "room limit exceeded", RetryAfter: retryAfter}
	}
	return nil
}

func (s *MessageService) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		close(s.persistCh)
		s.persistWorkers.Wait()

		s.subMu.Lock()
		defer s.subMu.Unlock()
		for roomID, unsub := range s.subscriptions {
			if err := unsub(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("unsubscribe %s: %w", roomID, err)
			}
		}
		s.subscriptions = make(map[string]func() error)
		if err := s.broker.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	})
	return firstErr
}
