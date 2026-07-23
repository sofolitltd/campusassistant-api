package handler

import (
	"net/http"
	"strconv"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CareerHandler struct {
	repo domain.CareerRepository
	db   *gorm.DB
}

func NewCareerHandler(repo domain.CareerRepository, db *gorm.DB) *CareerHandler {
	return &CareerHandler{repo: repo, db: db}
}

// resolveViewer builds the requesting user's organizational context from
// their Student record, same source/shape as CommunityHandler.resolveViewer,
// so a shared Job's scope filter matches reliably.
func (h *CareerHandler) resolveViewer(ctx *gin.Context, userID uuid.UUID) domain.CommunityViewer {
	viewer := domain.CommunityViewer{}
	var student struct {
		UniversityID uuid.UUID `gorm:"column:university_id"`
		DepartmentID uuid.UUID `gorm:"column:department_id"`
		BatchID      uuid.UUID `gorm:"column:batch_id"`
	}
	if err := h.db.WithContext(ctx.Request.Context()).
		Table("students").
		Select("university_id, department_id, batch_id").
		Where("user_id = ?", userID).
		First(&student).Error; err != nil {
		return viewer
	}
	viewer.UniversityID = student.UniversityID
	viewer.DepartmentID = student.DepartmentID
	viewer.BatchID = student.BatchID
	return viewer
}

// ---- Admin (full CRUD — circulars are admin-authored) ----

func (h *CareerHandler) GetAllCirculars(c *gin.Context) {
	categoryID, _ := uuid.Parse(c.Query("category_id"))
	var isPublished *bool
	if v := c.Query("is_published"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err == nil {
			isPublished = &parsed
		}
	}
	limit, offset := parseLimitOffset(c)
	filter := domain.CareerCircularFilter{
		CategoryID:  categoryID,
		IsPublished: isPublished,
		Search:      c.Query("search"),
		Limit:       limit,
		Offset:      offset,
	}
	circulars, count, err := h.repo.GetAllCirculars(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch circulars"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": circulars, "count": count, "limit": limit, "offset": offset})
}

func (h *CareerHandler) GetCircularByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid circular id"})
		return
	}
	circular, err := h.repo.GetCircularByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Circular not found"})
		return
	}
	c.JSON(http.StatusOK, circular)
}

func (h *CareerHandler) CreateCircular(c *gin.Context) {
	var circular domain.CareerCircular
	if err := c.ShouldBindJSON(&circular); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.CreateCircular(c.Request.Context(), &circular); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create circular"})
		return
	}
	c.JSON(http.StatusOK, circular)
}

func (h *CareerHandler) UpdateCircular(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid circular id"})
		return
	}
	// Load the existing row and bind JSON onto it (not a blank struct) so a
	// partial payload doesn't zero out fields the caller omitted — same
	// reasoning as ProductHandler.UpdateProduct.
	circular, err := h.repo.GetCircularByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Circular not found"})
		return
	}
	if err := c.ShouldBindJSON(circular); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	circular.ID = id
	if err := h.repo.UpdateCircular(c.Request.Context(), circular); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update circular"})
		return
	}
	c.JSON(http.StatusOK, circular)
}

func (h *CareerHandler) DeleteCircular(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid circular id"})
		return
	}
	if err := h.repo.DeleteCircular(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete circular"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Circular deleted"})
}

// ---- Public browse (app-facing) ----

// GetCircularsByLocation is the app-facing browse endpoint: published
// circulars that are global (no targets) or targeted to this
// university/department. Optional ?category_id=&search= narrow further.
func (h *CareerHandler) GetCircularsByLocation(c *gin.Context) {
	universityID, _ := uuid.Parse(c.Query("university_id"))
	departmentID, _ := uuid.Parse(c.Query("department_id"))
	categoryID, _ := uuid.Parse(c.Query("category_id"))

	circulars, err := h.repo.GetCircularsByLocation(c.Request.Context(), universityID, departmentID, categoryID, c.Query("search"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch circulars"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": circulars})
}

func (h *CareerHandler) ViewCircular(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid circular id"})
		return
	}
	if err := h.repo.IncrementCircularViews(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record view"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "View recorded"})
}

// ---- Self-service: My Jobs (JWT) ----

func (h *CareerHandler) GetMyJobs(c *gin.Context) {
	jobs, err := h.repo.GetMyJobs(c.Request.Context(), currentUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch jobs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": jobs})
}

// CreateMyJob is the manual "Add Job" flow — a freeform entry not tied to
// any circular.
func (h *CareerHandler) CreateMyJob(c *gin.Context) {
	var job domain.CareerJob
	if err := c.ShouldBindJSON(&job); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := currentUserID(c)
	job.UserID = userID
	job.CircularID = nil
	if job.Status == "" {
		job.Status = domain.CareerJobPending
	}

	// Opt-in peer sharing: stamp the poster's own affiliation at creation
	// time so scope-matching (GetSharedJobsByScope) has something to
	// compare against later — same reasoning as CommunityUseCase.CreatePost.
	if job.Scope == "" {
		job.Scope = domain.CareerJobScopePrivate
	}
	if job.Scope != domain.CareerJobScopePrivate {
		viewer := h.resolveViewer(c, userID)
		job.UniversityID = &viewer.UniversityID
		job.DepartmentID = &viewer.DepartmentID
		job.BatchID = &viewer.BatchID
	}

	if err := h.repo.CreateJob(c.Request.Context(), &job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job"})
		return
	}
	c.JSON(http.StatusOK, job)
}

// GetSharedJobs lists other students' Jobs shared at ?scope=batch|department|university.
func (h *CareerHandler) GetSharedJobs(c *gin.Context) {
	scope := domain.CareerJobScope(c.Query("scope"))
	if scope != domain.CareerJobScopeBatch && scope != domain.CareerJobScopeDepartment && scope != domain.CareerJobScopeUniversity {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be batch, department, or university"})
		return
	}
	viewer := h.resolveViewer(c, currentUserID(c))
	jobs, err := h.repo.GetSharedJobsByScope(c.Request.Context(), scope, viewer)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch shared jobs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": jobs})
}

// CreateMyJobFromCircular is the "Save to My Jobs" pull — copies the
// circular's fields into a new job owned by the caller so later edits/
// deletion of the circular never affect it.
func (h *CareerHandler) CreateMyJobFromCircular(c *gin.Context) {
	circularID, err := uuid.Parse(c.Param("circularId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid circular id"})
		return
	}
	circular, err := h.repo.GetCircularByID(c.Request.Context(), circularID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Circular not found"})
		return
	}

	job := domain.CareerJob{
		UserID:         currentUserID(c),
		CircularID:     &circular.ID,
		Title:          circular.Title,
		Organization:   circular.Organization,
		PostLink:       circular.PostLink,
		ResourceLink:   circular.ResourceLink,
		AttachmentURLs: circular.AttachmentURLs,
		PublishDate:    circular.PublishDate,
		DeadlineDate:   circular.DeadlineDate,
		Status:         domain.CareerJobPending,
	}
	if err := h.repo.CreateJob(c.Request.Context(), &job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save job"})
		return
	}
	c.JSON(http.StatusOK, job)
}

// ownedJob loads a job and verifies the requester owns it.
func (h *CareerHandler) ownedJob(c *gin.Context) (*domain.CareerJob, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job id"})
		return nil, false
	}
	job, err := h.repo.GetJobByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return nil, false
	}
	if job.UserID != currentUserID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this job"})
		return nil, false
	}
	return job, true
}

func (h *CareerHandler) UpdateMyJob(c *gin.Context) {
	existing, ok := h.ownedJob(c)
	if !ok {
		return
	}
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.UpdateJob(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job"})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// SetMyJobStatus updates just the status; body: { "status": "pending"|"applied"|"completed" }.
func (h *CareerHandler) SetMyJobStatus(c *gin.Context) {
	existing, ok := h.ownedJob(c)
	if !ok {
		return
	}
	var body struct {
		Status domain.CareerJobStatus `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.SetJobStatus(c.Request.Context(), existing.ID, body.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job status"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Job status updated"})
}

func (h *CareerHandler) DeleteMyJob(c *gin.Context) {
	existing, ok := h.ownedJob(c)
	if !ok {
		return
	}
	if err := h.repo.DeleteJob(c.Request.Context(), existing.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete job"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Job deleted"})
}

// ---- Self-service: Reminders (JWT) ----

func (h *CareerHandler) CreateMyReminder(c *gin.Context) {
	var body struct {
		JobID    *uuid.UUID `json:"job_id"`
		Title    string     `json:"title" binding:"required"`
		RemindAt time.Time  `json:"remind_at" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !body.RemindAt.After(time.Now()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "remind_at must be in the future"})
		return
	}
	userID := currentUserID(c)

	// If tied to a job, verify ownership before scheduling.
	if body.JobID != nil {
		job, err := h.repo.GetJobByID(c.Request.Context(), *body.JobID)
		if err != nil || job.UserID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this job"})
			return
		}
	}

	reminder := domain.CareerReminder{
		UserID:   userID,
		JobID:    body.JobID,
		Title:    body.Title,
		RemindAt: body.RemindAt,
		Status:   domain.CareerReminderPending,
	}
	if err := h.repo.CreateReminder(c.Request.Context(), &reminder); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create reminder"})
		return
	}
	c.JSON(http.StatusOK, reminder)
}

func (h *CareerHandler) GetMyReminders(c *gin.Context) {
	reminders, err := h.repo.GetMyReminders(c.Request.Context(), currentUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch reminders"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": reminders})
}

func (h *CareerHandler) CancelMyReminder(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reminder id"})
		return
	}
	reminder, err := h.repo.GetReminderByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Reminder not found"})
		return
	}
	if reminder.UserID != currentUserID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this reminder"})
		return
	}
	if err := h.repo.CancelReminder(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel reminder"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Reminder cancelled"})
}
