package limiter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestAllowUpdatesAllRulesOnlyWhenEveryRulePasses(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	limiter := NewRedisFixedWindowLimiter(client)
	rules := []Rule{
		{Scope: "user", Key: "lim:{room-1}:user:u1", Limit: 1, Window: time.Minute},
		{Scope: "ip", Key: "lim:{room-1}:ip:127.0.0.1", Limit: 10, Window: time.Minute},
		{Scope: "room", Key: "lim:{room-1}:room", Limit: 10, Window: time.Minute},
	}

	first, err := limiter.Allow(context.Background(), rules)
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !first.Allowed {
		t.Fatalf("expected first request to pass, got %+v", first)
	}

	second, err := limiter.Allow(context.Background(), rules)
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	if second.Allowed || second.RejectedScope != "user" {
		t.Fatalf("expected user rejection, got %+v", second)
	}
	if second.RetryAfter <= 0 {
		t.Fatalf("expected a positive retry duration, got %s", second.RetryAfter)
	}

	for _, key := range []string{rules[1].Key, rules[2].Key} {
		value, err := redisServer.Get(key)
		if err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		if value != "1" {
			t.Fatalf("rejected request consumed quota for %s: value=%s", key, value)
		}
	}
}

func TestAllowRejectsInvalidRules(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	limiter := NewRedisFixedWindowLimiter(client)
	_, err := limiter.Allow(context.Background(), []Rule{
		{Scope: "user", Key: "key", Limit: 0, Window: time.Second},
	})
	if !errors.Is(err, ErrInvalidLimitRules) {
		t.Fatalf("expected ErrInvalidLimitRules, got %v", err)
	}
}
