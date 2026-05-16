package limiter

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrInvalidLimiterResult = errors.New("invalid limiter script result")

// Limiter 抽象限流器接口，返回是否放行和建议重试时间。
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

// RedisFixedWindowLimiter 使用 Redis + Lua 实现固定窗口限流。
type RedisFixedWindowLimiter struct {
	client *redis.Client
}

// NewRedisFixedWindowLimiter 创建固定窗口限流器。
func NewRedisFixedWindowLimiter(client *redis.Client) *RedisFixedWindowLimiter {
	return &RedisFixedWindowLimiter{client: client}
}

// fixedWindowScript 返回 [是否允许(1/0), 剩余 TTL(ms)]。
var fixedWindowScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
local ttl = redis.call("PTTL", KEYS[1])
if current > tonumber(ARGV[1]) then
  return {0, ttl}
end
return {1, ttl}
`)

// Allow 执行 Lua 脚本并解析返回值。
func (l *RedisFixedWindowLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	res, err := fixedWindowScript.Run(
		ctx,
		l.client,
		[]string{key},
		limit,
		window.Milliseconds(),
	).Result()
	if err != nil {
		return false, 0, err
	}

	values, ok := res.([]interface{})
	if !ok || len(values) != 2 {
		return false, 0, ErrInvalidLimiterResult
	}

	allowedNum, _ := values[0].(int64)
	ttlNum, _ := values[1].(int64)
	if ttlNum < 0 {
		ttlNum = 0
	}
	return allowedNum == 1, time.Duration(ttlNum) * time.Millisecond, nil
}
