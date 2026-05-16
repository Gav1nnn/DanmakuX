package protocol

import "time"

// WSInboundMessage 表示客户端通过 WebSocket 上行发送的消息体。
type WSInboundMessage struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// WSOutboundMessage 表示服务端下行推送给客户端的消息体。
type WSOutboundMessage struct {
	Type      string    `json:"type"`
	MessageID string    `json:"message_id,omitempty"`
	RoomID    string    `json:"room_id,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
	Content   string    `json:"content,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// BroadcastMessage 是节点间通过 Redis Pub/Sub 传递的统一广播消息。
type BroadcastMessage struct {
	MessageID string    `json:"message_id"`
	RoomID    string    `json:"room_id"`
	UserID    string    `json:"user_id"`
	Content   string    `json:"content"`
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
}
