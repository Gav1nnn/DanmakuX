package redis

import (
	"context"

	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/redis/go-redis/v9"
)

// New 初始化 Redis 客户端。
func New(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}

// Ping 用于启动阶段探测 Redis 连通性。
func Ping(ctx context.Context, client *redis.Client) error {
	return client.Ping(ctx).Err()
}
