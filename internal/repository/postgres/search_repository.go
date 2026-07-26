package postgres

import (
	"context"
	"strings"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SearchRepository fans a single query out across every publicly searchable
// entity type. It's dedicated (not generic CRUD) and doesn't go through a
// usecase layer, same shape as StatsRepository — it doesn't map to a single
// domain.Repository[T].
type SearchRepository struct {
	db *gorm.DB
}

func NewSearchRepository(db *gorm.DB) *SearchRepository {
	return &SearchRepository{db: db}
}

// allSearchTypes is the default set searched when the caller doesn't pass
// ?types=.
var allSearchTypes = []string{
	"resource", "notice", "course", "club", "association",
	"teacher", "staff", "marketplace", "lost_found", "career",
}

func (r *SearchRepository) Search(ctx context.Context, query string, types []string, universityID, departmentID string, limitPerType int) (*domain.SearchResults, error) {
	results := &domain.SearchResults{
		Query:   query,
		Results: map[string]interface{}{},
	}

	trimmed := strings.TrimSpace(query)
	if len(trimmed) < 2 {
		return results, nil
	}

	if limitPerType <= 0 {
		limitPerType = 5
	}
	if limitPerType > 20 {
		limitPerType = 20
	}

	wanted := types
	if len(wanted) == 0 {
		wanted = allSearchTypes
	}
	want := make(map[string]bool, len(wanted))
	for _, t := range wanted {
		want[strings.TrimSpace(t)] = true
	}

	uniID, _ := uuid.Parse(universityID)
	deptID, _ := uuid.Parse(departmentID)
	like := "%" + trimmed + "%"

	type searchFn func(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error)
	fns := map[string]searchFn{
		"resource":    r.searchResources,
		"notice":      r.searchNotices,
		"course":      r.searchCourses,
		"club":        r.searchClubs,
		"association": r.searchAssociations,
		"teacher":     r.searchTeachers,
		"staff":       r.searchStaff,
		"marketplace": r.searchProducts,
		"lost_found":  r.searchLostFound,
		"career":      r.searchCareerCirculars,
	}

	for _, t := range allSearchTypes {
		if !want[t] {
			continue
		}
		fn, ok := fns[t]
		if !ok {
			continue
		}
		items, count, err := fn(ctx, like, uniID, deptID, limitPerType)
		if err != nil {
			return nil, err
		}
		results.Results[t] = items
		results.Total += count
	}

	return results, nil
}

// searchResources mirrors resourceRepository.GetAll: same published-status
// default, same ILIKE targets, plus the Preload("Batches") that GET
// /resources' list path always applies so the JSON shape (batches field)
// matches byte-for-byte.
func (r *SearchRepository) searchResources(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.Resource
	q := r.db.WithContext(ctx).Model(&domain.Resource{}).
		Where("resources.status = ?", domain.ResourceStatusPublished).
		Where("resources.title ILIKE ? OR resources.description ILIKE ? OR resources.course_code ILIKE ?", like, like, like)
	if uniID != uuid.Nil {
		q = q.Where("resources.university_id = ?", uniID)
	}
	if deptID != uuid.Nil {
		q = q.Where("resources.department_id = ?", deptID)
	}
	if err := q.Preload("Batches").Order("resources.created_at desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchNotices mirrors GET /notices' plain GormRepository.GetAll — no
// preloads are applied there without an explicit ?preload=true, so none are
// added here either.
func (r *SearchRepository) searchNotices(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.Notice
	q := r.db.WithContext(ctx).Model(&domain.Notice{}).
		Where("notices.message ILIKE ? OR notices.uploader ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("notices.university_id = ?", uniID)
	}
	if deptID != uuid.Nil {
		q = q.Where("notices.department_id = ?", deptID)
	}
	if err := q.Order("notices.created_at desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchCourses mirrors courseRepository.GetAll's Preload("Batches").
// Preload("CourseCategory").Preload("Level") so course cards get the same
// nested objects GET /courses returns.
func (r *SearchRepository) searchCourses(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.Course
	q := r.db.WithContext(ctx).Model(&domain.Course{}).
		Where("courses.course_title ILIKE ? OR courses.course_code ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("courses.university_id = ?", uniID)
	}
	if deptID != uuid.Nil {
		q = q.Where("courses.department_id = ?", deptID)
	}
	if err := q.Preload("Batches").Preload("CourseCategory").Preload("Level").
		Order("courses.course_code asc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchClubs mirrors clubRepository.GetAllClubs. IsFollowing/IsMember are
// only populated there when a RequestingUserID is available — the search
// endpoint has no authenticated-user context to key that off of, so those
// fields stay at their zero value here, same as any unauthenticated
// GET /clubs call.
func (r *SearchRepository) searchClubs(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.Club
	q := r.db.WithContext(ctx).Model(&domain.Club{}).
		Where("clubs.is_active = ?", true).
		Where("clubs.name ILIKE ? OR clubs.description ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("clubs.university_id = ?", uniID)
	}
	if deptID != uuid.Nil {
		q = q.Where("clubs.department_id IS NULL OR clubs.department_id = ?", deptID)
	}
	if err := q.Order("clubs.created_at desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchAssociations mirrors associationRepository.GetAllAssociations — same
// reasoning as searchClubs re: IsFollowing/IsMember/IsPendingMember staying
// zero-valued without a RequestingUserID.
func (r *SearchRepository) searchAssociations(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	// Association is district-based, not department-scoped — no
	// department_id column, so departmentID is intentionally unused here.
	var rows []domain.Association
	q := r.db.WithContext(ctx).Model(&domain.Association{}).
		Where("associations.is_active = ?", true).
		Where("associations.name ILIKE ? OR associations.description ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("associations.university_id = ?", uniID)
	}
	if err := q.Order("associations.created_at desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchTeachers mirrors GET /teachers (GenericHandler backed by a plain
// GormRepository) — no preloads without ?preload=true, none added here.
func (r *SearchRepository) searchTeachers(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.Teacher
	q := r.db.WithContext(ctx).Model(&domain.Teacher{}).
		Where("teachers.name ILIKE ? OR teachers.designation ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("teachers.university_id = ?", uniID)
	}
	if deptID != uuid.Nil {
		q = q.Where("teachers.department_id = ?", deptID)
	}
	if err := q.Order("teachers.name asc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchStaff mirrors GET /staffs (also a plain GormRepository via
// registerRoutes) — no preloads without ?preload=true.
func (r *SearchRepository) searchStaff(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.Staff
	q := r.db.WithContext(ctx).Model(&domain.Staff{}).
		Where("staffs.name ILIKE ? OR staffs.post ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("staffs.university_id = ?", uniID)
	}
	if deptID != uuid.Nil {
		q = q.Where("staffs.department_id = ?", deptID)
	}
	if err := q.Order("staffs.name asc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchProducts mirrors productRepository.GetAllProducts, which is what GET
// /products (the marketplace list endpoint) uses — it preloads Targets,
// Merchant, and Category. The existing published+targeting-scoped WHERE
// clause here is left untouched.
func (r *SearchRepository) searchProducts(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.Product
	q := r.db.WithContext(ctx).
		Distinct("products.*").
		Joins("LEFT JOIN product_targets ON product_targets.product_id = products.id").
		Where("products.is_published = ?", true).
		Where("products.title ILIKE ? OR products.description ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("product_targets.id IS NULL OR (product_targets.university_id = ? AND (product_targets.department_id = ? OR product_targets.department_id = ?))",
			uniID, deptID, uuid.Nil)
	}
	if err := q.Preload("Targets").Preload("Merchant").Preload("Category").
		Order("products.created_at desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchLostFound mirrors lostFoundRepository.GetAllItems, which is what GET
// /lost-found-items uses — it preloads Targets, Category, Poster. The
// existing open-status+targeting-scoped WHERE clause here is left untouched.
func (r *SearchRepository) searchLostFound(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.LostFoundItem
	q := r.db.WithContext(ctx).
		Distinct("lost_found_items.*").
		Joins("LEFT JOIN lost_found_item_targets ON lost_found_item_targets.item_id = lost_found_items.id").
		Where("lost_found_items.status = ?", domain.LostFoundStatusOpen).
		Where("lost_found_items.title ILIKE ? OR lost_found_items.description ILIKE ? OR lost_found_items.location ILIKE ?", like, like, like)
	if uniID != uuid.Nil {
		q = q.Where("lost_found_item_targets.id IS NULL OR (lost_found_item_targets.university_id = ? AND (lost_found_item_targets.department_id = ? OR lost_found_item_targets.department_id = ?))",
			uniID, deptID, uuid.Nil)
	}
	if err := q.Preload("Targets").Preload("Category").Preload("Poster").
		Order("lost_found_items.created_at desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}

// searchCareerCirculars mirrors careerRepository.GetAllCirculars, which is
// what GET /career-circulars uses — it preloads Targets, Category. The
// existing published+targeting-scoped WHERE clause here is left untouched.
func (r *SearchRepository) searchCareerCirculars(ctx context.Context, like string, uniID, deptID uuid.UUID, limit int) (interface{}, int, error) {
	var rows []domain.CareerCircular
	q := r.db.WithContext(ctx).
		Distinct("career_circulars.*").
		Joins("LEFT JOIN career_circular_targets ON career_circular_targets.circular_id = career_circulars.id").
		Where("career_circulars.is_published = ?", true).
		Where("career_circulars.title ILIKE ? OR career_circulars.organization ILIKE ?", like, like)
	if uniID != uuid.Nil {
		q = q.Where("career_circular_targets.id IS NULL OR (career_circular_targets.university_id = ? AND (career_circular_targets.department_id = ? OR career_circular_targets.department_id = ?))",
			uniID, deptID, uuid.Nil)
	}
	if err := q.Preload("Targets").Preload("Category").
		Order("career_circulars.created_at desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, len(rows), nil
}
