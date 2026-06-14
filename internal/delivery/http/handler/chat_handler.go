package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	ws "campusassistant-api/internal/delivery/http/websocket"
	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ChatHandler struct {
	usecase   domain.ChatUseCase
	wsHandler *ws.ChatWSHandler
}

func NewChatHandler(u domain.ChatUseCase, wsh *ws.ChatWSHandler) *ChatHandler {
	return &ChatHandler{usecase: u, wsHandler: wsh}
}

func (h *ChatHandler) GetConversations(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	conversations, err := h.usecase.GetConversations(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"conversations": conversations})
}

func (h *ChatHandler) GetOrCreateConversation(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var body struct {
		OtherUserID string `json:"otherUserId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	otherID, err := uuid.Parse(body.OtherUserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid otherUserId format"})
		return
	}

	conv, err := h.usecase.GetOrCreateConversation(c.Request.Context(), userID, otherID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, conv)
}

func (h *ChatHandler) GetMessages(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	idStr := c.Param("id")
	conversationID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}

	// Parse optional cursor and limit
	var cursor *time.Time
	if cursorStr := c.Query("cursor"); cursorStr != "" {
		t, err := time.Parse(time.RFC3339, cursorStr)
		if err == nil {
			cursor = &t
		}
	}

	limit := 30
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	messages, nextCursor, err := h.usecase.GetMessages(c.Request.Context(), conversationID, userID, cursor, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"messages": messages}
	if nextCursor != nil {
		resp["nextCursor"] = nextCursor.Format(time.RFC3339)
	}
	c.JSON(http.StatusOK, resp)
}

func (h *ChatHandler) SendMessage(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	idStr := c.Param("id")
	conversationID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}

	var body struct {
		Text        string `json:"text" binding:"required"`
		RepliedToID string `json:"repliedToId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var repliedToID *uuid.UUID
	if body.RepliedToID != "" {
		parsed, err := uuid.Parse(body.RepliedToID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repliedToId format"})
			return
		}
		repliedToID = &parsed
	}

	msg, err := h.usecase.SendMessage(c.Request.Context(), conversationID, userID, body.Text, repliedToID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Broadcast via WebSocket
	msgJSON, _ := json.Marshal(msg)
	senderName, _ := c.Get("user_name")
	senderNameStr, _ := senderName.(string)
	h.wsHandler.BroadcastNewMessage(conversationID, userID, msgJSON, senderNameStr)

	c.JSON(http.StatusCreated, msg)
}

func (h *ChatHandler) UpdateMessage(c *gin.Context) {
	msgIDStr := c.Param("messageId")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message ID"})
		return
	}

	var body struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.usecase.EditMessage(c.Request.Context(), msgID, body.Text); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Broadcast edit via WebSocket
	conversationID, err := uuid.Parse(c.Param("id"))
	if err == nil {
		h.wsHandler.BroadcastMessageEdited(conversationID, msgID, body.Text)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Message updated"})
}

func (h *ChatHandler) GetPendingConversations(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	conversations, err := h.usecase.GetPendingConversations(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"conversations": conversations})
}

func (h *ChatHandler) AcceptRequest(c *gin.Context) {
	idStr := c.Param("id")
	conversationID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}

	if err := h.usecase.AcceptRequest(c.Request.Context(), conversationID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Request accepted"})
}

func (h *ChatHandler) BlockRequest(c *gin.Context) {
	idStr := c.Param("id")
	conversationID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}

	if err := h.usecase.BlockRequest(c.Request.Context(), conversationID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Request blocked"})
}

func (h *ChatHandler) DeleteMessage(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	conversationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}
	msgID, err := uuid.Parse(c.Param("messageId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message ID"})
		return
	}

	if err := h.usecase.DeleteMessage(c.Request.Context(), msgID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.wsHandler.BroadcastMessageDeleted(conversationID, msgID, userID)
	c.JSON(http.StatusOK, gin.H{"message": "Message deleted"})
}

func (h *ChatHandler) ArchiveConversation(c *gin.Context) {
	conversationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}

	if err := h.usecase.ArchiveConversation(c.Request.Context(), conversationID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Conversation archived"})
}

func (h *ChatHandler) DeleteConversation(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	conversationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}

	if err := h.usecase.DeleteConversation(c.Request.Context(), conversationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Conversation deleted"})
}

func (h *ChatHandler) GetContacts(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	departmentIDVal := c.MustGet("department_id")
	var departmentID uuid.UUID
	switch v := departmentIDVal.(type) {
	case uuid.UUID:
		departmentID = v
	case *uuid.UUID:
		if v == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "user must belong to a department"})
			return
		}
		departmentID = *v
	default:
		c.JSON(http.StatusForbidden, gin.H{"error": "user must belong to a department"})
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	offset := 0
	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}

	search := c.Query("search")

	contacts, total, err := h.usecase.GetContacts(c.Request.Context(), departmentID, userID, limit, offset, search)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":   contacts,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *ChatHandler) MarkAsRead(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	idStr := c.Param("id")
	conversationID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid conversation ID"})
		return
	}

	if err := h.usecase.MarkAsRead(c.Request.Context(), conversationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Marked as read"})
}
