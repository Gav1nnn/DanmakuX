package protocol

import "time"

// WSInboundMessage is sent by client.
type WSInboundMessage struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// WSOutboundMessage is pushed to client.
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

// BroadcastMessage is published in Redis Pub/Sub.
type BroadcastMessage struct {
	MessageID string    `json:"message_id"`
	RoomID    string    `json:"room_id"`
	UserID    string    `json:"user_id"`
	Content   string    `json:"content"`
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
}
