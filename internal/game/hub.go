package game

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"sync"
)

type Client struct {
	Conn     *websocket.Conn
	PlayerID string
	writeMu  sync.Mutex
}

func (c *Client) WriteJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.Conn.WriteJSON(v)
}

type Hub struct {
	mu    sync.Mutex
	rooms map[string]map[*Client]bool
}

func NewHub() *Hub { return &Hub{rooms: map[string]map[*Client]bool{}} }
func (h *Hub) Add(room string, c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[room] == nil {
		h.rooms[room] = map[*Client]bool{}
	}
	h.rooms[room][c] = true
}

// Remove returns whether the same player still has another live connection in
// this room. A reconnect may briefly overlap with an old connection closing.
func (h *Hub) Remove(room string, c *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms[room], c)
	stillConnected := false
	for client := range h.rooms[room] {
		if client.PlayerID == c.PlayerID {
			stillConnected = true
			break
		}
	}
	if len(h.rooms[room]) == 0 {
		delete(h.rooms, room)
	}
	return stillConnected
}
func (h *Hub) Broadcast(room string, v any) {
	b, e := json.Marshal(v)
	if e != nil {
		return
	}
	h.mu.Lock()
	list := make([]*Client, 0, len(h.rooms[room]))
	for c := range h.rooms[room] {
		list = append(list, c)
	}
	h.mu.Unlock()
	for _, c := range list {
		c.writeMu.Lock()
		_ = c.Conn.WriteMessage(websocket.TextMessage, b)
		c.writeMu.Unlock()
	}
}
