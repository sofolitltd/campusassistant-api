package domain

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type PostScope string

const (
	ScopeBatch      PostScope = "Batch"
	ScopeDepartment PostScope = "Department"
	ScopeUniversity PostScope = "University"
	ScopeSaved      PostScope = "Saved"
	ScopeAll        PostScope = "All"
)

// CommunityViewer holds the requesting user's organizational context used to
// scope feed queries (my batch / department / university, and "All" = other
// universities).
type CommunityViewer struct {
	UniversityID uuid.UUID
	DepartmentID uuid.UUID
	BatchID      uuid.UUID
}

// NormalizeScope maps a (possibly lowercase) scope string from the client to a
// canonical PostScope value. Unknown values are passed through unchanged.
func NormalizeScope(s string) PostScope {
	switch strings.ToLower(s) {
	case "batch":
		return ScopeBatch
	case "department":
		return ScopeDepartment
	case "university":
		return ScopeUniversity
	case "saved":
		return ScopeSaved
	case "all":
		return ScopeAll
	default:
		return PostScope(s)
	}
}

type CommunityPost struct {
	Base
	Content       string             `gorm:"type:text;not null" json:"content"`
	Scope         PostScope          `gorm:"type:varchar(20);index;not null" json:"scope"`
	ImageURLs     pq.StringArray     `gorm:"type:text[];default:'{}'" json:"image_urls"`
	LikesCount    int                `gorm:"default:0" json:"likes_count"`
	CommentsCount int                `gorm:"default:0" json:"comments_count"`
	Comments      []CommunityComment `gorm:"foreignKey:PostID" json:"comments,omitempty"`

	// Organizational context of the author at post time (for easy filtering).
	UniversityID *uuid.UUID `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID *uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"`
	BatchID      *uuid.UUID `gorm:"type:uuid;index" json:"batch_id,omitempty"`

	// Relationships
	AuthorID uuid.UUID `gorm:"type:uuid;not null" json:"author_id"`
	Author   *User     `gorm:"foreignKey:AuthorID" json:"author,omitempty"`

	// Client-only field
	IsLiked bool `gorm:"-" json:"is_liked"`
	IsSaved bool `gorm:"-" json:"is_saved"`
}

type CommunityComment struct {
	Base
	Content    string    `gorm:"type:text;not null" json:"content"`
	PostID     uuid.UUID `gorm:"type:uuid;index;not null" json:"post_id"`
	ParentID   *uuid.UUID `gorm:"type:uuid;index" json:"parent_id,omitempty"`
	LikesCount int       `gorm:"default:0" json:"likes_count"`

	// Relationships
	AuthorID uuid.UUID `gorm:"type:uuid;not null" json:"author_id"`
	Author   *User     `gorm:"foreignKey:AuthorID" json:"author,omitempty"`
	Replies  []*CommunityComment `gorm:"foreignKey:ParentID" json:"replies,omitempty"`

	// Client-only
	IsLiked bool `gorm:"-" json:"is_liked"`
}

type CommunityPostLike struct {
	PostID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID uuid.UUID `gorm:"type:uuid;primaryKey"`
}

type CommunityCommentLike struct {
	CommentID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey"`
}

type CommunityRepository interface {
	CreatePost(ctx context.Context, post *CommunityPost) error
	GetPosts(ctx context.Context, scope PostScope, viewer CommunityViewer, offset, limit int) ([]CommunityPost, error)
	GetPostByID(ctx context.Context, id uuid.UUID) (*CommunityPost, error)
	UpdatePost(ctx context.Context, post *CommunityPost) error
	DeletePost(ctx context.Context, id uuid.UUID) error

	SavePost(ctx context.Context, postID, userID uuid.UUID) error
	GetSavedPosts(ctx context.Context, userID uuid.UUID, offset, limit int) ([]CommunityPost, error)
	GetLikedPosts(ctx context.Context, userID uuid.UUID, offset, limit int) ([]CommunityPost, error)
	IsPostSavedByUser(ctx context.Context, postID, userID uuid.UUID) (bool, error)
	UnsavePost(ctx context.Context, postID, userID uuid.UUID) error

	LikePost(ctx context.Context, postID, userID uuid.UUID) error
	UnlikePost(ctx context.Context, postID, userID uuid.UUID) error
	IsPostLikedByUser(ctx context.Context, postID, userID uuid.UUID) (bool, error)

	CreateComment(ctx context.Context, comment *CommunityComment) error
	GetCommentsByPostID(ctx context.Context, postID uuid.UUID) ([]CommunityComment, error)
	GetCommentByID(ctx context.Context, id uuid.UUID) (*CommunityComment, error)
	UpdateComment(ctx context.Context, comment *CommunityComment) error
	DeleteComment(ctx context.Context, id uuid.UUID) error

	LikeComment(ctx context.Context, commentID, userID uuid.UUID) error
	UnlikeComment(ctx context.Context, commentID, userID uuid.UUID) error
}

type CommunityUseCase interface {
	CreatePost(ctx context.Context, authorID uuid.UUID, content string, scope PostScope, imageURLs []string) (*CommunityPost, error)
	GetPosts(ctx context.Context, userID uuid.UUID, viewer CommunityViewer, scope PostScope, page, pageSize int) ([]CommunityPost, error)
	UpdatePost(ctx context.Context, authorID, postID uuid.UUID, content string) (*CommunityPost, error)
	DeletePost(ctx context.Context, authorID, postID uuid.UUID) error

	SavePost(ctx context.Context, postID, userID uuid.UUID) error
	GetSavedPosts(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]CommunityPost, error)
	GetLikedPosts(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]CommunityPost, error)
	UnsavePost(ctx context.Context, postID, userID uuid.UUID) error
	LikePost(ctx context.Context, postID, userID uuid.UUID) error
	UnlikePost(ctx context.Context, postID, userID uuid.UUID) error
	AddComment(ctx context.Context, authorID, postID uuid.UUID, parentID *uuid.UUID, content string) (*CommunityComment, error)
	GetComments(ctx context.Context, postID uuid.UUID, userID uuid.UUID) ([]CommunityComment, error)
	UpdateComment(ctx context.Context, authorID, commentID uuid.UUID, content string) (*CommunityComment, error)
	DeleteComment(ctx context.Context, authorID, commentID uuid.UUID) error
	LikeComment(ctx context.Context, commentID, userID uuid.UUID) error
	UnlikeComment(ctx context.Context, commentID, userID uuid.UUID) error
}
