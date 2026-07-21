package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var notificationUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

const (
	notificationWriteWait      = 10 * time.Second
	notificationPongWait       = 60 * time.Second
	notificationPingPeriod     = (notificationPongWait * 9) / 10
	notificationMaxMessageSize = 4096
)

type NotificationWSHandler struct {
	hub *NotificationHub
}

func NewNotificationWSHandler(hub *NotificationHub) *NotificationWSHandler {
	return &NotificationWSHandler{hub: hub}
}

func (h *NotificationWSHandler) ServeWS(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	conn, err := notificationUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[notif WS] upgrade error: %v", err)
		return
	}

	client := &NotificationClient{
		UserID:        userID,
		Conn:          conn,
		Send:          make(chan []byte, 256),
		Subscriptions: make(map[string]bool),
	}

	h.hub.Register(client)

	go h.writePump(client)
	go h.readPump(client)
}

func (h *NotificationWSHandler) writePump(client *NotificationClient) {
	ticker := time.NewTicker(notificationPingPeriod)
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
			client.Conn.SetWriteDeadline(time.Now().Add(notificationWriteWait))
			if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(notificationWriteWait))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

type subscribeAction struct {
	Action  string `json:"action"`
	Channel string `json:"channel"`
}

func (h *NotificationWSHandler) readPump(client *NotificationClient) {
	defer func() {
		h.hub.Unregister(client)
		client.Conn.Close()
	}()

	client.Conn.SetReadLimit(notificationMaxMessageSize)
	client.Conn.SetReadDeadline(time.Now().Add(notificationPongWait))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(notificationPongWait))
		return nil
	})

	for {
		_, raw, err := client.Conn.ReadMessage()
		if err != nil {
			break
		}

		var action subscribeAction
		if err := json.Unmarshal(raw, &action); err != nil {
			continue
		}

		switch action.Action {
		case "subscribe":
			if action.Channel != "" {
				h.hub.Subscribe(client, action.Channel)
			}
		case "unsubscribe":
			if action.Channel != "" {
				h.hub.Unsubscribe(client, action.Channel)
			}
		}
	}
}
