package handler

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CommunityHandler struct {
	usecase domain.CommunityUseCase
	storage *storage.R2Storage
	db      *gorm.DB
}

func NewCommunityHandler(u domain.CommunityUseCase, s *storage.R2Storage, db *gorm.DB) *CommunityHandler {
	return &CommunityHandler{usecase: u, storage: s, db: db}
}

func (h *CommunityHandler) CreatePost(c *gin.Context) {
	// Support multipart/form-data (with optional images) or plain JSON.
	content := c.PostForm("content")
	scopeStr := c.PostForm("scope")
	if content == "" || scopeStr == "" {
		// Fallback to JSON body
		var body struct {
			Content string           `json:"content"`
			Scope   domain.PostScope `json:"scope"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Content == "" || body.Scope == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "content and scope are required"})
			return
		}
		content = body.Content
		scopeStr = string(body.Scope)
	}

	scope := domain.NormalizeScope(scopeStr)

	var imageURLs []string
	if h.storage != nil {
		form, err := c.MultipartForm()
		if err == nil && form != nil {
			files := form.File["images"]
			for _, file := range files {
				now := time.Now()
				uniqueID := uuid.New().String()
				ext := filepath.Ext(file.Filename)
				path := fmt.Sprintf("community/%d/%02d/%s%s", now.Year(), now.Month(), uniqueID, ext)

				fileURL, upErr := h.storage.UploadFile(c.Request.Context(), file, path)
				if upErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to upload image: %v", upErr)})
					return
				}
				imageURLs = append(imageURLs, fileURL)
			}
		}
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	post, err := h.usecase.CreatePost(c.Request.Context(), userID, content, scope, imageURLs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, post)
}

func (h *CommunityHandler) GetPosts(c *gin.Context) {
	scope := c.Query("scope")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))

	userID := c.MustGet("user_id").(uuid.UUID)
	viewer, err := h.resolveViewer(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	posts, err := h.usecase.GetPosts(c.Request.Context(), userID, viewer, domain.NormalizeScope(scope), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, posts)
}

func (h *CommunityHandler) GetLikedPosts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))

	userID := c.MustGet("user_id").(uuid.UUID)

	posts, err := h.usecase.GetLikedPosts(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, posts)
}

func (h *CommunityHandler) GetSavedPosts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))

	userID := c.MustGet("user_id").(uuid.UUID)

	posts, err := h.usecase.GetSavedPosts(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, posts)
}

// resolveViewer builds the requesting user's organizational context from their
// Student record (the same source posts use), so filters match reliably.
func (h *CommunityHandler) resolveViewer(ctx context.Context, userID uuid.UUID) (domain.CommunityViewer, error) {
	viewer := domain.CommunityViewer{}
	var student struct {
		UniversityID uuid.UUID `gorm:"column:university_id"`
		DepartmentID uuid.UUID `gorm:"column:department_id"`
		BatchID      uuid.UUID `gorm:"column:batch_id"`
	}
	if err := h.db.WithContext(ctx).
		Table("students").
		Select("university_id, department_id, batch_id").
		Where("user_id = ?", userID).
		First(&student).Error; err != nil {
		// No student record: feed filters will simply match nothing.
		return viewer, nil
	}
	viewer.UniversityID = student.UniversityID
	viewer.DepartmentID = student.DepartmentID
	viewer.BatchID = student.BatchID
	return viewer, nil
}

func (h *CommunityHandler) UpdatePost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post ID"})
		return
	}

	var body struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	post, err := h.usecase.UpdatePost(c.Request.Context(), userID, postID, body.Content)
	if err != nil {
		if err == domain.ErrUnauthorized {
			c.JSON(http.StatusForbidden, gin.H{"error": "You can only edit your own posts"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, post)
}

func (h *CommunityHandler) DeletePost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post ID"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.usecase.DeletePost(c.Request.Context(), userID, postID); err != nil {
		if err == domain.ErrUnauthorized {
			c.JSON(http.StatusForbidden, gin.H{"error": "You can only delete your own posts"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Post deleted"})
}

func (h *CommunityHandler) SavePost(c *gin.Context) {
	idStr := c.Param("id")
	postID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.usecase.SavePost(c.Request.Context(), postID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Post saved to bookmarks"})
}

func (h *CommunityHandler) UnsavePost(c *gin.Context) {
	idStr := c.Param("id")
	postID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.usecase.UnsavePost(c.Request.Context(), postID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Post removed from bookmarks"})
}

func (h *CommunityHandler) LikePost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post ID"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.usecase.LikePost(c.Request.Context(), postID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Post liked"})
}

func (h *CommunityHandler) UnlikePost(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post ID"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.usecase.UnlikePost(c.Request.Context(), postID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Post unliked"})
}

func (h *CommunityHandler) AddComment(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post ID"})
		return
	}

	var body struct {
		Content  string     `json:"content" binding:"required"`
		ParentID *uuid.UUID `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	comment, err := h.usecase.AddComment(c.Request.Context(), userID, postID, body.ParentID, body.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, comment)
}

func (h *CommunityHandler) GetComments(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post ID"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	comments, err := h.usecase.GetComments(c.Request.Context(), postID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, comments)
}

func (h *CommunityHandler) LikeComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.usecase.LikeComment(c.Request.Context(), commentID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Comment liked"})
}

func (h *CommunityHandler) UnlikeComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.usecase.UnlikeComment(c.Request.Context(), commentID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Comment unliked"})
}

func (h *CommunityHandler) UpdateComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID"})
		return
	}

	var body struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	comment, err := h.usecase.UpdateComment(c.Request.Context(), userID, commentID, body.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, comment)
}

func (h *CommunityHandler) DeleteComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.usecase.DeleteComment(c.Request.Context(), userID, commentID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Comment deleted"})
}
