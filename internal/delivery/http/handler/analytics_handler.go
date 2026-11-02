package handler

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var analyticsCategoryPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

var acceptedAnalyticsEvents = map[string]bool{
	"resource_opened":           true,
	"resource_download_attempt": true,
	"resource_download_success": true,
	"resource_download_failed":  true,
	"resource_download_blocked": true,
	"search_performed":          true,
	"search_result_opened":      true,
}

var acceptedAnalyticsEntities = map[string]bool{
	"resource": true, "course": true, "notice": true, "club": true,
	"association": true, "teacher": true, "staff": true, "marketplace": true,
	"lost_found": true, "career": true,
}

var analyticsCleanupMu sync.Mutex
var lastAnalyticsCleanup time.Time

type AnalyticsHandler struct {
	db *gorm.DB
}

func NewAnalyticsHandler(db *gorm.DB) *AnalyticsHandler {
	return &AnalyticsHandler{db: db}
}

type analyticsEventRequest struct {
	ID          uuid.UUID  `json:"id"`
	EventType   string     `json:"event_type" binding:"required"`
	EntityType  string     `json:"entity_type"`
	EntityID    *uuid.UUID `json:"entity_id"`
	AttemptID   *uuid.UUID `json:"attempt_id"`
	Category    string     `json:"category"`
	ResultCount *int       `json:"result_count"`
	Platform    string     `json:"platform"`
}

// SubmitEvent accepts a small allowlisted event schema. Search text and
// client-supplied user IDs are intentionally not part of the request.
func (h *AnalyticsHandler) SubmitEvent(c *gin.Context) {
	role, _ := c.Get("user_role")
	if isAdmin, ok := role.(string); ok && (isAdmin == "admin" || isAdmin == "super_admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Analytics events are only accepted for user accounts"})
		return
	}

	var request analyticsEventRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid analytics event"})
		return
	}
	if !acceptedAnalyticsEvents[request.EventType] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported event_type"})
		return
	}
	if request.EntityType != "" && !acceptedAnalyticsEntities[request.EntityType] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported entity_type"})
		return
	}
	if request.EntityID != nil && request.EntityType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entity_type is required with entity_id"})
		return
	}
	if (request.EventType == "resource_opened" || request.EventType == "resource_download_attempt" ||
		request.EventType == "resource_download_success" || request.EventType == "resource_download_failed" ||
		request.EventType == "resource_download_blocked" || request.EventType == "search_result_opened") &&
		(request.EntityType == "" || request.EntityID == nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entity_type and entity_id are required for this event"})
		return
	}
	if (request.EventType == "resource_opened" || request.EventType == "resource_download_attempt" ||
		request.EventType == "resource_download_success" || request.EventType == "resource_download_failed" ||
		request.EventType == "resource_download_blocked") && request.EntityType != "resource" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Resource events require entity_type=resource"})
		return
	}
	if request.EventType == "search_performed" && (request.Category == "" || request.ResultCount == nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category and result_count are required for search_performed"})
		return
	}
	if request.Category != "" && !analyticsCategoryPattern.MatchString(request.Category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category"})
		return
	}
	if request.ResultCount != nil && (*request.ResultCount < 0 || *request.ResultCount > 10000) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid result_count"})
		return
	}
	if request.Platform != "" && request.Platform != "android" && request.Platform != "ios" && request.Platform != "web" && request.Platform != "other" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported platform"})
		return
	}

	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authenticated user is required"})
		return
	}
	actorID, ok := userID.(uuid.UUID)
	if !ok || actorID == uuid.Nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authenticated user is invalid"})
		return
	}

	event := domain.AnalyticsEvent{
		ID:          request.ID,
		UserID:      actorID,
		EventType:   request.EventType,
		EntityType:  request.EntityType,
		EntityID:    request.EntityID,
		AttemptID:   request.AttemptID,
		Category:    request.Category,
		ResultCount: request.ResultCount,
		Platform:    request.Platform,
		EventAt:     time.Now().UTC(),
	}
	var user domain.User
	if err := h.db.WithContext(c.Request.Context()).Select("id", "university_id", "department_id").First(&user, "id = ?", actorID).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authenticated user not found"})
		return
	}
	event.UniversityID = user.UniversityID
	event.DepartmentID = user.DepartmentID

	if err := h.db.WithContext(c.Request.Context()).Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record analytics event"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
}

type analyticsSummary struct {
	ActiveUsers          int64   `json:"active_users"`
	ResourceOpens        int64   `json:"resource_opens"`
	DownloadAttempts     int64   `json:"download_attempts"`
	DownloadSuccesses    int64   `json:"download_successes"`
	DownloadFailures     int64   `json:"download_failures"`
	DownloadBlocked      int64   `json:"download_blocked"`
	Searches             int64   `json:"searches"`
	ZeroResultSearches   int64   `json:"zero_result_searches"`
	SearchResultOpens    int64   `json:"search_result_opens"`
	NewUsers             int64   `json:"new_users"`
	UsersWithFirstAction int64   `json:"users_with_first_action"`
	CohortUsers          int64   `json:"cohort_users"`
	RetainedUsers        int64   `json:"retained_users"`
	RetentionRate        float64 `json:"retention_rate"`
}

type analyticsTrend struct {
	Date          string `json:"date"`
	ActiveUsers   int64  `json:"active_users"`
	ResourceOpens int64  `json:"resource_opens"`
	Downloads     int64  `json:"downloads"`
	Searches      int64  `json:"searches"`
}

type analyticsTopResource struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`
	Opens     int64     `json:"opens"`
	Downloads int64     `json:"downloads"`
}

type analyticsTopUser struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	Interactions int64     `json:"interactions"`
	Downloads    int64     `json:"downloads"`
}

type analyticsSearchCategory struct {
	Category    string `json:"category"`
	Searches    int64  `json:"searches"`
	ZeroResults int64  `json:"zero_results"`
	ResultOpens int64  `json:"result_opens"`
}

type analyticsDashboardResponse struct {
	Range            string                    `json:"range"`
	From             time.Time                 `json:"from"`
	Summary          analyticsSummary          `json:"summary"`
	Trends           []analyticsTrend          `json:"trends"`
	TopResources     []analyticsTopResource    `json:"top_resources"`
	TopUsers         []analyticsTopUser        `json:"top_users"`
	SearchCategories []analyticsSearchCategory `json:"search_categories"`
}

// Dashboard returns aggregated usage data for admin analytics. Raw activity
// rows are never exposed by this endpoint.
func (h *AnalyticsHandler) Dashboard(c *gin.Context) {
	rangeDays := map[string]int{"7d": 7, "30d": 30, "90d": 90}
	rangeKey := c.DefaultQuery("range", "30d")
	days, valid := rangeDays[rangeKey]
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "range must be 7d, 30d, or 90d"})
		return
	}

	var universityID, departmentID *uuid.UUID
	for name, target := range map[string]**uuid.UUID{
		"university_id": &universityID,
		"department_id": &departmentID,
	} {
		if raw := c.Query(name); raw != "" {
			parsed, err := uuid.Parse(raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid " + name})
				return
			}
			*target = &parsed
		}
	}

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)
	eventQuery := func() *gorm.DB {
		query := h.db.WithContext(c.Request.Context()).Model(&domain.AnalyticsEvent{}).
			Where("analytics_events.event_at >= ?", from)
		if universityID != nil {
			query = query.Where("analytics_events.university_id = ?", *universityID)
		}
		if departmentID != nil {
			query = query.Where("analytics_events.department_id = ?", *departmentID)
		}
		return query
	}

	response := analyticsDashboardResponse{
		Range: rangeKey, From: from,
		Trends:           []analyticsTrend{},
		TopResources:     []analyticsTopResource{},
		TopUsers:         []analyticsTopUser{},
		SearchCategories: []analyticsSearchCategory{},
	}
	counts := map[string]int64{}
	var eventCounts []struct {
		EventType string
		Count     int64
	}
	if err := eventQuery().Select("event_type, COUNT(*) AS count").Group("event_type").Scan(&eventCounts).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate analytics")
		return
	}
	for _, row := range eventCounts {
		counts[row.EventType] = row.Count
	}

	countDistinctUsers := func(query *gorm.DB) int64 {
		var count int64
		if err := query.Distinct("user_id").Count(&count).Error; err != nil {
			return -1
		}
		return count
	}
	activeQuery := eventQuery().Where("event_type = ?", "app_active")
	response.Summary.ActiveUsers = countDistinctUsers(activeQuery)
	if response.Summary.ActiveUsers < 0 {
		analyticsQueryError(c, "Failed to aggregate active users")
		return
	}
	response.Summary.ResourceOpens = counts["resource_opened"]
	response.Summary.DownloadAttempts = counts["resource_download_attempt"]
	response.Summary.DownloadSuccesses = counts["resource_download_success"]
	response.Summary.DownloadFailures = counts["resource_download_failed"]
	response.Summary.DownloadBlocked = counts["resource_download_blocked"]
	response.Summary.Searches = counts["search_performed"]
	response.Summary.SearchResultOpens = counts["search_result_opened"]

	var zeroResults int64
	if err := eventQuery().Where("event_type = ? AND result_count = 0", "search_performed").Count(&zeroResults).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate search analytics")
		return
	}
	response.Summary.ZeroResultSearches = zeroResults

	userQuery := h.db.WithContext(c.Request.Context()).Model(&domain.User{}).Where("created_at >= ?", from)
	if universityID != nil {
		userQuery = userQuery.Where("university_id = ?", *universityID)
	}
	if departmentID != nil {
		userQuery = userQuery.Where("department_id = ?", *departmentID)
	}
	if err := userQuery.Count(&response.Summary.NewUsers).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate new users")
		return
	}

	firstActionQuery := eventQuery().Joins("JOIN users ON users.id = analytics_events.user_id").
		Where("analytics_events.event_type IN ?", []string{"resource_opened", "resource_download_success"}).
		Where("users.created_at >= ?", from)
	if h.db.Dialector.Name() == "sqlite" {
		firstActionQuery = eventQuery().Joins("JOIN users ON users.id = analytics_events.user_id").
			Where("analytics_events.event_type IN ?", []string{"resource_opened", "resource_download_success"}).
			Where("users.created_at <= analytics_events.event_at").
			Where("users.created_at >= ?", from)
	} else {
		firstActionQuery = firstActionQuery.Where("users.created_at <= analytics_events.event_at")
	}
	firstActionQuery = firstActionQuery.Where(`NOT EXISTS (
		SELECT 1 FROM analytics_events earlier
		WHERE earlier.user_id = analytics_events.user_id
		AND earlier.event_type IN ('resource_opened', 'resource_download_success')
		AND earlier.event_at >= users.created_at
		AND earlier.event_at < analytics_events.event_at
	)`)
	response.Summary.UsersWithFirstAction = countDistinctUsers(firstActionQuery)
	if response.Summary.UsersWithFirstAction < 0 {
		analyticsQueryError(c, "Failed to aggregate onboarding funnel")
		return
	}

	cohortEnd := now.AddDate(0, 0, -7)
	cohortUsersQuery := h.db.WithContext(c.Request.Context()).Model(&domain.User{}).
		Where("created_at >= ? AND created_at < ?", from, cohortEnd)
	retainedQuery := h.db.WithContext(c.Request.Context()).Model(&domain.User{}).
		Where("created_at >= ? AND created_at < ?", from, cohortEnd).
		Where("EXISTS (SELECT 1 FROM analytics_events ae WHERE ae.user_id = users.id AND ae.event_type = ? AND ae.event_at > users.created_at + INTERVAL '6 days' AND ae.event_at <= users.created_at + INTERVAL '7 days')", "app_active")
	if h.db.Dialector.Name() == "sqlite" {
		retainedQuery = h.db.WithContext(c.Request.Context()).Model(&domain.User{}).
			Where("created_at >= ? AND created_at < ?", from, cohortEnd).
			Where("EXISTS (SELECT 1 FROM analytics_events ae WHERE ae.user_id = users.id AND ae.event_type = ? AND julianday(ae.event_at) > julianday(users.created_at) + 6 AND julianday(ae.event_at) <= julianday(users.created_at) + 7)", "app_active")
	}
	if universityID != nil {
		cohortUsersQuery = cohortUsersQuery.Where("university_id = ?", *universityID)
		retainedQuery = retainedQuery.Where("university_id = ?", *universityID)
	}
	if departmentID != nil {
		cohortUsersQuery = cohortUsersQuery.Where("department_id = ?", *departmentID)
		retainedQuery = retainedQuery.Where("department_id = ?", *departmentID)
	}
	if err := cohortUsersQuery.Count(&response.Summary.CohortUsers).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate retention cohorts")
		return
	}
	if err := retainedQuery.Count(&response.Summary.RetainedUsers).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate retention cohorts")
		return
	}
	if response.Summary.CohortUsers > 0 {
		response.Summary.RetentionRate = float64(response.Summary.RetainedUsers) / float64(response.Summary.CohortUsers) * 100
	}

	var trends []analyticsTrend
	if err := eventQuery().Select(`DATE(event_at) AS date,
		COUNT(DISTINCT CASE WHEN event_type = 'app_active' THEN user_id END) AS active_users,
		SUM(CASE WHEN event_type = 'resource_opened' THEN 1 ELSE 0 END) AS resource_opens,
		SUM(CASE WHEN event_type = 'resource_download_success' THEN 1 ELSE 0 END) AS downloads,
		SUM(CASE WHEN event_type = 'search_performed' THEN 1 ELSE 0 END) AS searches`).
		Group("DATE(event_at)").Order("DATE(event_at)").Scan(&trends).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate usage trends")
		return
	}
	response.Trends = trends

	type resourceCount struct {
		EntityID  uuid.UUID
		Opens     int64
		Downloads int64
	}
	var resourceCounts []resourceCount
	if err := eventQuery().Where("entity_type = ? AND entity_id IS NOT NULL AND event_type IN ?", "resource", []string{"resource_opened", "resource_download_success"}).
		Select(`entity_id,
			SUM(CASE WHEN event_type = 'resource_opened' THEN 1 ELSE 0 END) AS opens,
			SUM(CASE WHEN event_type = 'resource_download_success' THEN 1 ELSE 0 END) AS downloads`).
		Group("entity_id").Order("downloads DESC, opens DESC").Limit(10).Scan(&resourceCounts).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate resource usage")
		return
	}
	resourceIDs := make([]uuid.UUID, 0, len(resourceCounts))
	for _, row := range resourceCounts {
		resourceIDs = append(resourceIDs, row.EntityID)
	}
	var resources []domain.Resource
	if len(resourceIDs) > 0 {
		if err := h.db.WithContext(c.Request.Context()).Select("id", "title", "type").Where("id IN ?", resourceIDs).Find(&resources).Error; err != nil {
			analyticsQueryError(c, "Failed to load resource labels")
			return
		}
	}
	resourcesByID := make(map[uuid.UUID]domain.Resource, len(resources))
	for _, resource := range resources {
		resourcesByID[resource.ID] = resource
	}
	for _, row := range resourceCounts {
		resource := resourcesByID[row.EntityID]
		response.TopResources = append(response.TopResources, analyticsTopResource{
			ID: row.EntityID, Title: resource.Title, Type: string(resource.Type),
			Opens: row.Opens, Downloads: row.Downloads,
		})
	}

	type userCount struct {
		UserID       uuid.UUID
		Interactions int64
		Downloads    int64
	}
	var userCounts []userCount
	if err := eventQuery().Select(`user_id,
		SUM(CASE WHEN event_type IN ('resource_opened', 'search_performed', 'search_result_opened') THEN 1 ELSE 0 END) AS interactions,
		SUM(CASE WHEN event_type = 'resource_download_success' THEN 1 ELSE 0 END) AS downloads`).
		Group("user_id").Order("downloads DESC, interactions DESC").Limit(10).Scan(&userCounts).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate user usage")
		return
	}
	userIDs := make([]uuid.UUID, 0, len(userCounts))
	for _, row := range userCounts {
		userIDs = append(userIDs, row.UserID)
	}
	var users []domain.User
	if len(userIDs) > 0 {
		if err := h.db.WithContext(c.Request.Context()).Select("id", "first_name", "last_name", "email", "role").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
			analyticsQueryError(c, "Failed to load user labels")
			return
		}
	}
	usersByID := make(map[uuid.UUID]domain.User, len(users))
	for _, user := range users {
		usersByID[user.ID] = user
	}
	for _, row := range userCounts {
		user := usersByID[row.UserID]
		response.TopUsers = append(response.TopUsers, analyticsTopUser{
			ID: row.UserID, Name: user.FullName(), Email: user.Email, Role: string(user.Role),
			Interactions: row.Interactions, Downloads: row.Downloads,
		})
	}

	if err := eventQuery().Where("event_type = ?", "search_performed").
		Select(`category,
			COUNT(*) AS searches,
			SUM(CASE WHEN result_count = 0 THEN 1 ELSE 0 END) AS zero_results`).
		Group("category").Order("searches DESC").Limit(12).Scan(&response.SearchCategories).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate search categories")
		return
	}
	var categoryOpens []struct {
		Category string
		Count    int64
	}
	if err := eventQuery().Where("event_type = ?", "search_result_opened").
		Select("category, COUNT(*) AS count").Group("category").Scan(&categoryOpens).Error; err != nil {
		analyticsQueryError(c, "Failed to aggregate search result opens")
		return
	}
	opensByCategory := make(map[string]int64, len(categoryOpens))
	for _, row := range categoryOpens {
		opensByCategory[row.Category] = row.Count
	}
	for i := range response.SearchCategories {
		response.SearchCategories[i].ResultOpens = opensByCategory[response.SearchCategories[i].Category]
	}

	c.JSON(http.StatusOK, response)
}

func analyticsQueryError(c *gin.Context, message string) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": message})
}

// RecordDailyActive writes at most one app-active event per authenticated user
// per UTC calendar day. Heartbeats are retried periodically, so the unique key
// makes the write idempotent without storing device identifiers.
func RecordDailyActive(ctx context.Context, db *gorm.DB, userID uuid.UUID) error {
	now := time.Now().UTC()
	dedupeKey := fmt.Sprintf("app_active:%s:%s", userID, now.Format("2006-01-02"))
	if err := pruneAnalyticsEvents(ctx, db, now); err != nil {
		return err
	}
	var user domain.User
	if err := db.WithContext(ctx).Select("id", "university_id", "department_id").First(&user, "id = ?", userID).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&domain.AnalyticsEvent{
		UserID:       userID,
		EventType:    "app_active",
		DedupeKey:    &dedupeKey,
		UniversityID: user.UniversityID,
		DepartmentID: user.DepartmentID,
		EventAt:      now,
	}).Error
}

func pruneAnalyticsEvents(ctx context.Context, db *gorm.DB, now time.Time) error {
	analyticsCleanupMu.Lock()
	defer analyticsCleanupMu.Unlock()
	if !lastAnalyticsCleanup.IsZero() && now.Sub(lastAnalyticsCleanup) < 24*time.Hour {
		return nil
	}
	if err := db.WithContext(ctx).
		Where("event_at < ?", now.AddDate(0, 0, -90)).
		Delete(&domain.AnalyticsEvent{}).Error; err != nil {
		return fmt.Errorf("prune expired analytics events: %w", err)
	}
	lastAnalyticsCleanup = now
	return nil
}
