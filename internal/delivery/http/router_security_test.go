package http

import (
	"fmt"
	netHTTP "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"campusassistant-api/internal/config"
	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/auth"
	applog "campusassistant-api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These tests build the real router and hit it over HTTP to prove the
// protection on money/entitlement routes. The DB is in-memory SQLite with
// only the tables the auth middleware and these handlers touch.

const testJWTSecret = "router-test-secret-router-test-secret"

type routerEnv struct {
	r       *gin.Engine
	jwt     *auth.JWTManager
	student domain.User
	other   domain.User
	adminID uuid.UUID
	gdb     *gorm.DB
}

func (e *routerEnv) db(t *testing.T) *gorm.DB {
	t.Helper()
	return e.gdb
}

func newRouterEnv(t *testing.T) *routerEnv {
	t.Helper()
	applog.InitLogger("test")
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&domain.User{}, &domain.Admin{}, &domain.Journal{}, &domain.LedgerEntry{},
		&domain.SubscriptionPlan{}, &domain.SubscriptionTarget{}, &domain.UserSubscription{}, &domain.PlanEntitlement{}, &domain.UsageCounter{}, &domain.Student{}, &domain.AccessGrant{}); err != nil {
		t.Fatal(err)
	}

	e := &routerEnv{adminID: uuid.New(), gdb: db}
	e.student = domain.User{Email: "s@test.dev", TokenVersion: 1}
	e.other = domain.User{Email: "o@test.dev", TokenVersion: 1}
	db.Create(&e.student)
	db.Create(&e.other)
	db.Create(&domain.Admin{Base: domain.Base{ID: e.adminID}, Email: "a@test.dev", Role: "admin", IsActive: true})

	cfg := &config.Config{
		Environment: "development", APIKey: "test-key", APIKeyRequired: true, JWTSecret: testJWTSecret,
		JWTAccessTokenExpiry: 60, JWTRefreshTokenExpiry: 168,
		RateLimitPerMinute: 1000, RateLimitAuthPerMinute: 3, RateLimitPayPerMinute: 1000,
	}
	e.r = NewRouter(cfg, db)
	e.jwt = auth.NewJWTManager(testJWTSecret, time.Hour, 24*time.Hour)
	return e
}

func (e *routerEnv) token(t *testing.T, id uuid.UUID, role string) string {
	t.Helper()
	tok, err := e.jwt.GenerateAccessToken(id, "x@test.dev", role, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *routerEnv) call(method, path, token string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func TestRouter_MoneyRoutesAreAdminOnly(t *testing.T) {
	e := newRouterEnv(t)
	student := e.token(t, e.student.ID, "student")
	admin := e.token(t, e.adminID, "admin")
	pid := uuid.NewString()

	routes := []struct{ method, path string }{
		{"POST", "/api/v1/subscriptions/admin-grant"}, // used to let any user grant themselves Pro
		{"POST", "/api/v1/subscriptions"},
		{"GET", "/api/v1/subscriptions"},
		{"GET", "/api/v1/coupons"},
		{"POST", "/api/v1/coupons"},
		{"PUT", "/api/v1/coupons/" + pid},
		{"GET", "/api/v1/billing/summary"},
		{"GET", "/api/v1/billing/ledger"},
		{"GET", "/api/v1/billing/payouts"},
		{"GET", "/api/v1/billing/payments"},
		{"GET", "/api/v1/billing/refunds"},
		{"POST", "/api/v1/billing/refunds/subscription"},
		{"POST", "/api/v1/billing/refunds/order"},
		{"PUT", "/api/v1/billing/payouts/" + pid + "/paid"},
		{"PUT", "/api/v1/billing/payouts/" + pid + "/reject"},
		{"GET", "/api/v1/subscription-plans/" + pid + "/entitlements"},
		{"PUT", "/api/v1/subscription-plans/" + pid + "/entitlements"},
		{"GET", "/api/v1/student-verifications"},
		{"GET", "/api/v1/student-verifications/" + pid},
		{"PUT", "/api/v1/student-verifications/" + pid + "/approve"},
		{"PUT", "/api/v1/student-verifications/" + pid + "/reject"},
	}
	for _, rt := range routes {
		if got := e.call(rt.method, rt.path, "").Code; got != 401 {
			t.Errorf("%s %s without a token = %d, want 401", rt.method, rt.path, got)
		}
		if got := e.call(rt.method, rt.path, student).Code; got != 403 {
			t.Errorf("%s %s as a student = %d, want 403", rt.method, rt.path, got)
		}
		if got := e.call(rt.method, rt.path, admin).Code; got == 401 || got == 403 {
			t.Errorf("%s %s as admin = %d, must be allowed through", rt.method, rt.path, got)
		}
	}
}

func TestRouter_StudentCannotGrantThemselvesPro(t *testing.T) {
	e := newRouterEnv(t)
	student := e.token(t, e.student.ID, "student")
	body := fmt.Sprintf(`{"user_id":"%s","plan_id":"%s"}`, e.student.ID, uuid.New())

	req := httptest.NewRequest("POST", "/api/v1/subscriptions/admin-grant", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Authorization", "Bearer "+student)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("admin-grant as student = %d (%s), want 403", w.Code, w.Body.String())
	}
}

func TestRouter_SubscriptionLookupIsSelfOrAdmin(t *testing.T) {
	e := newRouterEnv(t)
	student := e.token(t, e.student.ID, "student")
	admin := e.token(t, e.adminID, "admin")

	if got := e.call("GET", "/api/v1/subscriptions/user/"+e.other.ID.String(), student).Code; got != 403 {
		t.Errorf("reading another user's subscription = %d, want 403", got)
	}
	if got := e.call("GET", "/api/v1/subscriptions/user/"+e.student.ID.String(), student).Code; got != 200 {
		t.Errorf("reading own subscription = %d, want 200", got)
	}
	if got := e.call("GET", "/api/v1/subscriptions/user/"+e.other.ID.String(), admin).Code; got != 200 {
		t.Errorf("admin reading a subscription = %d, want 200", got)
	}
}

func TestRouter_AnyUserCanReadOwnEntitlements(t *testing.T) {
	e := newRouterEnv(t)
	w := e.call("GET", "/api/v1/my/entitlements", e.token(t, e.student.ID, "student"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"is_pro":false`) {
		t.Fatalf("entitlements = %d %s", w.Code, w.Body.String())
	}
}

func TestRouter_APIKeyStillRequiredWhenConfigured(t *testing.T) {
	e := newRouterEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/my/entitlements", nil)
	req.Header.Set("Authorization", "Bearer "+e.token(t, e.student.ID, "student"))
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("missing API key = %d, want 401", w.Code)
	}
}

func TestRouter_LoginIsRateLimitedPerIP(t *testing.T) {
	e := newRouterEnv(t) // auth limit = 3/min
	codes := []int{}
	for i := 0; i < 5; i++ {
		codes = append(codes, e.call("POST", "/api/v1/auth/login", "").Code)
	}
	if codes[3] != netHTTP.StatusTooManyRequests || codes[4] != netHTTP.StatusTooManyRequests {
		t.Fatalf("login responses = %v, want the 4th and 5th to be 429", codes)
	}
	if codes[0] == netHTTP.StatusTooManyRequests {
		t.Fatalf("first login attempt was rate limited: %v", codes)
	}
}

func TestRouter_CORSIsNoLongerAWildcard(t *testing.T) {
	e := newRouterEnv(t)
	evil := e.call("GET", "/health", "", "Origin", "https://evil.example")
	if got := evil.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO for evil origin = %q, want none", got)
	}
	local := e.call("GET", "/health", "", "Origin", "http://localhost:3000")
	if got := local.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("ACAO for localhost in dev = %q", got)
	}
}

func (e *routerEnv) callBody(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func TestRouter_ClaimProfileRequiresSignIn(t *testing.T) {
	e := newRouterEnv(t)
	for _, path := range []string{"/api/v1/students/claim-profile", "/api/v1/teachers/claim-profile", "/api/v1/staffs/claim-profile"} {
		body := fmt.Sprintf(`{"code":"123456","user_id":"%s"}`, e.other.ID)
		if got := e.callBody("POST", path, "", body).Code; got != 401 {
			t.Errorf("POST %s without a token = %d, want 401", path, got)
		}
	}
}

func TestRouter_ClaimProfileLinksTheTokenUserOnly(t *testing.T) {
	e := newRouterEnv(t)
	db := e.db(t)
	if err := db.AutoMigrate(&domain.Student{}); err != nil {
		t.Fatal(err)
	}
	batchID, uniID, deptID := uuid.New(), uuid.New(), uuid.New()
	profile := domain.Student{StudentID: "1001", BatchID: batchID, UniversityID: uniID, DepartmentID: deptID, SessionID: uuid.New(), VerificationCode: "654321"}
	db.Create(&profile)
	student := e.token(t, e.student.ID, "student")

	// A body user_id that isn't the caller is refused.
	w := e.callBody("POST", "/api/v1/students/claim-profile", student, fmt.Sprintf(`{"code":"654321","user_id":"%s"}`, e.other.ID))
	if w.Code != 403 {
		t.Fatalf("claim for another user = %d (%s), want 403", w.Code, w.Body.String())
	}

	// Batch/university in the body are ignored; the token user is linked.
	w = e.callBody("POST", "/api/v1/students/claim-profile", student, fmt.Sprintf(`{"code":"654321","batch_id":"%s"}`, uuid.New()))
	if w.Code != 200 {
		t.Fatalf("own claim = %d (%s), want 200", w.Code, w.Body.String())
	}
	var got domain.Student
	db.First(&got, "id = ?", profile.ID)
	if got.UserID == nil || *got.UserID != e.student.ID || !got.IsClaimed || got.VerificationCode != "" || got.BatchID != batchID {
		t.Fatalf("claimed profile = user %v claimed %v code %q batch %v", got.UserID, got.IsClaimed, got.VerificationCode, got.BatchID)
	}
	var user domain.User
	db.First(&user, "id = ?", e.student.ID)
	if !user.IsVerified || user.UniversityID == nil || *user.UniversityID != uniID {
		t.Fatalf("user after claim: verified %v university %v", user.IsVerified, user.UniversityID)
	}

	// The code is single use.
	if got := e.callBody("POST", "/api/v1/students/claim-profile", e.token(t, e.other.ID, "student"), `{"code":"654321"}`).Code; got != 404 {
		t.Errorf("reusing a claimed code = %d, want 404", got)
	}
}

func TestRouter_RegisterIgnoresRequestedRole(t *testing.T) {
	e := newRouterEnv(t)
	w := e.callBody("POST", "/api/v1/auth/register", "",
		`{"email":"new@test.dev","password":"password123","first_name":"N","last_name":"U","role":"university_admin"}`)
	if w.Code != 201 {
		t.Fatalf("register = %d (%s), want 201", w.Code, w.Body.String())
	}
	var user domain.User
	e.db(t).First(&user, "email = ?", "new@test.dev")
	if user.Role != domain.RoleStudent {
		t.Fatalf("registered role = %q, want student", user.Role)
	}
}

func TestRouter_ProxyOnlyFetchesFromStorageBucket(t *testing.T) {
	e := newRouterEnv(t) // no R2_PUBLIC_URL configured → nothing is allowed
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://localhost:8000/",                   // Coolify dashboard
		"https://example.com/",
		"file:///etc/passwd",
		"",
	} {
		w := e.call("GET", "/api/v1/proxy?url="+url.QueryEscape(target), "")
		if w.Code != netHTTP.StatusBadRequest {
			t.Errorf("proxy %q: got %d, want 400", target, w.Code)
		}
	}
}
