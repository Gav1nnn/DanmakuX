package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/redis/go-redis/v9"
)

// RedisPubSubBroker 基于 Redis Pub/Sub 实现 Broker 接口。
type RedisPubSubBroker struct {
	client *redis.Client

	mu       sync.Mutex
	closers  map[string]func() error
	subIndex uint64
}

// NewRedisPubSubBroker 创建 Redis 广播实现。
func NewRedisPubSubBroker(client *redis.Client) *RedisPubSubBroker {
	return &RedisPubSubBroker{
		client:  client,
		closers: make(map[string]func() error),
	}
}

// channelName 将房间 ID 映射为 Redis 频道名。
func channelName(roomID string) string {
	return fmt.Sprintf("dm:room:%s", roomID)
}

// Publish 把广播消息序列化后发布到对应房间频道。
func (b *RedisPubSubBroker) Publish(ctx context.Context, msg protocol.BroadcastMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, channelName(msg.RoomID), payload).Err()
}

// Subscribe 订阅指定房间频道，并在后台协程中持续消费消息。
func (b *RedisPubSubBroker) Subscribe(ctx context.Context, roomID string, handler Handler) (func() error, error) {
	pubsub := b.client.Subscribe(ctx, channelName(roomID))
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, err
	}

	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ch := pubsub.Channel()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				// 单条消息解码失败时跳过，避免中断整个订阅协程。
				var payload protocol.BroadcastMessage
				if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
					continue
				}
				_ = handler(ctx, payload)
			}
		}
	}()

	// unsubscribe 需要保证幂等，避免重复 close 引发 panic。
	unsubscribe := func() error {
		var closeErr error
		once.Do(func() {
			close(stop)
			closeErr = pubsub.Close()
		})
		return closeErr
	}

	b.mu.Lock()
	b.subIndex++
	id := fmt.Sprintf("sub-%d", b.subIndex)
	b.closers[id] = unsubscribe
	b.mu.Unlock()

	return func() error {
		b.mu.Lock()
		delete(b.closers, id)
		b.mu.Unlock()
		return unsubscribe()
	}, nil
}

// Close 关闭 broker 持有的所有订阅。
func (b *RedisPubSubBroker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var firstErr error
	for id, closeFn := range b.closers {
		if err := closeFn(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", id, err)
		}
		delete(b.closers, id)
	}
	return firstErr
}
