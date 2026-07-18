package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type communityRepository struct {
	db *gorm.DB
}

func NewCommunityRepository(db *gorm.DB) domain.CommunityRepository {
	return &communityRepository{db: db}
}

func (r *communityRepository) CreatePost(ctx context.Context, post *domain.CommunityPost) error {
	return r.db.WithContext(ctx).Create(post).Error
}

func withAuthorStudent(db *gorm.DB) *gorm.DB {
	return db.
		Preload("Author").
		Preload("Author.Student.University").
		Preload("Author.Student.Department").
		Preload("Author.Student.Batch").
		Preload("Author.Student.Session")
}

func (r *communityRepository) GetPosts(ctx context.Context, scope domain.PostScope, viewer domain.CommunityViewer, offset, limit int) ([]domain.CommunityPost, error) {
	posts := []domain.CommunityPost{}
	db := withAuthorStudent(r.db.WithContext(ctx))

	switch scope {
	case domain.ScopeBatch:
		db = db.Where("scope = ? AND batch_id = ?", domain.ScopeBatch, viewer.BatchID)
	case domain.ScopeDepartment:
		db = db.Where("scope = ? AND department_id = ?", domain.ScopeDepartment, viewer.DepartmentID)
	case domain.ScopeUniversity:
		db = db.Where("scope = ? AND university_id = ?", domain.ScopeUniversity, viewer.UniversityID)
	case domain.ScopeAll:
		// All = University-scope posts from other universities (exclude mine).
		db = db.Where("scope = ? AND university_id IS NOT NULL AND university_id != ?", domain.ScopeUniversity, viewer.UniversityID)
	default:
		db = db.Where("scope = ?", scope)
	}

	err := db.Order("community_posts.created_at desc").
		Offset(offset).
		Limit(limit).
		Find(&posts).Error
	return posts, err
}

func (r *communityRepository) GetPostByID(ctx context.Context, id uuid.UUID) (*domain.CommunityPost, error) {
	var post domain.CommunityPost
	err := withAuthorStudent(r.db.WithContext(ctx)).First(&post, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &post, nil
}

func (r *communityRepository) DeletePost(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Unscoped().Delete(&domain.CommunityPost{}, "id = ?", id).Error
}

func (r *communityRepository) UpdatePost(ctx context.Context, post *domain.CommunityPost) error {
	return r.db.WithContext(ctx).Model(&domain.CommunityPost{}).
		Where("id = ?", post.ID).
		Update("content", post.Content).Error
}

func (r *communityRepository) SavePost(ctx context.Context, postID, userID uuid.UUID) error {
	var bookmark domain.Bookmark
	result := r.db.WithContext(ctx).Unscoped().
		Where("user_id = ? AND entity_id = ? AND entity_type = ?", userID, postID, "community_post").
		First(&bookmark)

	if result.Error == nil {
		if bookmark.DeletedAt.Valid {
			return r.db.WithContext(ctx).Unscoped().
				Model(&bookmark).
				Update("deleted_at", nil).Error
		}
		return nil
	}

	bookmark = domain.Bookmark{
		UserID:     userID,
		EntityType: "community_post",
		EntityID:   postID,
	}
	bookmark.SetCreatedBy(userID)
	return r.db.WithContext(ctx).Create(&bookmark).Error
}

func (r *communityRepository) GetSavedPosts(ctx context.Context, userID uuid.UUID, offset, limit int) ([]domain.CommunityPost, error) {
	posts := []domain.CommunityPost{}
	err := withAuthorStudent(r.db.WithContext(ctx).
		Table("community_posts").
		Joins("JOIN bookmarks ON bookmarks.entity_id = community_posts.id").
		Where("bookmarks.user_id = ? AND bookmarks.entity_type = ? AND bookmarks.deleted_at IS NULL", userID, "community_post").
		Order("bookmarks.created_at desc").
		Offset(offset).
		Limit(limit)).
		Find(&posts).Error
	return posts, err
}

func (r *communityRepository) GetLikedPosts(ctx context.Context, userID uuid.UUID, offset, limit int) ([]domain.CommunityPost, error) {
	posts := []domain.CommunityPost{}
	err := withAuthorStudent(r.db.WithContext(ctx).
		Table("community_posts").
		Joins("JOIN community_post_likes ON community_post_likes.post_id = community_posts.id").
		Where("community_post_likes.user_id = ?", userID).
		Order("community_posts.created_at desc").
		Offset(offset).
		Limit(limit)).
		Find(&posts).Error
	return posts, err
}

func (r *communityRepository) IsPostSavedByUser(ctx context.Context, postID, userID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("bookmarks").
		Where("user_id = ? AND entity_id = ? AND entity_type = ? AND deleted_at IS NULL", userID, postID, "community_post").
		Count(&count).Error
	return count > 0, err
}

func (r *communityRepository) UnsavePost(ctx context.Context, postID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Table("bookmarks").
		Where("user_id = ? AND entity_id = ? AND entity_type = ?", userID, postID, "community_post").
		Delete(&domain.Bookmark{}).Error
}

func (r *communityRepository) LikePost(ctx context.Context, postID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		like := domain.CommunityPostLike{PostID: postID, UserID: userID}
		if err := tx.Create(&like).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CommunityPost{}).Where("id = ?", postID).
			UpdateColumn("likes_count", gorm.Expr("likes_count + ?", 1)).Error
	})
}

func (r *communityRepository) UnlikePost(ctx context.Context, postID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&domain.CommunityPostLike{}, "post_id = ? AND user_id = ?", postID, userID).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CommunityPost{}).Where("id = ?", postID).
			UpdateColumn("likes_count", gorm.Expr("likes_count - ?", 1)).Error
	})
}

func (r *communityRepository) IsPostLikedByUser(ctx context.Context, postID, userID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.CommunityPostLike{}).
		Where("post_id = ? AND user_id = ?", postID, userID).
		Count(&count).Error
	return count > 0, err
}

func (r *communityRepository) CreateComment(ctx context.Context, comment *domain.CommunityComment) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(comment).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CommunityPost{}).Where("id = ?", comment.PostID).
			UpdateColumn("comments_count", gorm.Expr("comments_count + ?", 1)).Error
	})
}

func (r *communityRepository) GetCommentsByPostID(ctx context.Context, postID uuid.UUID) ([]domain.CommunityComment, error) {
	var allComments []domain.CommunityComment
	err := r.db.WithContext(ctx).Where("post_id = ?", postID).
		Order("created_at asc").
		Preload("Author").
		Find(&allComments).Error
	if err != nil {
		return nil, err
	}

	// Create a map for quick lookups using pointers to the elements in the slice
	commentMap := make(map[uuid.UUID]*domain.CommunityComment)
	for i := range allComments {
		// Initialize replies to empty slice of pointers
		allComments[i].Replies = []*domain.CommunityComment{}
		commentMap[allComments[i].ID] = &allComments[i]
	}

	// Build the tree
	for i := range allComments {
		comment := &allComments[i]
		if comment.ParentID == nil {
			// Roots will be collected later to avoid copying incomplete objects
		} else {
			if parent, ok := commentMap[*comment.ParentID]; ok {
				// Add pointer to the current element in the slice to the parent's replies
				parent.Replies = append(parent.Replies, comment)
			}
		}
	}

	// Collect only the root comments (which now contain nested pointers to all descendants)
	finalRoots := []domain.CommunityComment{}
	for i := range allComments {
		if allComments[i].ParentID == nil {
			finalRoots = append(finalRoots, allComments[i])
		}
	}

	return finalRoots, nil
}

func (r *communityRepository) GetCommentByID(ctx context.Context, id uuid.UUID) (*domain.CommunityComment, error) {
	var comment domain.CommunityComment
	err := r.db.WithContext(ctx).Preload("Author").First(&comment, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &comment, nil
}

func (r *communityRepository) UpdateComment(ctx context.Context, comment *domain.CommunityComment) error {
	return r.db.WithContext(ctx).Save(comment).Error
}

func (r *communityRepository) DeleteComment(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var comment domain.CommunityComment
		if err := tx.First(&comment, "id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&comment).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CommunityPost{}).Where("id = ?", comment.PostID).
			UpdateColumn("comments_count", gorm.Expr("comments_count - ?", 1)).Error
	})
}

func (r *communityRepository) LikeComment(ctx context.Context, commentID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		like := domain.CommunityCommentLike{CommentID: commentID, UserID: userID}
		if err := tx.Create(&like).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CommunityComment{}).Where("id = ?", commentID).
			UpdateColumn("likes_count", gorm.Expr("likes_count + ?", 1)).Error
	})
}

func (r *communityRepository) UnlikeComment(ctx context.Context, commentID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&domain.CommunityCommentLike{}, "comment_id = ? AND user_id = ?", commentID, userID).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CommunityComment{}).Where("id = ?", commentID).
			UpdateColumn("likes_count", gorm.Expr("likes_count - ?", 1)).Error
	})
}
