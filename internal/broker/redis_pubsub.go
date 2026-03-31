package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/redis/go-redis/v9"
)

type RedisPubSubBroker struct {
	client *redis.Client

	mu       sync.Mutex
	closers  map[string]func() error
	subIndex uint64
}

func NewRedisPubSubBroker(client *redis.Client) *RedisPubSubBroker {
	return &RedisPubSubBroker{
		client:  client,
		closers: make(map[string]func() error),
	}
}

func channelName(roomID string) string {
	return fmt.Sprintf("dm:room:%s", roomID)
}

func (b *RedisPubSubBroker) Publish(ctx context.Context, msg protocol.BroadcastMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, channelName(msg.RoomID), payload).Err()
}

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
				var payload protocol.BroadcastMessage
				if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
					continue
				}
				_ = handler(ctx, payload)
			}
		}
	}()

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
