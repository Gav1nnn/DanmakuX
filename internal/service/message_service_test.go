package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/broker"
	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/Gav1nnn/DanmakuX/internal/limiter"
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
	mu    sync.Mutex
	saved [][]model.Danmaku
}

func (r *fakeRepo) SaveBatch(_ context.Context, messages []model.Danmaku) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := append([]model.Danmaku(nil), messages...)
	r.saved = append(r.saved, cp)
	return nil
}

func (r *fakeRepo) ListByRoom(_ context.Context, _ string, _ int, _ time.Time) ([]model.Danmaku, error) {
	return nil, nil
}

func (r *fakeRepo) savedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, batch := range r.saved {
		total += len(batch)
	}
	return total
}

type fakeLimiter struct {
	decision limiter.Decision
	err      error
}

func (l fakeLimiter) Allow(_ context.Context, _ []limiter.Rule) (limiter.Decision, error) {
	if l.err != nil {
		return limiter.Decision{}, l.err
	}
	if l.decision == (limiter.Decision{}) {
		return limiter.Decision{Allowed: true}, nil
	}
	return l.decision, nil
}

type captureLimiter struct {
	mu    sync.Mutex
	calls int
	rules []limiter.Rule
}

func (l *captureLimiter) Allow(_ context.Context, rules []limiter.Rule) (limiter.Decision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.rules = append([]limiter.Rule(nil), rules...)
	return limiter.Decision{Allowed: true}, nil
}

func (l *captureLimiter) snapshot() (int, []limiter.Rule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls, append([]limiter.Rule(nil), l.rules...)
}

func newTestService(br *fakeBroker, repo *fakeRepo, lim limiter.Limiter) *MessageService {
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
		decision: limiter.Decision{
			RejectedScope: "user",
			RetryAfter:    time.Second,
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

func TestSendChecksAllLimitScopesInOneCall(t *testing.T) {
	t.Parallel()

	lim := &captureLimiter{}
	svc := newTestService(&fakeBroker{}, &fakeRepo{}, lim)
	if _, err := svc.Send(context.Background(), SendRequest{
		UserID:  "u1",
		RoomID:  "room-1",
		IP:      "127.0.0.1",
		Content: "hello",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	calls, rules := lim.snapshot()
	if calls != 1 {
		t.Fatalf("expected one limiter call, got %d", calls)
	}
	if len(rules) != 3 {
		t.Fatalf("expected three limit rules, got %d", len(rules))
	}

	expectedScopes := []string{"user", "ip", "room"}
	hashTag := ""
	for i, rule := range rules {
		if rule.Scope != expectedScopes[i] {
			t.Fatalf("rule %d scope: expected %s, got %s", i, expectedScopes[i], rule.Scope)
		}
		tagEnd := strings.IndexByte(rule.Key, '}')
		tagStart := strings.IndexByte(rule.Key, '{')
		if tagStart < 0 || tagEnd <= tagStart {
			t.Fatalf("rule %d has no Redis hash tag: %s", i, rule.Key)
		}
		currentTag := rule.Key[tagStart : tagEnd+1]
		if hashTag == "" {
			hashTag = currentTag
		} else if currentTag != hashTag {
			t.Fatalf("rules use different Redis hash tags: %s and %s", hashTag, currentTag)
		}
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

func TestPersistenceWorkerFlushesByBatchSizeAndOnClose(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := newTestService(&fakeBroker{}, repo, fakeLimiter{})
	svc.StartPersistenceWorker(2, time.Hour)

	for i := 0; i < 3; i++ {
		if _, err := svc.Send(context.Background(), SendRequest{
			UserID:  "u1",
			RoomID:  "room-1",
			IP:      "127.0.0.1",
			Content: "hello",
		}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	if err := svc.Close(); err != nil {
		t.Fatalf("close service: %v", err)
	}
	if got := repo.savedCount(); got != 3 {
		t.Fatalf("expected 3 persisted messages, got %d", got)
	}
}

func TestPersistenceWorkerFlushesByInterval(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := newTestService(&fakeBroker{}, repo, fakeLimiter{})
	svc.StartPersistenceWorker(100, 10*time.Millisecond)
	defer func() {
		if err := svc.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()

	if _, err := svc.Send(context.Background(), SendRequest{
		UserID:  "u1",
		RoomID:  "room-1",
		IP:      "127.0.0.1",
		Content: "hello",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if repo.savedCount() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("message was not flushed by interval")
}

func TestSendDuringCloseDoesNotPanic(t *testing.T) {
	t.Parallel()

	svc := newTestService(&fakeBroker{}, &fakeRepo{}, fakeLimiter{})
	svc.StartPersistenceWorker(50, 10*time.Millisecond)

	const senders = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < senders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = svc.Send(context.Background(), SendRequest{
				UserID:  "u1",
				RoomID:  "room-1",
				IP:      "127.0.0.1",
				Content: "hello",
			})
		}()
	}

	close(start)
	if err := svc.Close(); err != nil {
		t.Fatalf("close service: %v", err)
	}
	wg.Wait()
}
