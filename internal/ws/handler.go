package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/auth"
	"github.com/Gav1nnn/DanmakuX/internal/room"
	"github.com/Gav1nnn/DanmakuX/internal/service"
	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type Handler struct {
	log           *zap.Logger
	hub           *room.Hub
	messageSrv    *service.MessageService
	jwtSecret     string
	clientCfg     room.ClientConfig
	wsReadBuffer  int
	wsWriteBuffer int
}

func NewHandler(
	log *zap.Logger,
	hub *room.Hub,
	messageSrv *service.MessageService,
	jwtSecret string,
	clientCfg room.ClientConfig,
	wsReadBuffer int,
	wsWriteBuffer int,
) *Handler {
	return &Handler{
		log:           log,
		hub:           hub,
		messageSrv:    messageSrv,
		jwtSecret:     jwtSecret,
		clientCfg:     clientCfg,
		wsReadBuffer:  wsReadBuffer,
		wsWriteBuffer: wsWriteBuffer,
	}
}

func (h *Handler) ServeWS(c *gin.Context) {
	roomID := c.Query("room_id")
	token := c.Query("token")
	if roomID == "" || token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "room_id and token are required"})
		return
	}

	claims, err := auth.ParseToken(h.jwtSecret, token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  h.wsReadBuffer,
		WriteBufferSize: h.wsWriteBuffer,
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Error("upgrade websocket failed", zap.Error(err))
		return
	}

	client, clientCtx := room.NewClient(claims.UserID, roomID, c.ClientIP(), conn, h.clientCfg)
	h.hub.Join(client)
	if err := h.messageSrv.EnsureRoomSubscription(context.Background(), roomID); err != nil {
		h.log.Error("subscribe room failed", zap.String("room_id", roomID), zap.Error(err))
		h.hub.Leave(client)
		_ = client.Close()
		_ = conn.Close()
		return
	}

	h.log.Info("client joined room", zap.String("room_id", roomID), zap.String("user_id", claims.UserID))

	done := make(chan struct{})
	go h.writePump(clientCtx, client, done)
	h.readPump(clientCtx, client)

	_ = client.Close()
	<-done
	h.hub.Leave(client)
	h.log.Info("client left room", zap.String("room_id", roomID), zap.String("user_id", claims.UserID))
}

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
