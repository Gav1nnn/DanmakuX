package room

import (
	"context"
	"sync"
	"time"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
	"github.com/gorilla/websocket"
)

// ClientConfig controls websocket read/write behavior.
type ClientConfig struct {
	WriteWait      time.Duration
	PongWait       time.Duration
	PingPeriod     time.Duration
	MaxMessageSize int64
}

// Client is a websocket user connection.
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

func (c *Client) Enqueue(msg protocol.WSOutboundMessage) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

func (c *Client) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		err = c.conn.Close()
	})
	return err
}

func (c *Client) Conn() *websocket.Conn {
	return c.conn
}

func (c *Client) Send() <-chan protocol.WSOutboundMessage {
	return c.send
}

func (c *Client) Config() ClientConfig {
	return c.cfg
}
