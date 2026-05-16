package room

import (
	"context"
	"sync"
	"time"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/gorilla/websocket"
)

// ClientConfig 控制客户端连接的读写超时和心跳参数。
type ClientConfig struct {
	WriteWait      time.Duration
	PongWait       time.Duration
	PingPeriod     time.Duration
	MaxMessageSize int64
}

// Client 表示一个用户在某个房间内的 WebSocket 连接。
type Client struct {
	UserID string
	RoomID string
	IP     string

	conn   *websocket.Conn
	send   chan protocol.WSOutboundMessage
	cfg    ClientConfig
	cancel context.CancelFunc
	once   sync.Once
}

// NewClient 创建连接对象并返回其生命周期上下文。
func NewClient(userID, roomID, ip string, conn *websocket.Conn, cfg ClientConfig) (*Client, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		UserID: userID,
		RoomID: roomID,
		IP:     ip,
		conn:   conn,
		send:   make(chan protocol.WSOutboundMessage, 256),
		cfg:    cfg,
		cancel: cancel,
	}, ctx
}

// Enqueue 尝试将消息放入发送队列，队列满则立即失败。
func (c *Client) Enqueue(msg protocol.WSOutboundMessage) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// Close 幂等关闭连接并取消上下文。
func (c *Client) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		err = c.conn.Close()
	})
	return err
}

// Conn 返回底层 websocket 连接。
func (c *Client) Conn() *websocket.Conn {
	return c.conn
}

// Send 返回只读发送队列，供 writePump 消费。
func (c *Client) Send() <-chan protocol.WSOutboundMessage {
	return c.send
}

// Config 返回连接参数快照。
func (c *Client) Config() ClientConfig {
	return c.cfg
}
