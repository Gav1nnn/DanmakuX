package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config 汇总服务运行所需的全部配置。
type Config struct {
	Server ServerConfig
	MySQL  MySQLConfig
	Redis  RedisConfig
	Auth   AuthConfig
	Limit  LimitConfig
}

// ServerConfig 定义 HTTP 与 WebSocket 连接相关配置。
type ServerConfig struct {
	HTTPAddr           string
	NodeID             string
	ReadBufferSize     int
	WriteBufferSize    int
	WriteWait          time.Duration
	PongWait           time.Duration
	PingPeriod         time.Duration
	MaxMessageSizeByte int64
}

// MySQLConfig 定义 MySQL 连接池与 DSN 配置。
type MySQLConfig struct {
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
	MaxLifeTime  time.Duration
}

// RedisConfig 定义 Redis 连接参数。
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// AuthConfig 定义 JWT 鉴权参数。
type AuthConfig struct {
	JWTSecret string
	TokenTTL  time.Duration
}

// LimitConfig 定义用户/IP/房间三个维度的发送限流策略。
type LimitConfig struct {
	UserCount  int
	UserWindow time.Duration
	IPCount    int
	IPWindow   time.Duration
	RoomCount  int
	RoomWindow time.Duration
}

// Load 从环境变量加载配置，并填充默认值。
func Load() (Config, error) {
	cfg := Config{
		Server: ServerConfig{
			HTTPAddr:           getEnv("HTTP_ADDR", ":8080"),
			NodeID:             getEnv("NODE_ID", "node-1"),
			ReadBufferSize:     getEnvInt("WS_READ_BUFFER_SIZE", 1024),
			WriteBufferSize:    getEnvInt("WS_WRITE_BUFFER_SIZE", 1024),
			WriteWait:          getEnvDuration("WS_WRITE_WAIT", 10*time.Second),
			PongWait:           getEnvDuration("WS_PONG_WAIT", 60*time.Second),
			PingPeriod:         getEnvDuration("WS_PING_PERIOD", 50*time.Second),
			MaxMessageSizeByte: getEnvInt64("WS_MAX_MESSAGE_SIZE", 4096),
		},
		MySQL: MySQLConfig{
			DSN:          os.Getenv("MYSQL_DSN"),
			MaxOpenConns: getEnvInt("MYSQL_MAX_OPEN_CONNS", 20),
			MaxIdleConns: getEnvInt("MYSQL_MAX_IDLE_CONNS", 10),
			MaxLifeTime:  getEnvDuration("MYSQL_CONN_MAX_LIFETIME", 30*time.Minute),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "127.0.0.1:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		Auth: AuthConfig{
			JWTSecret: getEnv("JWT_SECRET", "change-me-in-production"),
			TokenTTL:  getEnvDuration("JWT_TOKEN_TTL", 24*time.Hour),
		},
		Limit: LimitConfig{
			UserCount:  getEnvInt("LIMIT_USER_COUNT", 5),
			UserWindow: getEnvDuration("LIMIT_USER_WINDOW", 3*time.Second),
			IPCount:    getEnvInt("LIMIT_IP_COUNT", 20),
			IPWindow:   getEnvDuration("LIMIT_IP_WINDOW", 3*time.Second),
			RoomCount:  getEnvInt("LIMIT_ROOM_COUNT", 200),
			RoomWindow: getEnvDuration("LIMIT_ROOM_WINDOW", time.Second),
		},
	}

	if cfg.MySQL.DSN == "" {
		return Config{}, fmt.Errorf("MYSQL_DSN is required")
	}
	return cfg, nil
}

// getEnv 读取字符串环境变量，未设置时返回回退值。
func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

// getEnvInt 读取 int 类型环境变量，解析失败时返回回退值。
func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// getEnvInt64 读取 int64 类型环境变量，解析失败时返回回退值。
func getEnvInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// getEnvDuration 读取 time.Duration 环境变量，格式错误时返回回退值。
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
