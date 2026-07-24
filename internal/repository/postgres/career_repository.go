package postgres

import (
	"context"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type careerRepository struct {
	db *gorm.DB
}

func NewCareerRepository(db *gorm.DB) domain.CareerRepository {
	return &careerRepository{db: db}
}

func (r *careerRepository) GetAllCirculars(ctx context.Context, filter domain.CareerCircularFilter) ([]domain.CareerCircular, int64, error) {
	var circulars []domain.CareerCircular
	q := r.db.WithContext(ctx).Model(&domain.CareerCircular{}).
		Preload("Targets").Preload("Category")

	if filter.CategoryID != uuid.Nil {
		q = q.Where("category_id = ?", filter.CategoryID)
	}
	if filter.IsPublished != nil {
		q = q.Where("is_published = ?", *filter.IsPublished)
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("title ILIKE ? OR organization ILIKE ?", like, like)
	}

	var count int64
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	err := q.Order("created_at desc").Limit(limit).Offset(filter.Offset).Find(&circulars).Error
	return circulars, count, err
}

func (r *careerRepository) GetCircularByID(ctx context.Context, id uuid.UUID) (*domain.CareerCircular, error) {
	var circular domain.CareerCircular
	err := r.db.WithContext(ctx).Preload("Targets").Preload("Category").First(&circular, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &circular, nil
}

func (r *careerRepository) CreateCircular(ctx context.Context, circular *domain.CareerCircular) error {
	return r.db.WithContext(ctx).Create(circular).Error
}

func (r *careerRepository) UpdateCircular(ctx context.Context, circular *domain.CareerCircular) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete old targets first to handle multi-select updates correctly
		// (same reasoning as productRepository.UpdateProduct).
		if err := tx.Where("circular_id = ?", circular.ID).Delete(&domain.CareerCircularTarget{}).Error; err != nil {
			return err
		}
		return tx.Save(circular).Error
	})
}

func (r *careerRepository) DeleteCircular(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.CareerCircular{}, id).Error
}

func (r *careerRepository) IncrementCircularViews(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.CareerCircular{}).
		Where("id = ?", id).
		UpdateColumn("views_count", gorm.Expr("views_count + 1")).Error
}

func (r *careerRepository) GetCircularsByLocation(ctx context.Context, universityID, departmentID, categoryID uuid.UUID, search string) ([]domain.CareerCircular, error) {
	var circulars []domain.CareerCircular
	q := r.db.WithContext(ctx).
		Distinct("career_circulars.*").
		Joins("LEFT JOIN career_circular_targets ON career_circular_targets.circular_id = career_circulars.id").
		Where("career_circulars.is_published = ?", true).
		Where("career_circular_targets.id IS NULL OR (career_circular_targets.university_id = ? AND (career_circular_targets.department_id = ? OR career_circular_targets.department_id = ?))",
			universityID, departmentID, uuid.Nil)

	if categoryID != uuid.Nil {
		q = q.Where("career_circulars.category_id = ?", categoryID)
	}
	if search != "" {
		like := "%" + search + "%"
		q = q.Where("career_circulars.title ILIKE ? OR career_circulars.organization ILIKE ?", like, like)
	}

	err := q.Order("career_circulars.created_at desc").
		Preload("Category").
		Find(&circulars).Error
	return circulars, err
}

func (r *careerRepository) GetMyJobs(ctx context.Context, userID uuid.UUID) ([]domain.CareerJob, error) {
	var jobs []domain.CareerJob
	err := r.db.WithContext(ctx).Preload("Category").Where("user_id = ?", userID).
		Order("created_at desc").Find(&jobs).Error
	return jobs, err
}

func (r *careerRepository) GetJobByID(ctx context.Context, id uuid.UUID) (*domain.CareerJob, error) {
	var job domain.CareerJob
	err := r.db.WithContext(ctx).Preload("Category").First(&job, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *careerRepository) CreateJob(ctx context.Context, job *domain.CareerJob) error {
	return r.db.WithContext(ctx).Create(job).Error
}

func (r *careerRepository) UpdateJob(ctx context.Context, job *domain.CareerJob) error {
	return r.db.WithContext(ctx).Save(job).Error
}

func (r *careerRepository) SetJobStatus(ctx context.Context, id uuid.UUID, status domain.CareerJobStatus) error {
	return r.db.WithContext(ctx).Model(&domain.CareerJob{}).Where("id = ?", id).Update("status", status).Error
}

func (r *careerRepository) DeleteJob(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.CareerJob{}, id).Error
}

func (r *careerRepository) GetSharedJobsByScope(ctx context.Context, scope domain.CareerJobScope, viewer domain.CommunityViewer) ([]domain.CareerJob, error) {
	var jobs []domain.CareerJob
	q := r.db.WithContext(ctx).Preload("Poster").Preload("Category")

	switch scope {
	case domain.CareerJobScopeBatch:
		q = q.Where("scope = ? AND batch_id = ?", domain.CareerJobScopeBatch, viewer.BatchID)
	case domain.CareerJobScopeDepartment:
		q = q.Where("scope = ? AND department_id = ?", domain.CareerJobScopeDepartment, viewer.DepartmentID)
	case domain.CareerJobScopeUniversity:
		q = q.Where("scope = ? AND university_id = ?", domain.CareerJobScopeUniversity, viewer.UniversityID)
	default:
		return nil, nil
	}

	err := q.Order("created_at desc").Find(&jobs).Error
	return jobs, err
}

func (r *careerRepository) CreateReminder(ctx context.Context, reminder *domain.CareerReminder) error {
	return r.db.WithContext(ctx).Create(reminder).Error
}

func (r *careerRepository) GetReminderByID(ctx context.Context, id uuid.UUID) (*domain.CareerReminder, error) {
	var reminder domain.CareerReminder
	err := r.db.WithContext(ctx).First(&reminder, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &reminder, nil
}

func (r *careerRepository) GetMyReminders(ctx context.Context, userID uuid.UUID) ([]domain.CareerReminder, error) {
	var reminders []domain.CareerReminder
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status IN ?", userID, []domain.CareerReminderStatus{domain.CareerReminderPending, domain.CareerReminderSent}).
		Order("remind_at asc").Find(&reminders).Error
	return reminders, err
}

func (r *careerRepository) CancelReminder(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.CareerReminder{}).
		Where("id = ? AND status = ?", id, domain.CareerReminderPending).
		Update("status", domain.CareerReminderCancelled).Error
}

// ClaimDueReminders atomically claims due, pending reminders via
// SELECT ... FOR UPDATE SKIP LOCKED so concurrent API replicas never
// double-send the same reminder.
func (r *careerRepository) ClaimDueReminders(ctx context.Context, before time.Time, limit int) ([]domain.CareerReminder, error) {
	var claimed []domain.CareerReminder
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var due []domain.CareerReminder
		if err := tx.Raw(
			"SELECT * FROM career_reminders WHERE status = ? AND remind_at <= ? ORDER BY remind_at ASC LIMIT ? FOR UPDATE SKIP LOCKED",
			domain.CareerReminderPending, before, limit,
		).Scan(&due).Error; err != nil {
			return err
		}
		if len(due) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(due))
		for i, rem := range due {
			ids[i] = rem.ID
		}
		if err := tx.Model(&domain.CareerReminder{}).Where("id IN ?", ids).
			Update("status", domain.CareerReminderClaimed).Error; err != nil {
			return err
		}
		claimed = due
		return nil
	})
	return claimed, err
}

func (r *careerRepository) MarkReminderSent(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.CareerReminder{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": domain.CareerReminderSent, "sent_at": gorm.Expr("NOW()")}).Error
}
