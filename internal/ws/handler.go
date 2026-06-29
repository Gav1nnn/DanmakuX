package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/auth"
	"github.com/Gav1nnn/DanmakuX/internal/metrics"
	"github.com/Gav1nnn/DanmakuX/internal/room"
	"github.com/Gav1nnn/DanmakuX/internal/service"
	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Handler 负责 WebSocket 连接接入、读写协程和消息转发。
type Handler struct {
	log           *zap.Logger             // 日志记录器
	hub           *room.Hub               // 房间管理器
	messageSrv    *service.MessageService // 消息服务
	jwtSecret     string                  // JWT 密钥
	metrics       *metrics.Metrics        // 指标收集器
	clientCfg     room.ClientConfig       // 客户端配置
	wsReadBuffer  int                     // WebSocket 读缓冲区大小
	wsWriteBuffer int                     // WebSocket 写缓冲区大小
}

// NewHandler 创建 WebSocket 处理器。
func NewHandler(
	log *zap.Logger,
	hub *room.Hub,
	messageSrv *service.MessageService,
	jwtSecret string,
	metricsCollector *metrics.Metrics,
	clientCfg room.ClientConfig,
	wsReadBuffer int,
	wsWriteBuffer int,
) *Handler {
	return &Handler{
		log:           log,
		hub:           hub,
		messageSrv:    messageSrv,
		jwtSecret:     jwtSecret,
		metrics:       metricsCollector,
		clientCfg:     clientCfg,
		wsReadBuffer:  wsReadBuffer,
		wsWriteBuffer: wsWriteBuffer,
	}
}

// ServeWS 处理 WebSocket 握手、鉴权、入房和资源回收。
func (h *Handler) ServeWS(c *gin.Context) {
	// 从query参数中获取 room_id 和 token
	roomID := c.Query("room_id")
	token := c.Query("token")
	// 解析token，验证用户身份
	if roomID == "" || token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "room_id and token are required"})
		return
	}

	claims, err := auth.ParseToken(h.jwtSecret, token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	// 升级HTTP连接到WebSocket
	upgrader := websocket.Upgrader{
		ReadBufferSize:  h.wsReadBuffer,
		WriteBufferSize: h.wsWriteBuffer,
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}
	// 创建客户端对象并加入房间
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Error("upgrade websocket failed", zap.Error(err))
		return
	}
	// 确保房间订阅成功，如果失败则清理资源并返回错误
	client, clientCtx := room.NewClient(claims.UserID, roomID, c.ClientIP(), conn, h.clientCfg)
	h.hub.Join(client)
	if err := h.messageSrv.EnsureRoomSubscription(context.Background(), roomID); err != nil {
		h.log.Error("subscribe room failed", zap.String("room_id", roomID), zap.Error(err))
		h.hub.Leave(client)
		_ = client.Close()
		_ = conn.Close()
		return
	}
	// 连接建立成功，记录日志并启动读写协程
	if h.metrics != nil {
		h.metrics.IncWSConnection()
		defer h.metrics.DecWSConnection()
	}
	h.log.Info("client joined room", zap.String("room_id", roomID), zap.String("user_id", claims.UserID))

	done := make(chan struct{})
	go h.writePump(clientCtx, client, done)
	h.readPump(clientCtx, client)
	// 连接关闭，清理资源并记录日志
	_ = client.Close()
	<-done
	h.hub.Leave(client)
	h.log.Info("client left room", zap.String("room_id", roomID), zap.String("user_id", claims.UserID))
}

// readPump 持续读取客户端上行消息并调用业务服务。
func (h *Handler) readPump(ctx context.Context, client *room.Client) {
	clientConn := client.Conn()
	cfg := client.Config()
	clientConn.SetReadLimit(cfg.MaxMessageSize)
	_ = clientConn.SetReadDeadline(time.Now().Add(cfg.PongWait))
	clientConn.SetPongHandler(func(string) error {
		return clientConn.SetReadDeadline(time.Now().Add(cfg.PongWait))
	})

	for {
		_, raw, err := clientConn.ReadMessage()
		if err != nil {
			return
		}
		var inbound protocol.WSInboundMessage
		if err := json.Unmarshal(raw, &inbound); err != nil {
			_ = client.Enqueue(protocol.WSOutboundMessage{
				Type:  "error",
				Error: "invalid message format",
			})
			continue
		}

		switch inbound.Type {
		case "danmaku":
			if _, err := h.messageSrv.Send(ctx, service.SendRequest{
				UserID:  client.UserID,
				RoomID:  client.RoomID,
				IP:      client.IP,
				Content: inbound.Content,
			}); err != nil {
				_ = client.Enqueue(protocol.WSOutboundMessage{
					Type:  "error",
					Error: err.Error(),
				})
			}
		case "ping":
			_ = client.Enqueue(protocol.WSOutboundMessage{Type: "pong"})
		default:
			_ = client.Enqueue(protocol.WSOutboundMessage{
				Type:  "error",
				Error: "unsupported message type",
			})
		}
	}
}

// writePump 持续从发送队列取消息写回 WebSocket，并定时发送 ping 保活。
func (h *Handler) writePump(ctx context.Context, client *room.Client, done chan struct{}) {
	defer close(done)
	cfg := client.Config()
	ticker := time.NewTicker(cfg.PingPeriod)
	defer ticker.Stop()

	clientConn := client.Conn()
	sendCh := client.Send()
	for {
		select {
		case <-ctx.Done():
			_ = clientConn.SetWriteDeadline(time.Now().Add(cfg.WriteWait))
			_ = clientConn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case <-ticker.C:
			_ = clientConn.SetWriteDeadline(time.Now().Add(cfg.WriteWait))
			if err := clientConn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case message := <-sendCh:
			_ = clientConn.SetWriteDeadline(time.Now().Add(cfg.WriteWait))
			if err := clientConn.WriteJSON(message); err != nil {
				return
			}
		}
	}
}
