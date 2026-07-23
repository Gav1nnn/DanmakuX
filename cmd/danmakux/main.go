package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/api"
	"github.com/Gav1nnn/DanmakuX/internal/broker"
	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/Gav1nnn/DanmakuX/internal/limiter"
	"github.com/Gav1nnn/DanmakuX/internal/logger"
	"github.com/Gav1nnn/DanmakuX/internal/metrics"
	"github.com/Gav1nnn/DanmakuX/internal/repository"
	"github.com/Gav1nnn/DanmakuX/internal/room"
	"github.com/Gav1nnn/DanmakuX/internal/service"
	mysqlstore "github.com/Gav1nnn/DanmakuX/internal/store/mysql"
	redisstore "github.com/Gav1nnn/DanmakuX/internal/store/redis"
	"github.com/Gav1nnn/DanmakuX/internal/ws"
	_ "github.com/joho/godotenv/autoload"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load() // 从环境变量加载配置
	if err != nil {
		panic(err)
	}
	// 初始化日志记录器
	log, err := logger.New()
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()
	// 设置系统信号监听，以便优雅关闭服务器
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	// 初始化 Redis 客户端并测试连接
	redisClient := redisstore.New(cfg.Redis)
	if err := redisstore.Ping(ctx, redisClient); err != nil {
		log.Fatal("redis ping failed", zap.Error(err))
	}
	defer func() { _ = redisClient.Close() }()
	// 初始化 MySQL 数据库连接并自动迁移
	mysqlDB, err := mysqlstore.New(cfg.MySQL)
	if err != nil {
		log.Fatal("mysql connect failed", zap.Error(err))
	}
	if err := mysqlstore.AutoMigrate(ctx, mysqlDB); err != nil {
		log.Fatal("mysql migrate failed", zap.Error(err))
	}
	// 初始化核心组件：消息服务、房间管理、限流器和消息代理
	hub := room.NewHub()
	metricsCollector := metrics.New()
	lim := limiter.NewRedisFixedWindowLimiter(redisClient)
	br := broker.NewRedisPubSubBroker(redisClient)
	repo := repository.NewGormDanmakuRepository(mysqlDB)
	msgSrv := service.NewMessageService(cfg.Server.NodeID, cfg.Limit, log, hub, br, repo, lim, metricsCollector)
	msgSrv.StartPersistenceWorker(ctx, 100, 500*time.Millisecond)
	defer func() {
		if err := msgSrv.Close(); err != nil {
			log.Error("close message service failed", zap.Error(err))
		}
	}()
	// 初始化 WebSocket 处理器和 HTTP 路由
	wsHandler := ws.NewHandler(
		log,
		hub,
		msgSrv,
		cfg.Auth.JWTSecret,
		metricsCollector,
		room.ClientConfig{
			WriteWait:      cfg.Server.WriteWait,
			PongWait:       cfg.Server.PongWait,
			PingPeriod:     cfg.Server.PingPeriod,
			MaxMessageSize: cfg.Server.MaxMessageSizeByte,
		},
		cfg.Server.ReadBufferSize,
		cfg.Server.WriteBufferSize,
	)
	// 创建 HTTP 服务器并启动
	router := api.NewRouter(api.RouterDeps{
		JWTSecret: cfg.Auth.JWTSecret,
		TokenTTL:  cfg.Auth.TokenTTL,
		Message:   msgSrv,
		Metrics:   metricsCollector,
	}, wsHandler.ServeWS)
	// 启动 HTTP 服务器
	srv := &http.Server{
		Addr:    cfg.Server.HTTPAddr,
		Handler: router,
	}
	// 以独立的 goroutine 启动 HTTP 服务器，并监听错误
	go func() {
		log.Info("danmakux server started",
			zap.String("http_addr", cfg.Server.HTTPAddr),
			zap.String("node_id", cfg.Server.NodeID),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("http server failed", zap.Error(err))
		}
	}()
	// 等待系统信号以触发优雅关闭
	<-ctx.Done()
	log.Info("shutdown signal received")
	// 创建一个新的上下文用于服务器关闭，设置超时时间为 8 秒
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer shutdownCancel()
	if err := wsHandler.Shutdown(shutdownCtx); err != nil {
		log.Error("websocket shutdown failed", zap.Error(err))
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http server shutdown failed", zap.Error(err))
		os.Exit(1)
	}
	log.Info("server exited")
}
