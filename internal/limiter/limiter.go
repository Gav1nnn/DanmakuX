package limiter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidLimiterResult = errors.New("invalid limiter script result")
	ErrInvalidLimitRules    = errors.New("invalid limit rules")
)

// Rule 描述一个参与原子判断的限流维度。
type Rule struct {
	Scope  string
	Key    string
	Limit  int
	Window time.Duration
}

// Decision 表示多维限流的统一判断结果。
type Decision struct {
	Allowed       bool
	RejectedScope string
	RetryAfter    time.Duration
}

// Limiter 抽象多维限流器接口。
type Limiter interface {
	Allow(ctx context.Context, rules []Rule) (Decision, error)
}

// RedisFixedWindowLimiter 使用 Redis + Lua 实现固定窗口限流。
type RedisFixedWindowLimiter struct {
	client *redis.Client
}

// NewRedisFixedWindowLimiter 创建固定窗口限流器。
func NewRedisFixedWindowLimiter(client *redis.Client) *RedisFixedWindowLimiter {
	return &RedisFixedWindowLimiter{client: client}
}

// fixedWindowScript 先检查全部规则，全部通过后再统一递增计数。
// 返回 [是否允许(1/0), 被拒绝规则下标(从 1 开始), 剩余 TTL(ms)]。
var fixedWindowScript = redis.NewScript(`
for i = 1, #KEYS do
  local current = tonumber(redis.call("GET", KEYS[i]) or "0")
  local limit = tonumber(ARGV[(i - 1) * 2 + 1])
  local window = tonumber(ARGV[(i - 1) * 2 + 2])
  if current >= limit then
    local ttl = redis.call("PTTL", KEYS[i])
    if ttl < 0 then
      redis.call("PEXPIRE", KEYS[i], window)
      ttl = window
    end
    return {0, i, ttl}
  end
end

for i = 1, #KEYS do
  local current = redis.call("INCR", KEYS[i])
  if current == 1 then
    redis.call("PEXPIRE", KEYS[i], ARGV[(i - 1) * 2 + 2])
  end
end

return {1, 0, 0}
`)

// Allow 在一次 Lua 调用中完成所有规则的检查与计数。
func (l *RedisFixedWindowLimiter) Allow(ctx context.Context, rules []Rule) (Decision, error) {
	if len(rules) == 0 {
		return Decision{}, ErrInvalidLimitRules
	}

	keys := make([]string, 0, len(rules))
	args := make([]interface{}, 0, len(rules)*2)
	for _, rule := range rules {
		if rule.Scope == "" || rule.Key == "" || rule.Limit <= 0 || rule.Window.Milliseconds() <= 0 {
			return Decision{}, fmt.Errorf("%w: scope=%q", ErrInvalidLimitRules, rule.Scope)
		}
		keys = append(keys, rule.Key)
		args = append(args, rule.Limit, rule.Window.Milliseconds())
	}

	res, err := fixedWindowScript.Run(ctx, l.client, keys, args...).Result()
	if err != nil {
		return Decision{}, err
	}

	values, ok := res.([]interface{})
	if !ok || len(values) != 3 {
		return Decision{}, ErrInvalidLimiterResult
	}

	allowedNum, okAllowed := values[0].(int64)
	rejectedIndex, okIndex := values[1].(int64)
	ttlNum, okTTL := values[2].(int64)
	if !okAllowed || !okIndex || !okTTL || (allowedNum != 0 && allowedNum != 1) {
		return Decision{}, ErrInvalidLimiterResult
	}
	if allowedNum == 1 {
		return Decision{Allowed: true}, nil
	}
	if rejectedIndex < 1 || rejectedIndex > int64(len(rules)) {
		return Decision{}, ErrInvalidLimiterResult
	}
	if ttlNum < 0 {
		ttlNum = 0
	}
	return Decision{
		RejectedScope: rules[rejectedIndex-1].Scope,
		RetryAfter:    time.Duration(ttlNum) * time.Millisecond,
	}, nil
}
