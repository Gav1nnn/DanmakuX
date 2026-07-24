package ws

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/auth"
	"github.com/Gav1nnn/DanmakuX/internal/broker"
	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/Gav1nnn/DanmakuX/internal/limiter"
	"github.com/Gav1nnn/DanmakuX/internal/metrics"
	"github.com/Gav1nnn/DanmakuX/internal/model"
	"github.com/Gav1nnn/DanmakuX/internal/room"
	"github.com/Gav1nnn/DanmakuX/internal/service"
	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type shutdownTestBroker struct{}

func (shutdownTestBroker) Publish(context.Context, protocol.BroadcastMessage) error {
	return nil
}

func (shutdownTestBroker) Subscribe(context.Context, string, broker.Handler) (func() error, error) {
	return func() error { return nil }, nil
}

func (shutdownTestBroker) Close() error {
	return nil
}

type shutdownTestRepository struct{}

func (shutdownTestRepository) SaveBatch(context.Context, []model.Danmaku) error {
	return nil
}

func (shutdownTestRepository) ListByRoom(context.Context, string, int, time.Time) ([]model.Danmaku, error) {
	return nil, nil
}

type shutdownTestLimiter struct{}

func (shutdownTestLimiter) Allow(context.Context, []limiter.Rule) (limiter.Decision, error) {
	return limiter.Decision{Allowed: true}, nil
}

func TestHandlerShutdownClosesActiveConnections(t *testing.T) {
	gin.SetMode(gin.TestMode)

	hub := room.NewHub()
	collector := metrics.New()
	messageService := service.NewMessageService(
		"node-test",
		config.LimitConfig{},
		zap.NewNop(),
		hub,
		shutdownTestBroker{},
		shutdownTestRepository{},
		shutdownTestLimiter{},
		collector,
	)
	handler := NewHandler(
		zap.NewNop(),
		hub,
		messageService,
		"test-secret",
		collector,
		room.ClientConfig{
			WriteWait:      time.Second,
			PongWait:       time.Minute,
			PingPeriod:     30 * time.Second,
			MaxMessageSize: 4096,
		},
		1024,
		1024,
	)

	router := gin.New()
	router.GET("/ws", handler.ServeWS)
	server := httptest.NewServer(router)
	defer server.Close()

	token := mustToken(t, "test-secret", "user-1")
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?room_id=room-1&token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	waitForRoomSize(t, hub, "room-1", 1)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := handler.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown handler: %v", err)
	}
	if size := hub.RoomSize("room-1"); size != 0 {
		t.Fatalf("expected empty room after shutdown, got %d clients", size)
	}

	if _, _, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		t.Fatal("expected new websocket connection to be rejected during shutdown")
	}
}

func mustToken(t *testing.T, secret, userID string) string {
	t.Helper()
	token, err := auth.GenerateToken(secret, userID, time.Minute)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return token
}

func waitForRoomSize(t *testing.T, hub *room.Hub, roomID string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.RoomSize(roomID) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("room %s did not reach size %d", roomID, want)
}
