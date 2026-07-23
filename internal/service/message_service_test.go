package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/broker"
	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/Gav1nnn/DanmakuX/internal/model"
	"github.com/Gav1nnn/DanmakuX/internal/room"
	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"go.uber.org/zap"
)

type fakeBroker struct {
	mu               sync.Mutex
	published        []protocol.BroadcastMessage
	subscribeCalls   int
	unsubscribeCalls int
}

func (b *fakeBroker) Publish(_ context.Context, msg protocol.BroadcastMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, msg)
	return nil
}

func (b *fakeBroker) Subscribe(_ context.Context, _ string, _ broker.Handler) (func() error, error) {
	b.mu.Lock()
	b.subscribeCalls++
	b.mu.Unlock()

	var once sync.Once
	return func() error {
		once.Do(func() {
			b.mu.Lock()
			b.unsubscribeCalls++
			b.mu.Unlock()
		})
		return nil
	}, nil
}

func (b *fakeBroker) Close() error {
	return nil
}

func (b *fakeBroker) subscriptionCounts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subscribeCalls, b.unsubscribeCalls
}

type fakeRepo struct {
	saved [][]model.Danmaku
}

func (r *fakeRepo) SaveBatch(_ context.Context, messages []model.Danmaku) error {
	cp := append([]model.Danmaku(nil), messages...)
	r.saved = append(r.saved, cp)
	return nil
}

func (r *fakeRepo) ListByRoom(_ context.Context, _ string, _ int, _ time.Time) ([]model.Danmaku, error) {
	return nil, nil
}

type fakeLimiter struct {
	errByKey map[string]error
}

func (l fakeLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration, error) {
	if err := l.errByKey[key]; err != nil {
		return false, 1500 * time.Millisecond, err
	}
	return true, 0, nil
}

func newTestService(br *fakeBroker, repo *fakeRepo, lim fakeLimiter) *MessageService {
	return NewMessageService(
		"node-test",
		config.LimitConfig{
			UserCount:  5,
			UserWindow: time.Second,
			IPCount:    20,
			IPWindow:   time.Second,
			RoomCount:  200,
			RoomWindow: time.Second,
		},
		zap.NewNop(),
		room.NewHub(),
		br,
		repo,
		lim,
		nil,
	)
}

func TestSendRejectsInvalidContentBeforePublish(t *testing.T) {
	t.Parallel()

	br := &fakeBroker{}
	svc := newTestService(br, &fakeRepo{}, fakeLimiter{})

	if _, err := svc.Send(context.Background(), SendRequest{
		UserID:  "u1",
		RoomID:  "room-1",
		IP:      "127.0.0.1",
		Content: "   ",
	}); !errors.Is(err, ErrInvalidContent) {
		t.Fatalf("expected ErrInvalidContent, got %v", err)
	}
	if len(br.published) != 0 {
		t.Fatalf("invalid content should not be published, got %d messages", len(br.published))
	}
}

func TestSendReturnsRateLimitError(t *testing.T) {
	t.Parallel()

	svc := newTestService(&fakeBroker{}, &fakeRepo{}, fakeLimiter{
		errByKey: map[string]error{
			"lim:user:u1": RateLimitError{Reason: "user limit exceeded", RetryAfter: time.Second},
		},
	})

	_, err := svc.Send(context.Background(), SendRequest{
		UserID:  "u1",
		RoomID:  "room-1",
		IP:      "127.0.0.1",
		Content: "hello",
	})

	var rateLimitErr RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected RateLimitError, got %v", err)
	}
	if rateLimitErr.Reason != "user limit exceeded" {
		t.Fatalf("unexpected reason: %s", rateLimitErr.Reason)
	}
}

func TestSendDropsPersistenceWhenQueueFullButStillPublishes(t *testing.T) {
	t.Parallel()

	br := &fakeBroker{}
	svc := newTestService(br, &fakeRepo{}, fakeLimiter{})
	for i := 0; i < cap(svc.persistCh); i++ {
		svc.persistCh <- model.Danmaku{ID: "filled"}
	}

	msg, err := svc.Send(context.Background(), SendRequest{
		UserID:  "u1",
		RoomID:  "room-1",
		IP:      "127.0.0.1",
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if msg.MessageID == "" {
		t.Fatal("expected generated message id")
	}
	if len(br.published) != 1 {
		t.Fatalf("expected message to stay on realtime publish path, got %d published", len(br.published))
	}
}

func TestRoomSubscriptionReferenceLifecycle(t *testing.T) {
	t.Parallel()

	br := &fakeBroker{}
	svc := newTestService(br, &fakeRepo{}, fakeLimiter{})

	if err := svc.AcquireRoomSubscription(context.Background(), "room-1"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := svc.AcquireRoomSubscription(context.Background(), "room-1"); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if subscribed, unsubscribed := br.subscriptionCounts(); subscribed != 1 || unsubscribed != 0 {
		t.Fatalf("unexpected counts after acquire: subscribed=%d unsubscribed=%d", subscribed, unsubscribed)
	}

	if err := svc.ReleaseRoomSubscription("room-1"); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if subscribed, unsubscribed := br.subscriptionCounts(); subscribed != 1 || unsubscribed != 0 {
		t.Fatalf("subscription released while still referenced: subscribed=%d unsubscribed=%d", subscribed, unsubscribed)
	}

	if err := svc.ReleaseRoomSubscription("room-1"); err != nil {
		t.Fatalf("second release: %v", err)
	}
	if subscribed, unsubscribed := br.subscriptionCounts(); subscribed != 1 || unsubscribed != 1 {
		t.Fatalf("unexpected counts after final release: subscribed=%d unsubscribed=%d", subscribed, unsubscribed)
	}
}

func TestRoomSubscriptionConcurrentAcquireUsesSingleSubscription(t *testing.T) {
	t.Parallel()

	br := &fakeBroker{}
	svc := newTestService(br, &fakeRepo{}, fakeLimiter{})

	const clients = 32
	var wg sync.WaitGroup
	errCh := make(chan error, clients)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- svc.AcquireRoomSubscription(context.Background(), "room-1")
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent acquire: %v", err)
		}
	}
	if subscribed, _ := br.subscriptionCounts(); subscribed != 1 {
		t.Fatalf("expected one broker subscription, got %d", subscribed)
	}

	for i := 0; i < clients; i++ {
		if err := svc.ReleaseRoomSubscription("room-1"); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
	}
	if _, unsubscribed := br.subscriptionCounts(); unsubscribed != 1 {
		t.Fatalf("expected one broker unsubscribe, got %d", unsubscribed)
	}
}
