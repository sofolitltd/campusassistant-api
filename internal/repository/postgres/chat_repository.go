package postgres

import (
	"context"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type chatRepository struct {
	db *gorm.DB
}

func NewChatRepository(db *gorm.DB) domain.ChatRepository {
	return &chatRepository{db: db}
}

func (r *chatRepository) GetConversations(ctx context.Context, userID uuid.UUID) ([]domain.Conversation, error) {
	var conversations []domain.Conversation

	err := r.db.WithContext(ctx).
		Joins("JOIN conversation_participants ON conversation_participants.conversation_id = conversations.id").
		Where("conversation_participants.user_id = ?", userID).
		Where("conversations.status NOT IN (?)", []string{domain.ConversationStatusBlocked, domain.ConversationStatusArchived}).
		Preload("Participants.User").
		Order("conversations.last_message_time DESC").
		Find(&conversations).Error
	if err != nil {
		return nil, err
	}

	for i := range conversations {
		r.enrichConversation(ctx, &conversations[i], userID)
	}

	return conversations, nil
}

func (r *chatRepository) GetPendingConversations(ctx context.Context, userID uuid.UUID) ([]domain.Conversation, error) {
	var conversations []domain.Conversation

	err := r.db.WithContext(ctx).
		Where("status = ? AND initiator_id != ?", domain.ConversationStatusPending, userID).
		Joins("JOIN conversation_participants ON conversation_participants.conversation_id = conversations.id").
		Where("conversation_participants.user_id = ?", userID).
		Preload("Participants.User").
		Order("conversations.created_at DESC").
		Find(&conversations).Error
	if err != nil {
		return nil, err
	}

	for i := range conversations {
		r.enrichConversation(ctx, &conversations[i], userID)
	}

	return conversations, nil
}

func (r *chatRepository) GetOrCreateConversation(ctx context.Context, userID, otherUserID uuid.UUID) (*domain.Conversation, error) {
	var existing domain.Conversation

	// Look for existing conversation where both users are participants
	err := r.db.WithContext(ctx).
		Raw(`
			SELECT c.* FROM conversations c
			WHERE c.deleted_at IS NULL
			AND EXISTS (
				SELECT 1 FROM conversation_participants cp1
				WHERE cp1.conversation_id = c.id AND cp1.user_id = ?
			)
			AND EXISTS (
				SELECT 1 FROM conversation_participants cp2
				WHERE cp2.conversation_id = c.id AND cp2.user_id = ?
			)
		`, userID, otherUserID).
		First(&existing).Error

	if err == nil {
		r.db.WithContext(ctx).Preload("Participants.User").First(&existing, "id = ?", existing.ID)
		r.enrichConversation(ctx, &existing, userID)
		return &existing, nil
	}

	// Check for soft-deleted conversation
	var deleted domain.Conversation
	err = r.db.WithContext(ctx).Unscoped().
		Raw(`
			SELECT c.* FROM conversations c
			WHERE c.deleted_at IS NOT NULL
			AND EXISTS (
				SELECT 1 FROM conversation_participants cp1
				WHERE cp1.conversation_id = c.id AND cp1.user_id = ?
			)
			AND EXISTS (
				SELECT 1 FROM conversation_participants cp2
				WHERE cp2.conversation_id = c.id AND cp2.user_id = ?
			)
		`, userID, otherUserID).
		First(&deleted).Error
	if err == nil {
		// Restore the soft-deleted conversation
		r.db.WithContext(ctx).Unscoped().Model(&deleted).Updates(map[string]interface{}{
			"deleted_at": nil,
			"status":     domain.ConversationStatusAccepted,
		})
		// Messages remain untouched: individually deleted messages stay in
		// user_deleted_messages (WhatsApp model), and cascade-deleted messages
		// are not stored as deleted_at on the row — they're invisible only
		// because the conversation was soft-deleted.
		r.db.WithContext(ctx).Preload("Participants.User").First(&existing, "id = ?", deleted.ID)
		r.enrichConversation(ctx, &existing, userID)
		return &existing, nil
	}

	// Create new conversation as pending request
	conv := domain.Conversation{
		LastMessageTime: time.Now(),
		Status:          domain.ConversationStatusPending,
		InitiatorID:     userID,
	}
	conv.SetCreatedBy(userID)

	if err := r.db.WithContext(ctx).Create(&conv).Error; err != nil {
		return nil, err
	}

	// Add participants
	participants := []domain.ConversationParticipant{
		{ConversationID: conv.ID, UserID: userID},
		{ConversationID: conv.ID, UserID: otherUserID},
	}
	if err := r.db.WithContext(ctx).Create(&participants).Error; err != nil {
		return nil, err
	}

	// Re-fetch with participants preloaded so enrichConversation has data
	if err := r.db.WithContext(ctx).
		Preload("Participants.User").
		First(&conv, "id = ?", conv.ID).Error; err != nil {
		return nil, err
	}

	r.enrichConversation(ctx, &conv, userID)
	return &conv, nil
}

func (r *chatRepository) GetMessages(ctx context.Context, conversationID uuid.UUID, userID uuid.UUID, cursor *time.Time, limit int) ([]domain.Message, *time.Time, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	query := r.db.WithContext(ctx).
		Where("conversation_id = ? AND id NOT IN (SELECT message_id FROM user_deleted_messages WHERE user_id = ?)", conversationID, userID).
		Order("created_at DESC").
		Limit(limit + 1)

	if cursor != nil {
		query = query.Where("created_at < ?", *cursor)
	}

	var messages []domain.Message
	if err := query.Find(&messages).Error; err != nil {
		return nil, nil, err
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	// Reverse to ASC order for the frontend
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	// Return cursor for next page (oldest message's CreatedAt)
	var nextCursor *time.Time
	if hasMore && len(messages) > 0 {
		nextCursor = &messages[0].CreatedAt
	}

	for i := range messages {
		messages[i].Timestamp = messages[i].CreatedAt
		if messages[i].RepliedToID != nil {
			var replied domain.Message
			if err := r.db.WithContext(ctx).
				Select("text").
				Where("id = ?", *messages[i].RepliedToID).
				First(&replied).Error; err == nil {
				messages[i].RepliedToText = replied.Text
			}
		}
	}
	return messages, nextCursor, nil
}

func (r *chatRepository) SendMessage(ctx context.Context, message *domain.Message) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(message).Error; err != nil {
			return err
		}

		// Update conversation's last message info
		return tx.Model(&domain.Conversation{}).
			Where("id = ?", message.ConversationID).
			Updates(map[string]interface{}{
				"last_message":           message.Text,
				"last_message_time":      message.CreatedAt,
				"last_message_sender_id": message.SenderID,
			}).Error
	})
}

func (r *chatRepository) UpdateMessage(ctx context.Context, messageID uuid.UUID, text string) error {
	var msg domain.Message
	if err := r.db.WithContext(ctx).Select("conversation_id").First(&msg, "id = ?", messageID).Error; err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Message{}).
			Where("id = ?", messageID).
			Updates(map[string]interface{}{
				"text":       text,
				"updated_at": time.Now(),
			}).Error; err != nil {
			return err
		}

		// If this message is the most recent one, update the conversation preview
		var lastMsg domain.Message
		if err := tx.Where("conversation_id = ?", msg.ConversationID).
			Order("created_at DESC").
			First(&lastMsg).Error; err != nil {
			return nil
		}
		if lastMsg.ID == messageID {
			return tx.Model(&domain.Conversation{}).
				Where("id = ?", msg.ConversationID).
				Update("last_message", text).Error
		}
		return nil
	})
}

func (r *chatRepository) MarkAsRead(ctx context.Context, conversationID, userID uuid.UUID) error {
	// Mark all messages as read that were sent by other users
	return r.db.WithContext(ctx).
		Model(&domain.Message{}).
		Where("conversation_id = ? AND sender_id != ? AND read = ?", conversationID, userID, false).
		Update("read", true).Error
}

func (r *chatRepository) AcceptRequest(ctx context.Context, conversationID uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.Conversation{}).
		Where("id = ?", conversationID).
		Update("status", domain.ConversationStatusAccepted).Error
}

func (r *chatRepository) BlockRequest(ctx context.Context, conversationID uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.Conversation{}).
		Where("id = ?", conversationID).
		Update("status", domain.ConversationStatusBlocked).Error
}

func (r *chatRepository) DeleteMessage(ctx context.Context, messageID uuid.UUID, userID uuid.UUID) error {
	var msg domain.Message
	if err := r.db.WithContext(ctx).Select("conversation_id").First(&msg, "id = ?", messageID).Error; err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Record user-level deletion (WhatsApp model: message row is never modified)
		// Use OnConflict to make this idempotent — duplicate deletes are silently ignored.
		tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&domain.UserDeletedMessage{
			UserID:    userID,
			MessageID: messageID,
		})

		// Recompute last message only if the deleting user was the sender
		if msg.SenderID == userID {
			var lastMsg domain.Message
			err := tx.Where("conversation_id = ? AND id NOT IN (SELECT message_id FROM user_deleted_messages WHERE user_id = ?)", msg.ConversationID, userID).
				Order("created_at DESC").
				First(&lastMsg).Error

			updates := map[string]interface{}{
				"last_message_time": time.Now(),
			}
			if err == nil {
				updates["last_message"] = lastMsg.Text
				updates["last_message_sender_id"] = lastMsg.SenderID
			} else {
				updates["last_message"] = ""
				updates["last_message_sender_id"] = nil
			}

			return tx.Model(&domain.Conversation{}).
				Where("id = ?", msg.ConversationID).
				Updates(updates).Error
		}

		return nil
	})
}

func (r *chatRepository) ArchiveConversation(ctx context.Context, conversationID uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.Conversation{}).
		Where("id = ?", conversationID).
		Update("status", domain.ConversationStatusArchived).Error
}

func (r *chatRepository) DeleteConversation(ctx context.Context, conversationID uuid.UUID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Bulk-insert all message IDs for this conversation into user_deleted_messages
		// Single query instead of per-message loop — O(1) not O(n)
		tx.Exec(
			`INSERT INTO user_deleted_messages (user_id, message_id, created_at)
			 SELECT ?, id, NOW() FROM messages WHERE conversation_id = ?
			 ON CONFLICT DO NOTHING`,
			userID, conversationID,
		)

		return tx.Delete(&domain.Conversation{}, "id = ?", conversationID).Error
	})
}

func (r *chatRepository) GetContacts(ctx context.Context, departmentID uuid.UUID, userID uuid.UUID, limit, offset int, search string) ([]domain.Contact, int64, error) {
	var total int64

	baseQuery := r.db.WithContext(ctx).
		Model(&domain.User{}).
		Joins("JOIN students ON students.user_id = users.id").
		Where("students.department_id = ?", departmentID).
		Where("users.id != ?", userID)

	if err := baseQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query := baseQuery.
		Select("users.id, users.first_name, users.last_name, users.avatar_url").
		Order("users.first_name ASC").
		Limit(limit).Offset(offset)

	if search != "" {
		query = query.Where("(users.first_name || ' ' || users.last_name) ILIKE ?", "%"+search+"%")
	}

	var users []domain.User
	if err := query.Find(&users).Error; err != nil {
		return nil, 0, err
	}

	contacts := make([]domain.Contact, len(users))
	for i, u := range users {
		contacts[i] = domain.Contact{
			UserID:    u.ID.String(),
			Name:      u.FullName(),
			AvatarURL: u.AvatarURL,
		}
	}

	return contacts, total, nil
}

// CleanupDeletedMessages hard-deletes user_deleted_message records older than the given cutoff.
// Also hard-deletes message rows for soft-deleted conversations older than the cutoff.
func (r *chatRepository) CleanupDeletedMessages(ctx context.Context, before time.Time) (int64, error) {
	var total int64

	// Clean up old user_deleted_messages
	result := r.db.WithContext(ctx).
		Exec("DELETE FROM user_deleted_messages WHERE created_at < ?", before)
	if result.Error != nil {
		return 0, result.Error
	}
	total += result.RowsAffected

	// Clean up orphaned user_deleted_messages (whose message no longer exists)
	result = r.db.WithContext(ctx).
		Exec(`DELETE FROM user_deleted_messages WHERE message_id NOT IN (SELECT id FROM messages)`)
	if result.Error != nil {
		return 0, result.Error
	}
	total += result.RowsAffected

	// Hard-delete conversations that were soft-deleted before the cutoff
	var convIDs []uuid.UUID
	r.db.WithContext(ctx).Unscoped().
		Where("deleted_at IS NOT NULL AND deleted_at < ?", before).
		Pluck("id", &convIDs)

	if len(convIDs) > 0 {
		// Clean up user_deleted_messages for these conversations
		r.db.WithContext(ctx).
			Where("message_id IN (SELECT id FROM messages WHERE conversation_id IN (?))", convIDs).
			Delete(&domain.UserDeletedMessage{})

		// Hard-delete the messages
		r.db.WithContext(ctx).
			Where("conversation_id IN (?)", convIDs).
			Delete(&domain.Message{})

		// Hard-delete the conversations
		result = r.db.WithContext(ctx).Unscoped().
			Where("id IN (?)", convIDs).
			Delete(&domain.Conversation{})
		total += result.RowsAffected
	}

	return total, nil
}

func (r *chatRepository) enrichConversation(ctx context.Context, conv *domain.Conversation, currentUserID uuid.UUID) {
	// Build participant data map
	conv.ParticipantData = make(map[string]domain.ParticipantInfo)
	for _, p := range conv.Participants {
		if p.User != nil {
			conv.ParticipantData[p.UserID.String()] = domain.ParticipantInfo{
				Name:  p.User.FullName(),
				Image: p.User.AvatarURL,
			}
		}
	}

	// Count unread messages (excluding ones the user deleted)
	var unreadCount int64
	r.db.WithContext(ctx).Model(&domain.Message{}).
		Where("conversation_id = ? AND sender_id != ? AND read = ? AND id NOT IN (SELECT message_id FROM user_deleted_messages WHERE user_id = ?)", conv.ID, currentUserID, false, currentUserID).
		Count(&unreadCount)
	conv.UnreadCount = int(unreadCount)
}
