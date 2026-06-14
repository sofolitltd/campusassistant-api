package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
)

type ChatWSHandler struct {
	hub     *Hub
	usecase domain.ChatUseCase
}

func NewChatWSHandler(hub *Hub, uc domain.ChatUseCase) *ChatWSHandler {
	return &ChatWSHandler{hub: hub, usecase: uc}
}

func (h *ChatWSHandler) ServeWS(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	conversationID := c.Param("id")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &Client{
		UserID:         userID,
		ConversationID: conversationID,
		Conn:           conn,
		Send:           make(chan []byte, 256),
	}

	h.hub.Register(client)

	go h.writePump(client)
	go h.readPump(client)
}

func (h *ChatWSHandler) writePump(client *Client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			if !ok {
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *ChatWSHandler) readPump(client *Client) {
	defer func() {
		h.hub.Unregister(client)
		client.Conn.Close()
	}()

	client.Conn.SetReadLimit(maxMessageSize)
	client.Conn.SetReadDeadline(time.Now().Add(pongWait))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, raw, err := client.Conn.ReadMessage()
		if err != nil {
			break
		}

		var event MessageEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			continue
		}

		switch event.Type {
		case "typing":
			event.UserID = client.UserID.String()
			data, _ := json.Marshal(event)
			h.hub.BroadcastToConversation(client.ConversationID, data, client)

		case "mark_read":
			event.UserID = client.UserID.String()
			data, _ := json.Marshal(event)
			h.hub.BroadcastToConversation(client.ConversationID, data, client)

		case "delivered":
			event.Type = "message_delivered"
			event.UserID = client.UserID.String()
			data, _ := json.Marshal(event)
			h.hub.BroadcastToConversation(client.ConversationID, data, client)
		}
	}
}

// BroadcastNewMessage sends a new message event to all clients in a conversation
func (h *ChatWSHandler) BroadcastNewMessage(conversationID uuid.UUID, userID uuid.UUID, message json.RawMessage, senderName string) {
	event := MessageEvent{
		Type:           "new_message",
		ConversationID: conversationID.String(),
		UserID:         userID.String(),
		Message:        message,
		SenderName:     senderName,
	}
	data, _ := json.Marshal(event)
	h.hub.BroadcastToConversationAll(conversationID.String(), data)
}

// BroadcastMessageEdited sends a message edited event
func (h *ChatWSHandler) BroadcastMessageEdited(conversationID uuid.UUID, messageID uuid.UUID, text string) {
	event := MessageEvent{
		Type:           "message_edited",
		ConversationID: conversationID.String(),
		MessageID:      messageID.String(),
		Text:           text,
	}
	data, _ := json.Marshal(event)
	h.hub.BroadcastToConversationAll(conversationID.String(), data)
}

// BroadcastMessageDeleted sends a message deleted event
func (h *ChatWSHandler) BroadcastMessageDeleted(conversationID uuid.UUID, messageID uuid.UUID, userID uuid.UUID) {
	event := MessageEvent{
		Type:           "message_deleted",
		ConversationID: conversationID.String(),
		MessageID:      messageID.String(),
		UserID:         userID.String(),
	}
	data, _ := json.Marshal(event)
	h.hub.BroadcastToConversationAll(conversationID.String(), data)
}
