package room

import (
	"testing"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
)

func TestHubJoinLeaveAndBroadcast(t *testing.T) {
	hub := NewHub()
	c1 := &Client{UserID: "u1", RoomID: "r1", send: make(chan protocol.WSOutboundMessage, 4)}
	c2 := &Client{UserID: "u2", RoomID: "r1", send: make(chan protocol.WSOutboundMessage, 4)}

	if n := hub.Join(c1); n != 1 {
		t.Fatalf("expected room size 1, got %d", n)
	}
	if n := hub.Join(c2); n != 2 {
		t.Fatalf("expected room size 2, got %d", n)
	}

	count := hub.BroadcastLocal("r1", protocol.WSOutboundMessage{Type: "danmaku", Content: "hello"})
	if count != 2 {
		t.Fatalf("expected broadcast count 2, got %d", count)
	}

	if n := hub.Leave(c1); n != 1 {
		t.Fatalf("expected room size 1 after leave, got %d", n)
	}
	if n := hub.Leave(c2); n != 0 {
		t.Fatalf("expected room size 0 after leave, got %d", n)
	}
}
