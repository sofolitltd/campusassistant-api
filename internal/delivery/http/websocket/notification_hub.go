package websocket

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type NotificationMessage struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type NotificationClient struct {
	UserID         uuid.UUID
	Conn           *websocket.Conn
	Send           chan []byte
	Subscriptions  map[string]bool
	mu             sync.Mutex
}

type NotificationHub struct {
	mu       sync.RWMutex
	clients  map[*NotificationClient]bool
	channels map[string]map[*NotificationClient]bool
}

func NewNotificationHub() *NotificationHub {
	return &NotificationHub{
		clients:  make(map[*NotificationClient]bool),
		channels: make(map[string]map[*NotificationClient]bool),
	}
}

func (h *NotificationHub) Register(client *NotificationClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client] = true
}

func (h *NotificationHub) Unregister(client *NotificationClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for channel := range client.Subscriptions {
		if clients, ok := h.channels[channel]; ok {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.channels, channel)
			}
		}
	}
	delete(h.clients, client)
	close(client.Send)
}

func (h *NotificationHub) Subscribe(client *NotificationClient, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	client.mu.Lock()
	client.Subscriptions[channel] = true
	client.mu.Unlock()

	if _, ok := h.channels[channel]; !ok {
		h.channels[channel] = make(map[*NotificationClient]bool)
	}
	h.channels[channel][client] = true
}

func (h *NotificationHub) Unsubscribe(client *NotificationClient, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	client.mu.Lock()
	delete(client.Subscriptions, channel)
	client.mu.Unlock()

	if clients, ok := h.channels[channel]; ok {
		delete(clients, client)
		if len(clients) == 0 {
			delete(h.channels, channel)
		}
	}
}

func (h *NotificationHub) BroadcastToChannel(channel string, event []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, ok := h.channels[channel]; ok {
		for client := range clients {
			select {
			case client.Send <- event:
			default:
			}
		}
	}
}

func (h *NotificationHub) SendToUser(userID uuid.UUID, event []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if client.UserID == userID {
			select {
			case client.Send <- event:
			default:
			}
		}
	}
}
