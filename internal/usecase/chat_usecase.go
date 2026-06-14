package usecase

import (
	"context"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

type chatUseCase struct {
	repo domain.ChatRepository
}

func NewChatUseCase(repo domain.ChatRepository) domain.ChatUseCase {
	return &chatUseCase{repo: repo}
}

func (u *chatUseCase) GetConversations(ctx context.Context, userID uuid.UUID) ([]domain.Conversation, error) {
	return u.repo.GetConversations(ctx, userID)
}

func (u *chatUseCase) GetPendingConversations(ctx context.Context, userID uuid.UUID) ([]domain.Conversation, error) {
	return u.repo.GetPendingConversations(ctx, userID)
}

func (u *chatUseCase) AcceptRequest(ctx context.Context, conversationID uuid.UUID) error {
	return u.repo.AcceptRequest(ctx, conversationID)
}

func (u *chatUseCase) BlockRequest(ctx context.Context, conversationID uuid.UUID) error {
	return u.repo.BlockRequest(ctx, conversationID)
}

func (u *chatUseCase) GetOrCreateConversation(ctx context.Context, userID, otherUserID uuid.UUID) (*domain.Conversation, error) {
	return u.repo.GetOrCreateConversation(ctx, userID, otherUserID)
}

func (u *chatUseCase) GetMessages(ctx context.Context, conversationID, userID uuid.UUID, cursor *time.Time, limit int) ([]domain.Message, *time.Time, error) {
	return u.repo.GetMessages(ctx, conversationID, userID, cursor, limit)
}

func (u *chatUseCase) SendMessage(ctx context.Context, conversationID, senderID uuid.UUID, text string, repliedToID *uuid.UUID) (*domain.Message, error) {
	msg := &domain.Message{
		ConversationID: conversationID,
		SenderID:       senderID,
		Text:           text,
		RepliedToID:    repliedToID,
	}
	msg.SetCreatedBy(senderID)

	if err := u.repo.SendMessage(ctx, msg); err != nil {
		return nil, err
	}

	msg.Timestamp = msg.CreatedAt
	return msg, nil
}

func (u *chatUseCase) EditMessage(ctx context.Context, messageID uuid.UUID, text string) error {
	return u.repo.UpdateMessage(ctx, messageID, text)
}

func (u *chatUseCase) DeleteMessage(ctx context.Context, messageID, userID uuid.UUID) error {
	return u.repo.DeleteMessage(ctx, messageID, userID)
}

func (u *chatUseCase) DeleteConversation(ctx context.Context, conversationID, userID uuid.UUID) error {
	return u.repo.DeleteConversation(ctx, conversationID, userID)
}

func (u *chatUseCase) ArchiveConversation(ctx context.Context, conversationID uuid.UUID) error {
	return u.repo.ArchiveConversation(ctx, conversationID)
}

func (u *chatUseCase) GetContacts(ctx context.Context, departmentID uuid.UUID, userID uuid.UUID, limit, offset int, search string) ([]domain.Contact, int64, error) {
	return u.repo.GetContacts(ctx, departmentID, userID, limit, offset, search)
}

func (u *chatUseCase) MarkAsRead(ctx context.Context, conversationID, userID uuid.UUID) error {
	return u.repo.MarkAsRead(ctx, conversationID, userID)
}
