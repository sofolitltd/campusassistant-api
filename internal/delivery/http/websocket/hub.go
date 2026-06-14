package websocket

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type MessageEvent struct {
	Type           string          `json:"type"`
	ConversationID string          `json:"conversationId,omitempty"`
	UserID         string          `json:"userId,omitempty"`
	Message        json.RawMessage `json:"message,omitempty"`
	MessageID      string          `json:"messageId,omitempty"`
	Text           string          `json:"text,omitempty"`
	IsTyping       bool            `json:"isTyping,omitempty"`
	SenderName     string          `json:"senderName,omitempty"`
}

type Client struct {
	UserID         uuid.UUID
	ConversationID string
	Conn           *websocket.Conn
	Send           chan []byte
}

type Hub struct {
	mu      sync.RWMutex
	rooms   map[string]map[*Client]bool
	clients map[*Client]bool
}

func NewHub() *Hub {
	return &Hub{
		rooms:   make(map[string]map[*Client]bool),
		clients: make(map[*Client]bool),
	}
}

func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client] = true

	// Join conversation room
	if _, ok := h.rooms[client.ConversationID]; !ok {
		h.rooms[client.ConversationID] = make(map[*Client]bool)
	}
	h.rooms[client.ConversationID][client] = true
}

func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.rooms[client.ConversationID]; ok {
		delete(h.rooms[client.ConversationID], client)
		if len(h.rooms[client.ConversationID]) == 0 {
			delete(h.rooms, client.ConversationID)
		}
	}
	delete(h.clients, client)
	close(client.Send)
}

func (h *Hub) BroadcastToConversation(conversationID string, event []byte, sender *Client) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, ok := h.rooms[conversationID]; ok {
		for client := range clients {
			if client != sender {
				select {
				case client.Send <- event:
				default:
				}
			}
		}
	}
}

func (h *Hub) BroadcastToConversationAll(conversationID string, event []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, ok := h.rooms[conversationID]; ok {
		for client := range clients {
			select {
			case client.Send <- event:
			default:
			}
		}
	}
}
