package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/auth"
	"github.com/Gav1nnn/DanmakuX/internal/metrics"
	"github.com/Gav1nnn/DanmakuX/internal/service"
	"github.com/gin-gonic/gin"
)

// RouterDeps 定义了创建路由器所需的依赖项
type RouterDeps struct {
	JWTSecret string
	TokenTTL  time.Duration
	Message   *service.MessageService
	Metrics   *metrics.Metrics
}

// NewRouter 创建 Gin 路由器并挂载 HTTP/WS 入口。
func NewRouter(deps RouterDeps, wsHandler gin.HandlerFunc) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), gin.Logger())
	router.Static("/examples", "./examples")
	router.GET("/healthz", func(c *gin.Context) { // 健康检查端点
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/metrics", metricsHandler(deps.Metrics))                                // Prometheus 指标端点
	router.POST("/api/v1/auth/guest", guestTokenHandler(deps.JWTSecret, deps.TokenTTL)) // 游客登录获取 JWT
	router.GET("/api/v1/rooms/:room_id/messages", historyHandler(deps.Message))         // 获取弹幕历史记录
	router.GET("/ws", wsHandler)                                                        // WebSocket 连接端点
	return router
}

// metricsHandler 输出 Prometheus text exposition 格式的运行指标。
func metricsHandler(metricsCollector *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		if metricsCollector == nil {
			c.String(http.StatusServiceUnavailable, "metrics collector unavailable\n")
			return
		}
		c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(metricsCollector.PrometheusText()))
	}
}

// guestTokenHandler 处理游客鉴权，签发短期 JWT。
func guestTokenHandler(secret string, ttl time.Duration) gin.HandlerFunc {
	type req struct {
		UserID string `json:"user_id"`
	}
	return func(c *gin.Context) {
		var body req
		if err := c.ShouldBindJSON(&body); err != nil || body.UserID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
			return
		}

		token, err := auth.GenerateToken(secret, body.UserID, ttl)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "expires_in": ttl.Seconds()})
	}
}

// historyHandler 查询房间历史弹幕，支持数量和时间游标。
func historyHandler(messageSrv *service.MessageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		roomID := c.Param("room_id")
		if roomID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "room_id is required"})
			return
		}

		limit := 50
		if q := c.Query("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil {
				limit = n
			}
		}
		var before time.Time
		if q := c.Query("before"); q != "" {
			t, err := time.Parse(time.RFC3339, q)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "before must be RFC3339"})
				return
			}
			before = t
		}

		history, err := messageSrv.ListHistory(c.Request.Context(), roomID, limit, before)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query history"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": history})
	}
}
