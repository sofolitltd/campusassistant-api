package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type NoticeEngagementHandler struct {
	db *gorm.DB
}

func NewNoticeEngagementHandler(db *gorm.DB) *NoticeEngagementHandler {
	return &NoticeEngagementHandler{db: db}
}

func (h *NoticeEngagementHandler) LikeNotice(c *gin.Context) {
	noticeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notice ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	err = h.db.Transaction(func(tx *gorm.DB) error {
		like := domain.NoticeLike{NoticeID: noticeID, UserID: userID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&like)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // already liked
		}
		return tx.Model(&domain.Notice{}).Where("id = ?", noticeID).
			UpdateColumn("likes_count", gorm.Expr("likes_count + ?", 1)).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to like notice"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Liked"})
}

func (h *NoticeEngagementHandler) UnlikeNotice(c *gin.Context) {
	noticeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notice ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	err = h.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&domain.NoticeLike{}, "notice_id = ? AND user_id = ?", noticeID, userID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // wasn't liked
		}
		return tx.Model(&domain.Notice{}).Where("id = ?", noticeID).
			UpdateColumn("likes_count", gorm.Expr("GREATEST(likes_count - 1, 0)")).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unlike notice"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Unliked"})
}

// GetLikedNoticeIDs returns the IDs of notices the requesting user has liked,
// scoped to a department, so the client can merge "is liked" state into a
// notice list fetched from the public generic /notices endpoint.
func (h *NoticeEngagementHandler) GetLikedNoticeIDs(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	departmentID := c.Query("department_id")

	query := h.db.Model(&domain.NoticeLike{}).Where("user_id = ?", userID)
	if departmentID != "" {
		query = query.Joins("JOIN notices ON notices.id = notice_likes.notice_id").
			Where("notices.department_id = ?", departmentID)
	}

	var ids []uuid.UUID
	if err := query.Pluck("notice_likes.notice_id", &ids).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch liked notices"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ids})
}

func (h *NoticeEngagementHandler) ViewNotice(c *gin.Context) {
	noticeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notice ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	err = h.db.Transaction(func(tx *gorm.DB) error {
		read := domain.NoticeRead{NoticeID: noticeID, UserID: userID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&read)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // already read
		}
		return tx.Model(&domain.Notice{}).Where("id = ?", noticeID).
			UpdateColumn("views_count", gorm.Expr("views_count + ?", 1)).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record view"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Recorded"})
}

func (h *NoticeEngagementHandler) GetComments(c *gin.Context) {
	noticeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notice ID"})
		return
	}

	var comments []domain.NoticeComment
	if err := h.db.Where("notice_id = ?", noticeID).Order("created_at asc").
		Preload("Author").Find(&comments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch comments"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": comments})
}

func (h *NoticeEngagementHandler) AddComment(c *gin.Context) {
	noticeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notice ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	comment := domain.NoticeComment{NoticeID: noticeID, AuthorID: userID, Content: req.Content}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&comment).Error; err != nil {
			return err
		}
		return tx.Model(&domain.Notice{}).Where("id = ?", noticeID).
			UpdateColumn("comments_count", gorm.Expr("comments_count + ?", 1)).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add comment"})
		return
	}

	h.db.Preload("Author").First(&comment, "id = ?", comment.ID)
	c.JSON(http.StatusCreated, comment)
}

func (h *NoticeEngagementHandler) DeleteComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	var comment domain.NoticeComment
	if err := h.db.First(&comment, "id = ?", commentID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}
	if comment.AuthorID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only delete your own comments"})
		return
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&comment).Error; err != nil {
			return err
		}
		return tx.Model(&domain.Notice{}).Where("id = ?", comment.NoticeID).
			UpdateColumn("comments_count", gorm.Expr("GREATEST(comments_count - 1, 0)")).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete comment"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}
