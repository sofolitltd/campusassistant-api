package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Conversation struct {
	Base
	LastMessage         string    `json:"lastMessage"`
	LastMessageTime     time.Time `json:"lastMessageTime"`
	LastMessageSenderID uuid.UUID `gorm:"type:uuid" json:"lastMessageSender"`
	Status              string    `gorm:"default:accepted" json:"status"`
	InitiatorID         uuid.UUID `gorm:"type:uuid" json:"initiatorId"`

	Participants []ConversationParticipant `gorm:"foreignKey:ConversationID;constraint:OnDelete:CASCADE" json:"participants,omitempty"`

	// Computed fields
	ParticipantData map[string]ParticipantInfo `gorm:"-" json:"participantData"`
	UnreadCount     int                       `gorm:"-" json:"unreadCount"`
}

const (
	ConversationStatusPending  = "pending"
	ConversationStatusAccepted = "accepted"
	ConversationStatusBlocked  = "blocked"
	ConversationStatusArchived = "archived"
)

type ParticipantInfo struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
}

type ConversationParticipant struct {
	ConversationID uuid.UUID `gorm:"type:uuid;primaryKey;constraint:OnDelete:CASCADE"`
	UserID         uuid.UUID `gorm:"type:uuid;primaryKey;constraint:OnDelete:CASCADE"`
	LastReadAt     time.Time `json:"last_read_at"`

	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

type Message struct {
	Base
	ConversationID uuid.UUID `gorm:"type:uuid;index;not null;constraint:OnDelete:CASCADE" json:"conversationId"`
	SenderID       uuid.UUID `gorm:"type:uuid;not null" json:"senderId"`
	Text           string    `gorm:"type:text;not null" json:"text"`
	Read           bool      `gorm:"default:false;index" json:"read"`

	RepliedToID  *uuid.UUID `gorm:"type:uuid" json:"repliedToId"`
	RepliedToText string    `gorm:"-" json:"repliedToText"`

	Sender *User `gorm:"foreignKey:SenderID" json:"sender,omitempty"`

	// Computed field for Flutter compatibility
	Timestamp time.Time `gorm:"-" json:"timestamp"`
}

// Contact represents a lightweight user for the new-chat picker.
type Contact struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
}

// UserDeletedMessage tracks messages a user has chosen to hide ("delete for me").
// This follows the WhatsApp/Messenger pattern: the message row itself is never
// modified; per-user visibility is managed through this separate table.
type UserDeletedMessage struct {
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey;constraint:OnDelete:CASCADE" json:"userId"`
	MessageID uuid.UUID `gorm:"type:uuid;primaryKey;constraint:OnDelete:CASCADE" json:"messageId"`
	CreatedAt time.Time `json:"created_at"`
}

type ChatRepository interface {
	GetConversations(ctx context.Context, userID uuid.UUID) ([]Conversation, error)
	GetPendingConversations(ctx context.Context, userID uuid.UUID) ([]Conversation, error)
	GetOrCreateConversation(ctx context.Context, userID, otherUserID uuid.UUID) (*Conversation, error)
	GetMessages(ctx context.Context, conversationID, userID uuid.UUID, cursor *time.Time, limit int) ([]Message, *time.Time, error)
	SendMessage(ctx context.Context, message *Message) error
	UpdateMessage(ctx context.Context, messageID uuid.UUID, text string) error
	MarkAsRead(ctx context.Context, conversationID, userID uuid.UUID) error
	AcceptRequest(ctx context.Context, conversationID uuid.UUID) error
	BlockRequest(ctx context.Context, conversationID uuid.UUID) error
	DeleteMessage(ctx context.Context, messageID, userID uuid.UUID) error
	DeleteConversation(ctx context.Context, conversationID, userID uuid.UUID) error
	ArchiveConversation(ctx context.Context, conversationID uuid.UUID) error
	CleanupDeletedMessages(ctx context.Context, before time.Time) (int64, error)
	GetContacts(ctx context.Context, departmentID uuid.UUID, userID uuid.UUID, limit, offset int, search string) ([]Contact, int64, error)
}

type ChatUseCase interface {
	GetConversations(ctx context.Context, userID uuid.UUID) ([]Conversation, error)
	GetPendingConversations(ctx context.Context, userID uuid.UUID) ([]Conversation, error)
	GetOrCreateConversation(ctx context.Context, userID, otherUserID uuid.UUID) (*Conversation, error)
	GetMessages(ctx context.Context, conversationID, userID uuid.UUID, cursor *time.Time, limit int) ([]Message, *time.Time, error)
	SendMessage(ctx context.Context, conversationID, senderID uuid.UUID, text string, repliedToID *uuid.UUID) (*Message, error)
	EditMessage(ctx context.Context, messageID uuid.UUID, text string) error
	MarkAsRead(ctx context.Context, conversationID, userID uuid.UUID) error
	AcceptRequest(ctx context.Context, conversationID uuid.UUID) error
	BlockRequest(ctx context.Context, conversationID uuid.UUID) error
	DeleteMessage(ctx context.Context, messageID, userID uuid.UUID) error
	DeleteConversation(ctx context.Context, conversationID, userID uuid.UUID) error
	ArchiveConversation(ctx context.Context, conversationID uuid.UUID) error
	GetContacts(ctx context.Context, departmentID uuid.UUID, userID uuid.UUID, limit, offset int, search string) ([]Contact, int64, error)
}
