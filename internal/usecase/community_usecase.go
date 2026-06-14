package usecase

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

type communityUseCase struct {
	repo domain.CommunityRepository
}

func NewCommunityUseCase(repo domain.CommunityRepository) domain.CommunityUseCase {
	return &communityUseCase{repo: repo}
}

func (u *communityUseCase) CreatePost(ctx context.Context, authorID uuid.UUID, content string, scope domain.PostScope) (*domain.CommunityPost, error) {
	post := &domain.CommunityPost{
		Content:  content,
		Scope:    scope,
		AuthorID: authorID,
	}
	post.SetCreatedBy(authorID)

	if err := u.repo.CreatePost(ctx, post); err != nil {
		return nil, err
	}
	return u.repo.GetPostByID(ctx, post.ID)
}

func (u *communityUseCase) GetPosts(ctx context.Context, userID uuid.UUID, scope domain.PostScope, page, pageSize int) ([]domain.CommunityPost, error) {
	if scope == domain.ScopeSaved {
		return u.GetSavedPosts(ctx, userID, page, pageSize)
	}

	offset := (page - 1) * pageSize
	posts, err := u.repo.GetPosts(ctx, scope, offset, pageSize)
	if err != nil {
		return nil, err
	}

	// Check if user has liked each post
	for i := range posts {
		liked, _ := u.repo.IsPostLikedByUser(ctx, posts[i].ID, userID)
		posts[i].IsLiked = liked
		saved, _ := u.repo.IsPostSavedByUser(ctx, posts[i].ID, userID)
		posts[i].IsSaved = saved
	}

	return posts, nil
}

func (u *communityUseCase) GetSavedPosts(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]domain.CommunityPost, error) {
	offset := (page - 1) * pageSize
	posts, err := u.repo.GetSavedPosts(ctx, userID, offset, pageSize)
	if err != nil {
		return nil, err
	}

	for i := range posts {
		liked, _ := u.repo.IsPostLikedByUser(ctx, posts[i].ID, userID)
		posts[i].IsLiked = liked
		posts[i].IsSaved = true // Since they are from saved collection
	}

	return posts, nil
}

func (u *communityUseCase) SavePost(ctx context.Context, postID, userID uuid.UUID) error {
	return u.repo.SavePost(ctx, postID, userID)
}

func (u *communityUseCase) UnsavePost(ctx context.Context, postID, userID uuid.UUID) error {
	return u.repo.UnsavePost(ctx, postID, userID)
}

func (u *communityUseCase) LikePost(ctx context.Context, postID, userID uuid.UUID) error {
	return u.repo.LikePost(ctx, postID, userID)
}

func (u *communityUseCase) UnlikePost(ctx context.Context, postID, userID uuid.UUID) error {
	return u.repo.UnlikePost(ctx, postID, userID)
}

func (u *communityUseCase) AddComment(ctx context.Context, authorID, postID uuid.UUID, parentID *uuid.UUID, content string) (*domain.CommunityComment, error) {
	comment := &domain.CommunityComment{
		Content:  content,
		PostID:   postID,
		ParentID: parentID,
		AuthorID: authorID,
	}
	comment.SetCreatedBy(authorID)

	if err := u.repo.CreateComment(ctx, comment); err != nil {
		return nil, err
	}

	// Fetch with author info
	comments, err := u.repo.GetCommentsByPostID(ctx, postID)
	if err != nil {
		return nil, err
	}
	
	// Helpers to find comment in potentially nested list
	var findInPointers func([]*domain.CommunityComment) *domain.CommunityComment
	findInPointers = func(list []*domain.CommunityComment) *domain.CommunityComment {
		for _, c := range list {
			if c.ID == comment.ID {
				return c
			}
			if len(c.Replies) > 0 {
				if found := findInPointers(c.Replies); found != nil {
					return found
				}
			}
		}
		return nil
	}

	var findInObjects func([]domain.CommunityComment) *domain.CommunityComment
	findInObjects = func(list []domain.CommunityComment) *domain.CommunityComment {
		for i := range list {
			if list[i].ID == comment.ID {
				return &list[i]
			}
			if len(list[i].Replies) > 0 {
				if found := findInPointers(list[i].Replies); found != nil {
					return found
				}
			}
		}
		return nil
	}

	found := findInObjects(comments)
	if found != nil {
		return found, nil
	}
	return comment, nil
}

func (u *communityUseCase) GetComments(ctx context.Context, postID uuid.UUID, userID uuid.UUID) ([]domain.CommunityComment, error) {
	comments, err := u.repo.GetCommentsByPostID(ctx, postID)
	if err != nil {
		return nil, err
	}

	// TODO: Ideally we should have a bulk check for comment likes
	// For now we'll skip is_liked for comments or do it simply if needed
	return comments, nil
}

func (u *communityUseCase) UpdateComment(ctx context.Context, authorID, commentID uuid.UUID, content string) (*domain.CommunityComment, error) {
	comment, err := u.repo.GetCommentByID(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if comment.AuthorID != authorID {
		return nil, domain.ErrUnauthorized
	}
	comment.Content = content
	if err := u.repo.UpdateComment(ctx, comment); err != nil {
		return nil, err
	}
	return comment, nil
}

func (u *communityUseCase) DeleteComment(ctx context.Context, authorID, commentID uuid.UUID) error {
	comment, err := u.repo.GetCommentByID(ctx, commentID)
	if err != nil {
		return err
	}
	if comment.AuthorID != authorID {
		return domain.ErrUnauthorized
	}
	return u.repo.DeleteComment(ctx, commentID)
}

func (u *communityUseCase) LikeComment(ctx context.Context, commentID, userID uuid.UUID) error {
	return u.repo.LikeComment(ctx, commentID, userID)
}

func (u *communityUseCase) UnlikeComment(ctx context.Context, commentID, userID uuid.UUID) error {
	return u.repo.UnlikeComment(ctx, commentID, userID)
}
