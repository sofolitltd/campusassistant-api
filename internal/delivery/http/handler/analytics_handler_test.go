package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func newAnalyticsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.User{}, &domain.Resource{}, &domain.AnalyticsEvent{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func analyticsRequestContext(t *testing.T, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestSubmitEventIsIdempotentAndUsesAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newAnalyticsTestDB(t)
	userID := uuid.New()
	if err := db.Create(&domain.User{
		Base:  domain.Base{ID: userID},
		Email: "student@example.test",
		Role:  domain.RoleStudent,
	}).Error; err != nil {
		t.Fatal(err)
	}
	eventID := uuid.New()
	handler := NewAnalyticsHandler(db)

	for range 2 {
		c, recorder := analyticsRequestContext(t, map[string]any{
			"id": eventID, "event_type": "search_performed", "category": "book",
			"result_count": 0, "query": "this value must not be stored",
			"user_id": uuid.New(),
		})
		c.Set("user_id", userID)
		c.Set("user_role", "student")
		handler.SubmitEvent(c)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("SubmitEvent status = %d, want %d: %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
		}
	}

	var events []domain.AnalyticsEvent
	if err := db.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("stored %d events, want 1", len(events))
	}
	if events[0].UserID != userID || events[0].Category != "book" || events[0].ResultCount == nil || *events[0].ResultCount != 0 {
		t.Fatalf("stored event does not match authenticated context and safe fields: %#v", events[0])
	}
}

func TestDashboardAggregatesRetentionUsageAndSearchQuality(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newAnalyticsTestDB(t)
	userID := uuid.New()
	createdAt := time.Now().UTC().AddDate(0, 0, -10)
	user := domain.User{
		Base:  domain.Base{ID: userID, CreatedAt: createdAt},
		Email: "student@example.test",
		Role:  domain.RoleStudent,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	resourceID := uuid.New()
	if err := db.Create(&domain.Resource{
		Base:  domain.Base{ID: resourceID},
		Title: "Study notes",
		Type:  domain.TypeNote,
	}).Error; err != nil {
		t.Fatal(err)
	}
	retainedAt := createdAt.Add(6*24*time.Hour + 12*time.Hour)
	searchCount := 0
	for _, event := range []domain.AnalyticsEvent{
		{UserID: userID, EventType: "app_active", EventAt: retainedAt},
		{UserID: userID, EventType: "resource_opened", EntityType: "resource", EntityID: &resourceID, EventAt: time.Now().UTC()},
		{UserID: userID, EventType: "resource_download_attempt", EntityType: "resource", EntityID: &resourceID, EventAt: time.Now().UTC()},
		{UserID: userID, EventType: "resource_download_success", EntityType: "resource", EntityID: &resourceID, EventAt: time.Now().UTC()},
		{UserID: userID, EventType: "search_performed", Category: "book", ResultCount: &searchCount, EventAt: time.Now().UTC()},
		{UserID: userID, EventType: "search_result_opened", Category: "book", EventAt: time.Now().UTC()},
	} {
		if err := db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?range=30d", nil)
	NewAnalyticsHandler(db).Dashboard(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("Dashboard status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response analyticsDashboardResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Summary.ActiveUsers != 1 || response.Summary.ResourceOpens != 1 ||
		response.Summary.DownloadAttempts != 1 || response.Summary.DownloadSuccesses != 1 ||
		response.Summary.Searches != 1 || response.Summary.ZeroResultSearches != 1 ||
		response.Summary.SearchResultOpens != 1 || response.Summary.CohortUsers != 1 ||
		response.Summary.RetainedUsers != 1 {
		t.Fatalf("unexpected analytics summary: %+v", response.Summary)
	}
	if len(response.TopResources) != 1 || response.TopResources[0].Title != "Study notes" || response.TopResources[0].Downloads != 1 {
		t.Fatalf("unexpected top resources: %+v", response.TopResources)
	}
	if len(response.SearchCategories) != 1 || response.SearchCategories[0].Category != "book" ||
		response.SearchCategories[0].ZeroResults != 1 || response.SearchCategories[0].ResultOpens != 1 {
		t.Fatalf("unexpected search categories: %+v", response.SearchCategories)
	}
	if err := RecordDailyActive(context.Background(), db, userID); err != nil {
		t.Fatal(err)
	}
	if err := RecordDailyActive(context.Background(), db, userID); err != nil {
		t.Fatal(err)
	}
	var activeCount int64
	if err := db.Model(&domain.AnalyticsEvent{}).Where("event_type = ?", "app_active").Count(&activeCount).Error; err != nil {
		t.Fatal(err)
	}
	if activeCount != 2 {
		t.Fatalf("stored %d app_active events, want historical event + one idempotent daily event", activeCount)
	}
}

func TestDashboardRejectsUnsupportedRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?range=365d", nil)
	NewAnalyticsHandler(newAnalyticsTestDB(t)).Dashboard(c)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("Dashboard status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
