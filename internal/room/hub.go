package room

import (
	"sync"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
)

// Hub 管理当前节点内“房间 -> 连接集合”的映射。
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*Client]struct{}
}

// NewHub 创建节点内房间管理器。
func NewHub() *Hub {
	return &Hub{
		rooms: make(map[string]map[*Client]struct{}),
	}
}

// Join 将客户端加入房间并返回加入后的房间连接数。
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

// Leave 将客户端移出房间并返回移除后的房间连接数。
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

// BroadcastLocal 向本节点指定房间广播消息，返回成功入队数。
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

// RoomSize 返回本节点某房间当前连接数。
func (h *Hub) RoomSize(roomID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[roomID])
}
