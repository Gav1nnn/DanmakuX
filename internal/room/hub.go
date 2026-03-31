package room

import (
	"sync"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
)

// Hub tracks all room connections on current node.
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{
		rooms: make(map[string]map[*Client]struct{}),
	}
}

func (h *Hub) Join(client *Client) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	clients, ok := h.rooms[client.RoomID]
	if !ok {
		clients = make(map[*Client]struct{})
		h.rooms[client.RoomID] = clients
	}
	clients[client] = struct{}{}
	return len(clients)
}

func (h *Hub) Leave(client *Client) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	clients, ok := h.rooms[client.RoomID]
	if !ok {
		return 0
	}
	delete(clients, client)
	if len(clients) == 0 {
		delete(h.rooms, client.RoomID)
		return 0
	}
	return len(clients)
}

func (h *Hub) BroadcastLocal(roomID string, msg protocol.WSOutboundMessage) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clients, ok := h.rooms[roomID]
	if !ok {
		return 0
	}

	count := 0
	for client := range clients {
		if client.Enqueue(msg) {
			count++
		}
	}
	return count
}

func (h *Hub) RoomSize(roomID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[roomID])
}
