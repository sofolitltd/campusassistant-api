package http

import (
	"campusassistant-api/internal/config"
	"campusassistant-api/internal/delivery/http/handler"
	"campusassistant-api/internal/delivery/http/middleware"
	ws "campusassistant-api/internal/delivery/http/websocket"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/internal/service"
	"campusassistant-api/internal/usecase"
	"campusassistant-api/pkg/auth"
	"campusassistant-api/pkg/bkash"
	"campusassistant-api/pkg/fcm"
	"campusassistant-api/pkg/mailer"
	"campusassistant-api/pkg/storage"
	"context"
	"log"
	netHTTP "net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func NewRouter(cfg *config.Config, db *gorm.DB) *gin.Engine {
	// gin.New (not Default): Default already attaches Logger+Recovery, and
	// the old code then added them again, double-logging every request. The
	// access logger here omits query strings so WebSocket ?token= JWTs never
	// reach the logs.
	r := gin.New()
	r.MaxMultipartMemory = 10 << 20 // 10 MB limit for file uploads

	// Rate limiting keys on the client IP, which is only trustworthy if we
	// know which proxies set X-Forwarded-For.
	if proxies := config.SplitList(cfg.TrustedProxies); len(proxies) > 0 {
		if err := r.SetTrustedProxies(proxies); err != nil {
			log.Fatalf("invalid TRUSTED_PROXIES: %v", err)
		}
	} else if cfg.Environment != "development" {
		log.Printf("[security] TRUSTED_PROXIES is not set: X-Forwarded-For is trusted from any source, so per-IP rate limits can be evaded by spoofing it")
	}

	// Middlewares
	r.Use(middleware.AccessLogger())
	r.Use(gin.Recovery())
	corsOrigins := config.SplitList(cfg.CORSAllowedOrigins)
	if o := middleware.NormalizeOrigin(cfg.BkashCallbackBaseURL); o != "" {
		corsOrigins = append(corsOrigins, o) // the Flutter web app that bKash redirects back to
	}
	r.Use(middleware.CORSMiddleware(middleware.CORSConfig{
		AllowedOrigins: corsOrigins,
		AllowLocalhost: cfg.Environment == "development",
	}))

	// Rate limiters (0 disables). Global per-IP is generous because a whole
	// campus can share one NAT address; the strict ones guard credential
	// guessing and money movement.
	limiter := func(perMinute int, key func(*gin.Context) string) gin.HandlerFunc {
		if perMinute <= 0 {
			return middleware.NoopMiddleware()
		}
		return middleware.RateLimit(middleware.NewRateLimiter(perMinute, time.Minute), key)
	}
	r.Use(limiter(cfg.RateLimitPerMinute, middleware.ByIP))
	authLimit := limiter(cfg.RateLimitAuthPerMinute, middleware.ByIP)
	resetLimit := limiter(cfg.RateLimitAuthPerMinute/3, middleware.ByIP) // code guessing: tighter still
	payLimit := limiter(cfg.RateLimitPayPerMinute, middleware.ByUserOrIP)

	// Health Check (Public)
	r.GET("/health", func(c *gin.Context) {
		dbStatus := "connected"
		sqlDB, err := db.DB()
		if err != nil || sqlDB.Ping() != nil {
			dbStatus = "disconnected"
		}

		c.JSON(200, gin.H{
			"status":      "UP",
			"database":    dbStatus,
			"environment": cfg.Environment,
		})
	})

	// Initialize JWT Manager
	jwtManager := auth.NewJWTManager(
		cfg.JWTSecret,
		time.Duration(cfg.JWTAccessTokenExpiry)*time.Minute,
		time.Duration(cfg.JWTRefreshTokenExpiry)*time.Hour,
	)

	// Per-route access policies. Enforcing by default in development so a
	// misclassified route fails loudly here rather than in production; prod
	// opts in with ACCESS_ENFORCE=true after a log-only window.
	guard := middleware.NewGuard(jwtManager, db, cfg.AccessEnforce || cfg.Environment == "development")

	// API V1 Group
	v1 := r.Group("/api/v1")

	// Transactional email (password reset codes). Falls back to a logging
	// no-op mailer when SMTP_* is unset, so the server still boots locally.
	mail := mailer.New(cfg)

	// Public Auth Routes (No API Key or JWT required)
	adminRepo := postgres.NewAdminRepository(db)
	authHandler := handler.NewAuthHandler(db, jwtManager, cfg.JWTAccessTokenExpiry, adminRepo, mail)
	authGroup := v1.Group("/auth")
	{
		authGroup.POST("/register", authLimit, authHandler.Register)
		authGroup.POST("/login", authLimit, authHandler.Login)
		authGroup.POST("/admin-login", authLimit, authHandler.AdminLogin)
		authGroup.POST("/refresh", authLimit, authHandler.RefreshToken)
		// Password reset (public — the caller is by definition locked out)
		authGroup.POST("/forgot-password", resetLimit, authHandler.ForgotPassword)
		authGroup.POST("/verify-reset-code", resetLimit, authHandler.VerifyResetCode)
		authGroup.POST("/reset-password", resetLimit, authHandler.ResetPassword)
		// Protected routes - require JWT
		authGroup.GET("/me", middleware.JWTMiddleware(jwtManager, db), authHandler.GetMe)
		authGroup.POST("/change-password", middleware.JWTMiddleware(jwtManager, db), authHandler.ChangePassword)
	}

	// Public Proxy Route for local/emulator R2 image proxying
	v1.GET("/proxy", func(c *gin.Context) {
		targetURL := c.Query("url")
		if targetURL == "" {
			c.JSON(netHTTP.StatusBadRequest, gin.H{"error": "url is required"})
			return
		}

		resp, err := netHTTP.Get(targetURL)
		if err != nil {
			c.JSON(netHTTP.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()

		c.DataFromReader(resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
	})

	// The API key is a shared secret shipped inside the mobile app, so it only
	// identifies a client; real protection is the per-route JWT policy plus
	// rate limits. It stays mandatory by default (older app builds send it);
	// API_KEY_REQUIRED=false drops the requirement once the app stops
	// shipping it. main.go refuses that setting unless route policies are
	// enforced.
	if cfg.APIKeyRequired {
		v1.Use(middleware.APIKeyMiddleware(cfg.APIKey))
	}

	// Admin management (requires JWT)
	adminHandler := handler.NewAdminHandler(adminRepo)
	adminsGroup := v1.Group("/admins")
	adminsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		adminsGroup.GET("", adminHandler.List)
		adminsGroup.POST("", adminHandler.Create)
		adminsGroup.PUT("/:id", adminHandler.Update)
		adminsGroup.PUT("/:id/password", adminHandler.ChangePassword)
		adminsGroup.DELETE("/:id", adminHandler.Delete)
	}

	// Helper to register generic routes
	registerRoutes[domain.University](v1, db, "universities", guard, middleware.Access{Read: middleware.Public, Write: middleware.AdminJWT})
	registerRoutes[domain.Faculty](v1, db, "faculties", guard, middleware.Access{Read: middleware.Public, Write: middleware.AdminJWT})
	registerRoutes[domain.Department](v1, db, "departments", guard, middleware.Access{Read: middleware.Public, Write: middleware.AdminJWT})
	registerRoutes[domain.Session](v1, db, "sessions", guard, middleware.Access{Read: middleware.Public, Write: middleware.AdminJWT})
	registerRoutes[domain.Batch](v1, db, "batches", guard, middleware.Access{Read: middleware.Public, Write: middleware.AdminJWT})
	registerRoutes[domain.User](v1, db, "users", guard, middleware.Access{Read: middleware.AdminJWT, Write: middleware.AdminJWT})
	registerRoutes[domain.Notice](v1, db, "notices", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})
	registerRoutes[domain.Contributor](v1, db, "contributors", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	// Notice engagement (like/view/comment) — requires JWT to attribute actions to a user.
	noticeEngagementHandler := handler.NewNoticeEngagementHandler(db)
	noticeEngagementGroup := v1.Group("/notices")
	noticeEngagementGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		noticeEngagementGroup.GET("/liked-ids", noticeEngagementHandler.GetLikedNoticeIDs)
		noticeEngagementGroup.POST("/:id/like", noticeEngagementHandler.LikeNotice)
		noticeEngagementGroup.POST("/:id/unlike", noticeEngagementHandler.UnlikeNotice)
		noticeEngagementGroup.POST("/:id/view", noticeEngagementHandler.ViewNotice)
		noticeEngagementGroup.GET("/:id/comments", noticeEngagementHandler.GetComments)
		noticeEngagementGroup.POST("/:id/comments", noticeEngagementHandler.AddComment)
		noticeEngagementGroup.DELETE("/comments/:comment_id", noticeEngagementHandler.DeleteComment)
	}

	// Specialized Student Routes
	studentRepo := postgres.NewGormRepositoryWithOrder[domain.Student](db, "weight ASC, LEFT(student_id, 2) DESC, student_id ASC")
	studentUsecase := usecase.NewGenericUsecase(studentRepo)
	studentHandler := handler.NewStudentHandler(studentUsecase)
	studentGroup := v1.Group("/students")
	{
		studentGroup.POST("", guard.Require(middleware.AdminJWT), studentHandler.Create)
		studentGroup.POST("/verify-code", guard.Require(middleware.Public), studentHandler.VerifyCode)
		studentGroup.POST("/claim-profile", guard.Require(middleware.Public), studentHandler.ClaimProfile)
		studentGroup.GET("", guard.Require(middleware.AuthJWT), studentHandler.GetAll)
		studentGroup.GET("/:id", guard.Require(middleware.AuthJWT), studentHandler.GetByID)
		studentGroup.PUT("/:id", guard.Require(middleware.AdminJWT), studentHandler.Update)
		studentGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), studentHandler.Delete)
	}

	// Self-service profile routes — JWT gated, resolve "self" from the
	// token's user_id rather than a client-supplied ID, unlike the plain
	// /users and /students CRUD above (API-key gated only, no ownership
	// check). This is what EditProfilePage actually saves to.
	userRepo := postgres.NewGormRepository[domain.User](db)
	userUsecase := usecase.NewGenericUsecase(userRepo)
	userHandler := handler.NewUserHandler(userUsecase)
	myUserGroup := v1.Group("/my/user")
	myUserGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myUserGroup.PUT("", userHandler.UpdateMyUser)
	}

	myStudentGroup := v1.Group("/my/student")
	myStudentGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myStudentGroup.GET("", studentHandler.GetMyStudent)
		myStudentGroup.PUT("", studentHandler.UpdateMyStudent)
		myStudentGroup.PUT("/address", studentHandler.UpdateMyAddress)
	}

	teacherRepo := postgres.NewGormRepositoryWithOrder[domain.Teacher](db, "weight ASC, name ASC")
	teacherUsecase := usecase.NewGenericUsecase(teacherRepo)
	teacherHandler := handler.NewGenericHandler(teacherUsecase)
	teacherGroup := v1.Group("/teachers")
	{
		teacherGroup.POST("", guard.Require(middleware.AdminJWT), teacherHandler.Create)
		teacherGroup.GET("", guard.Require(middleware.AuthJWT), teacherHandler.GetAll)
		teacherGroup.GET("/:id", guard.Require(middleware.AuthJWT), teacherHandler.GetByID)
		teacherGroup.PUT("/:id", guard.Require(middleware.AdminJWT), teacherHandler.Update)
		teacherGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), teacherHandler.Delete)
	}
	registerRoutes[domain.Staff](v1, db, "staffs", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})
	crRepo := postgres.NewGormRepository[domain.CR](db)
	crUsecase := usecase.NewGenericUsecase(crRepo)
	crHandler := handler.NewCrHandler(crUsecase)
	crGroup := v1.Group("/crs")
	{
		crGroup.POST("", guard.Require(middleware.AdminJWT), crHandler.Create)
		crGroup.GET("", guard.Require(middleware.AuthJWT), crHandler.GetAll)
		crGroup.GET("/:id", guard.Require(middleware.AuthJWT), crHandler.GetByID)
		crGroup.PUT("/:id", guard.Require(middleware.AdminJWT), crHandler.Update)
		crGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), crHandler.Delete)
	}
	registerRoutes[domain.Verification](v1, db, "verifications", guard, middleware.Access{Read: middleware.AdminJWT, Write: middleware.AdminJWT})

	fcmClient, err := fcm.NewClient(context.Background(), cfg)
	if err != nil {
		log.Printf("[fcm] push notifications disabled: %v", err)
		fcmClient = nil
	}
	notificationService := service.NewNotificationService(db, fcmClient)
	deviceTopicService := service.NewDeviceTopicService(db, fcmClient)

	r2Storage, r2Err := storage.NewR2Storage(cfg)
	resourceRepo := postgres.NewResourceRepository(db)
	resourceUsecase := usecase.NewGenericUsecase(resourceRepo)
	var r2 *storage.R2Storage
	if r2Err == nil {
		r2 = r2Storage
	}
	resourceHandler := handler.NewResourceHandler(resourceUsecase, r2, db, notificationService)
	rg := v1.Group("/resources")
	{
		rg.POST("", guard.Require(middleware.AuthJWT), resourceHandler.Create)
		rg.GET("", guard.Require(middleware.AuthJWT), resourceHandler.GetAll)
		rg.GET("/:id", guard.Require(middleware.AuthJWT), resourceHandler.GetByID)
		rg.PUT("/:id", guard.Require(middleware.AuthJWT), resourceHandler.Update)
		rg.DELETE("/:id", guard.Require(middleware.AuthJWT), resourceHandler.Delete)
		// Review workflow
		rg.PATCH("/:id/approve", guard.Require(middleware.AdminJWT), resourceHandler.ApproveResource)
		rg.PATCH("/:id/reject", guard.Require(middleware.AdminJWT), resourceHandler.RejectResource)
		// Engagement
		rg.POST("/:id/download", guard.Require(middleware.AuthJWT), resourceHandler.IncrementDownload)
		rg.POST("/:id/view", guard.Require(middleware.AuthJWT), resourceHandler.IncrementView)
		rg.POST("/:id/rate", guard.Require(middleware.AuthJWT), resourceHandler.RateResource)
		rg.GET("/:id/rating", guard.Require(middleware.AuthJWT), resourceHandler.GetMyRating)
	}

	registerRoutes[domain.Transport](v1, db, "transports", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})
	registerRoutes[domain.Attachment](v1, db, "attachments", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	levelRepo := postgres.NewLevelRepository(db)
	levelUsecase := usecase.NewGenericUsecase[domain.Level](levelRepo)
	levelHandler := handler.NewGenericHandler[domain.Level](levelUsecase)
	lg := v1.Group("/levels")
	{
		lg.POST("", guard.Require(middleware.AdminJWT), levelHandler.Create)
		lg.GET("", guard.Require(middleware.AuthJWT), levelHandler.GetAll)
		lg.GET("/:id", guard.Require(middleware.AuthJWT), levelHandler.GetByID)
		lg.PUT("/:id", guard.Require(middleware.AdminJWT), levelHandler.Update)
		lg.DELETE("/:id", guard.Require(middleware.AdminJWT), levelHandler.Delete)
	}

	registerRoutes[domain.Hall](v1, db, "halls", guard, middleware.Access{Read: middleware.Public, Write: middleware.AdminJWT})
	registerRoutes[domain.Organization](v1, db, "organizations", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})
	registerRoutes[domain.Alumni](v1, db, "alumni", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})
	bookmarkRepo := postgres.NewGormRepository[domain.Bookmark](db)
	bookmarkUc := usecase.NewGenericUsecase(bookmarkRepo)
	bookmarkHandler := handler.NewGenericHandler[domain.Bookmark](bookmarkUc)
	bg := v1.Group("/bookmarks")
	{
		bg.POST("", guard.Require(middleware.AuthJWT), bookmarkHandler.Create)
		bg.GET("", guard.Require(middleware.AuthJWT), bookmarkHandler.GetAll)
		bg.GET("/:id", guard.Require(middleware.AuthJWT), bookmarkHandler.GetByID)
		bg.PUT("/:id", guard.Require(middleware.AuthJWT), bookmarkHandler.Update)
		bg.DELETE("/:id", guard.Require(middleware.AuthJWT), bookmarkHandler.HardDelete)
	}
	registerRoutes[domain.Routine](v1, db, "routines", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	courseRepo := postgres.NewCourseRepository(db)
	courseUsecase := usecase.NewGenericUsecase[domain.Course](courseRepo)
	courseHandler := handler.NewGenericHandler[domain.Course](courseUsecase)
	cg := v1.Group("/courses")
	{
		cg.POST("", guard.Require(middleware.AdminJWT), courseHandler.Create)
		cg.GET("", guard.Require(middleware.AuthJWT), courseHandler.GetAll)
		cg.GET("/:id", guard.Require(middleware.AuthJWT), courseHandler.GetByID)
		cg.PUT("/:id", guard.Require(middleware.AdminJWT), courseHandler.Update)
		cg.DELETE("/:id", guard.Require(middleware.AdminJWT), courseHandler.Delete)
	}

	registerRoutes[domain.CourseCategory](v1, db, "course-categories", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})
	registerRoutes[domain.CoursePrefix](v1, db, "course-prefixes", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})
	chapterRepo := postgres.NewChapterRepository(db)
	chapterUsecase := usecase.NewGenericUsecase(chapterRepo)
	chapterHandler := handler.NewGenericHandler(chapterUsecase)
	chg := v1.Group("/chapters")
	{
		chg.POST("", guard.Require(middleware.AdminJWT), chapterHandler.Create)
		chg.GET("", guard.Require(middleware.AuthJWT), chapterHandler.GetAll)
		chg.GET("/:id", guard.Require(middleware.AuthJWT), chapterHandler.GetByID)
		chg.PUT("/:id", guard.Require(middleware.AdminJWT), chapterHandler.Update)
		chg.DELETE("/:id", guard.Require(middleware.AdminJWT), chapterHandler.Delete)
	}

	// Specialized Banner Routes
	bannerRepo := postgres.NewBannerRepository(db)
	bannerUsecase := usecase.NewGenericUsecase[domain.Banner](bannerRepo)
	bannerHandler := handler.NewGenericHandler[domain.Banner](bannerUsecase)
	bannerGroup := v1.Group("/banners")
	{
		bannerGroup.POST("", guard.Require(middleware.AdminJWT), bannerHandler.Create)
		bannerGroup.GET("", guard.Require(middleware.AuthJWT), bannerHandler.GetAll)
		bannerGroup.GET("/:id", guard.Require(middleware.AuthJWT), bannerHandler.GetByID)
		bannerGroup.PUT("/:id", guard.Require(middleware.AdminJWT), bannerHandler.Update)
		bannerGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), bannerHandler.Delete)
	}

	// Dashboard Stats
	statsRepo := postgres.NewStatsRepository(db)
	statsHandler := handler.NewStatsHandler(statsRepo)
	v1.GET("/stats", guard.Require(middleware.AdminJWT), statsHandler.GetDashboardStats)

	// Rewards: coin-based system for resource access. Users earn coins by
	// watching ads, spend coins to access resources by file size.
	rewardRepo := postgres.NewRewardRepository(db)
	rewardHandler := handler.NewRewardHandler(rewardRepo, db)
	rewardGroup := v1.Group("/rewards")
	rewardGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		rewardGroup.GET("/balance", rewardHandler.GetBalance)
		rewardGroup.POST("/earn", rewardHandler.Earn)
		rewardGroup.POST("/spend/:resourceId", rewardHandler.Spend)
		rewardGroup.GET("/transactions", rewardHandler.GetTransactions)
	}
	v1.GET("/rewards/cost/:resourceId", guard.Require(middleware.AuthJWT), rewardHandler.GetCost)

	// Club: dedicated repo/handler (not generic CRUD) because of the
	// denormalized follower count and JWT-scoped follow/suggest actions.
	clubRepo := postgres.NewClubRepository(db)
	clubHandler := handler.NewClubHandler(clubRepo)
	clubGroup := v1.Group("/clubs")
	{
		clubGroup.GET("", guard.Require(middleware.AuthJWT), clubHandler.GetAllClubs)
		clubGroup.POST("", guard.Require(middleware.AdminJWT), clubHandler.CreateClub)
		clubGroup.GET("/:id", guard.Require(middleware.AuthJWT), clubHandler.GetClubByID)
		clubGroup.PUT("/:id", guard.Require(middleware.AdminJWT), clubHandler.UpdateClub)
		clubGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), clubHandler.DeleteClub)
	}
	v1.GET("/clubs-by-location", guard.Require(middleware.Public), clubHandler.GetPublicClubs)
	v1.GET("/clubs/:id/members", guard.Require(middleware.Public), clubHandler.GetPublicClubMembers)

	clubAuthGroup := v1.Group("/clubs")
	clubAuthGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		clubAuthGroup.POST("/:id/follow", clubHandler.FollowClub)
		clubAuthGroup.DELETE("/:id/follow", clubHandler.UnfollowClub)
		clubAuthGroup.POST("/:id/join", clubHandler.JoinClub)
		clubAuthGroup.DELETE("/:id/join", clubHandler.LeaveClub)
		clubAuthGroup.POST("/suggest", clubHandler.SuggestClub)
	}

	// Club events: flat resource filtered by club_id (mirrors skill-videos),
	// plus a published-only "/clubs/:id/events" listing for the app.
	clubEventHandler := handler.NewClubEventHandler(db, notificationService)
	v1.GET("/clubs/:id/events", guard.Require(middleware.Public), clubEventHandler.GetPublicClubEvents)
	clubEventGroup := v1.Group("/club-events")
	{
		clubEventGroup.GET("", guard.Require(middleware.AuthJWT), clubEventHandler.GetAllClubEvents)
		clubEventGroup.POST("", guard.Require(middleware.AdminJWT), clubEventHandler.CreateClubEvent)
		clubEventGroup.PUT("/:id", guard.Require(middleware.AdminJWT), clubEventHandler.UpdateClubEvent)
		clubEventGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), clubEventHandler.DeleteClubEvent)
	}

	// Club self-service management: JWT-gated "/my/clubs/*" surface for a
	// club's own requester/officers (ClubManager role), fully separate from
	// the admin panel's API-key-gated /clubs CRUD above. See club.go's
	// SuggestClub, which seeds the requester as "owner" at request time.
	clubManagementRepo := postgres.NewClubManagementRepository(db)
	clubManageHandler := handler.NewClubManageHandler(clubManagementRepo, clubRepo, db, notificationService)
	v1.GET("/clubs/:id/posts", guard.Require(middleware.Public), clubManageHandler.GetPublicClubPosts)
	myClubsGroup := v1.Group("/my/clubs")
	myClubsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myClubsGroup.GET("", clubManageHandler.GetMyClubs)
		myClubsGroup.PUT("/:id", clubManageHandler.UpdateMyClub)
		myClubsGroup.GET("/:id/followers", clubManageHandler.GetFollowersForPromotion)
		myClubsGroup.GET("/:id/managers", clubManageHandler.GetManagers)
		myClubsGroup.POST("/:id/managers", clubManageHandler.PromoteManager)
		myClubsGroup.DELETE("/:id/managers/:userId", clubManageHandler.RemoveManager)
		myClubsGroup.POST("/:id/events", clubManageHandler.CreateMyClubEvent)
		myClubsGroup.POST("/:id/posts", clubManageHandler.CreateClubPost)
	}

	// BD district/upazila static reference data — shared source for the
	// admin panel and the mobile app's Association pickers.
	bdLocationHandler := handler.NewBDLocationHandler()
	v1.GET("/bd-districts", guard.Require(middleware.Public), bdLocationHandler.GetAll)

	// Association: district/sub-district scoped counterpart to Club. Same
	// dedicated-repo/handler shape as Club, for the same reasons.
	associationRepo := postgres.NewAssociationRepository(db)
	associationHandler := handler.NewAssociationHandler(associationRepo)
	associationGroup := v1.Group("/associations")
	{
		associationGroup.GET("", guard.Require(middleware.AuthJWT), associationHandler.GetAllAssociations)
		associationGroup.POST("", guard.Require(middleware.AdminJWT), associationHandler.CreateAssociation)
		associationGroup.GET("/:id", guard.Require(middleware.AuthJWT), associationHandler.GetAssociationByID)
		associationGroup.PUT("/:id", guard.Require(middleware.AdminJWT), associationHandler.UpdateAssociation)
		associationGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), associationHandler.DeleteAssociation)
		associationGroup.GET("/:id/members/pending", guard.Require(middleware.AuthJWT), associationHandler.GetPendingAssociationMembers)
		associationGroup.POST("/:id/members/:userId/approve", guard.Require(middleware.AdminJWT), associationHandler.ApproveAssociationMember)
		associationGroup.POST("/:id/members/:userId/reject", guard.Require(middleware.AdminJWT), associationHandler.RejectAssociationMember)
	}
	v1.GET("/associations-by-location", guard.Require(middleware.Public), associationHandler.GetPublicAssociations)
	v1.GET("/associations/:id/members", guard.Require(middleware.Public), associationHandler.GetPublicAssociationMembers)

	associationAuthGroup := v1.Group("/associations")
	associationAuthGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		associationAuthGroup.POST("/:id/follow", associationHandler.FollowAssociation)
		associationAuthGroup.DELETE("/:id/follow", associationHandler.UnfollowAssociation)
		associationAuthGroup.POST("/:id/join", associationHandler.JoinAssociation)
		associationAuthGroup.DELETE("/:id/join", associationHandler.LeaveAssociation)
		associationAuthGroup.POST("/suggest", associationHandler.SuggestAssociation)
	}

	associationEventHandler := handler.NewAssociationEventHandler(db, notificationService)
	v1.GET("/associations/:id/events", guard.Require(middleware.Public), associationEventHandler.GetPublicAssociationEvents)
	associationEventGroup := v1.Group("/association-events")
	{
		associationEventGroup.GET("", guard.Require(middleware.AuthJWT), associationEventHandler.GetAllAssociationEvents)
		associationEventGroup.POST("", guard.Require(middleware.AdminJWT), associationEventHandler.CreateAssociationEvent)
		associationEventGroup.PUT("/:id", guard.Require(middleware.AdminJWT), associationEventHandler.UpdateAssociationEvent)
		associationEventGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), associationEventHandler.DeleteAssociationEvent)
	}

	// Association self-service management: JWT-gated "/my/associations/*"
	// surface, mirrors Club's "/my/clubs/*" split from the admin panel's
	// API-key-gated /associations CRUD above.
	associationManagementRepo := postgres.NewAssociationManagementRepository(db)
	associationManageHandler := handler.NewAssociationManageHandler(associationManagementRepo, associationRepo, db, notificationService)
	v1.GET("/associations/:id/posts", guard.Require(middleware.Public), associationManageHandler.GetPublicAssociationPosts)
	myAssociationsGroup := v1.Group("/my/associations")
	myAssociationsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myAssociationsGroup.GET("", associationManageHandler.GetMyAssociations)
		myAssociationsGroup.GET("/joined", associationHandler.GetJoinedAssociations)
		myAssociationsGroup.PUT("/:id", associationManageHandler.UpdateMyAssociation)
		myAssociationsGroup.GET("/:id/followers", associationManageHandler.GetFollowersForPromotion)
		myAssociationsGroup.GET("/:id/managers", associationManageHandler.GetManagers)
		myAssociationsGroup.POST("/:id/managers", associationManageHandler.PromoteManager)
		myAssociationsGroup.DELETE("/:id/managers/:userId", associationManageHandler.RemoveManager)
		myAssociationsGroup.POST("/:id/events", associationManageHandler.CreateMyAssociationEvent)
		myAssociationsGroup.POST("/:id/posts", associationManageHandler.CreateAssociationPost)
	}

	registerRoutes[domain.EmergencyContact](v1, db, "emergency-contacts", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	// R2 Upload Routes
	if r2 != nil {
		uploadHandler := handler.NewUploadHandler(db, r2)
		v1.POST("/upload", guard.Require(middleware.AuthJWT), uploadHandler.UploadImage)
		v1.DELETE("/upload", guard.Require(middleware.AuthJWT), uploadHandler.DeleteFile)
		r.GET("/upload", uploadHandler.ShowUploadPage) // Serving the demo page at root /upload

		// Authenticated upload surface: ownership-tracked, and forces
		// sensitive folders (see sensitiveUploadFolders) to private storage
		// resolved only via a short-lived presigned URL — never a
		// permanent public link. The legacy /upload above stays untouched
		// since the admin panel has no JWT/login concept to satisfy it.
		myUploadGroup := v1.Group("/my/upload")
		myUploadGroup.Use(middleware.JWTMiddleware(jwtManager, db))
		{
			myUploadGroup.POST("", uploadHandler.MyUploadImage)
			myUploadGroup.GET("/:id", uploadHandler.MyGetUploadURL)
		}
		// Admin resolver for viewing any attachment (e.g. a merchant's
		// verification documents) — no ownership check, API-key gated only
		// (same trust level as the rest of the admin panel).
		v1.GET("/attachments/:id/url", guard.Require(middleware.AdminJWT), uploadHandler.AdminGetAttachmentURL)
	}

	// Skill Routes ("Skill Up" home section) — dedicated (not generic CRUD)
	// because Targets need explicit delete-then-save handling on update,
	// same reasoning as SubscriptionPlan. GetSkillsByLocation is registered
	// at a distinct top-level path (not nested under /skills/:id) to avoid
	// any ambiguity between a literal path segment and the :id wildcard.
	skillRepo := postgres.NewSkillRepository(db)
	skillHandler := handler.NewSkillHandler(skillRepo)
	v1.GET("/skills-by-location", guard.Require(middleware.Public), skillHandler.GetSkillsByLocation)
	skillGroup := v1.Group("/skills")
	{
		skillGroup.GET("", guard.Require(middleware.AuthJWT), skillHandler.GetAllSkills)
		skillGroup.POST("", guard.Require(middleware.AdminJWT), skillHandler.CreateSkill)
		skillGroup.GET("/:id", guard.Require(middleware.AuthJWT), skillHandler.GetSkillByID)
		skillGroup.PUT("/:id", guard.Require(middleware.AdminJWT), skillHandler.UpdateSkill)
		skillGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), skillHandler.DeleteSkill)
	}
	registerRoutes[domain.SkillVideo](v1, db, "skill-videos", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	// Campus Marketplace: Merchant + Product routes. Dedicated (not generic
	// CRUD) for the same reasons as Skill — Targets need explicit
	// delete-then-save handling, and merchant approval/ownership are custom
	// state transitions/checks, not plain field updates.
	merchantRepo := postgres.NewMerchantRepository(db)
	productRepo := postgres.NewProductRepository(db)
	merchantHandler := handler.NewMerchantHandler(db, merchantRepo, productRepo, notificationService)
	v1.GET("/merchants/platform", guard.Require(middleware.Public), merchantHandler.GetPlatformMerchant)
	merchantGroup := v1.Group("/merchants")
	{
		merchantGroup.GET("", guard.Require(middleware.AuthJWT), merchantHandler.GetAllMerchants)
		merchantGroup.GET("/:id", guard.Require(middleware.AuthJWT), merchantHandler.GetMerchantByID)
		merchantGroup.PUT("/:id", guard.Require(middleware.AdminJWT), merchantHandler.UpdateMerchant)
		merchantGroup.PUT("/:id/approve", guard.Require(middleware.AdminJWT), merchantHandler.ApproveMerchant)
		merchantGroup.PUT("/:id/reject", guard.Require(middleware.AdminJWT), merchantHandler.RejectMerchant)
		merchantGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), merchantHandler.DeleteMerchant)
	}

	productHandler := handler.NewProductHandler(productRepo, merchantRepo)
	wishlistSvc := service.NewWishlistService(db, notificationService)
	productHandler.SetWishlist(wishlistSvc)
	wishlistHandler := handler.NewWishlistHandler(wishlistSvc, productRepo)
	v1.GET("/products-lookup", guard.Require(middleware.Public), wishlistHandler.Lookup)
	myWishlistGroup := v1.Group("/my/wishlist")
	myWishlistGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myWishlistGroup.GET("", wishlistHandler.List)
		myWishlistGroup.GET("/ids", wishlistHandler.IDs)
		myWishlistGroup.PUT("/:id", wishlistHandler.Add)
		myWishlistGroup.DELETE("/:id", wishlistHandler.Remove)
	}
	v1.GET("/products-by-location", guard.Require(middleware.Public), productHandler.GetProductsByLocation)
	productGroup := v1.Group("/products")
	{
		productGroup.GET("", guard.Require(middleware.AuthJWT), productHandler.GetAllProducts)
		productGroup.POST("", guard.Require(middleware.AdminJWT), productHandler.CreateProduct)
		productGroup.GET("/:id", guard.Require(middleware.AuthJWT), productHandler.GetProductByID)
		productGroup.PUT("/:id", guard.Require(middleware.AdminJWT), productHandler.UpdateProduct)
		productGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), productHandler.DeleteProduct)
	}

	// Marketplace Categories — simple generic CRUD, same pattern as CourseCategory.
	registerRoutes[domain.MarketplaceCategory](v1, db, "marketplace-categories", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	// Merchant self-service surface: JWT-gated "/my/*" routes for a merchant
	// managing their own application/products, mirroring the "/my/clubs"
	// split from the admin panel's API-key-gated /clubs and /products CRUD.
	myMerchantGroup := v1.Group("/my/merchant")
	myMerchantGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myMerchantGroup.POST("/apply", merchantHandler.ApplyForMerchant)
		myMerchantGroup.GET("", merchantHandler.GetMyMerchant)
	}
	myMerchantsGroup := v1.Group("/my/merchants")
	myMerchantsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myMerchantsGroup.GET("", merchantHandler.GetMyMerchants)
		myMerchantsGroup.PUT("/:id", merchantHandler.UpdateMyMerchant)
		myMerchantsGroup.DELETE("/:id", merchantHandler.DeleteMyMerchant)
		// Scoped by merchant :id (not resolved from the JWT user alone) since
		// a user can own several merchants — see ProductHandler.resolveOwnedMerchant.
		myMerchantsGroup.GET("/:id/products", productHandler.GetMyProducts)
		myMerchantsGroup.POST("/:id/products", productHandler.CreateMyProduct)
		myMerchantsGroup.PUT("/:id/products/:productId", productHandler.UpdateMyProduct)
		myMerchantsGroup.DELETE("/:id/products/:productId", productHandler.DeleteMyProduct)
	}

	// Address book — user-managed shipping addresses (JWT gated, ownership-checked).
	addressRepo := postgres.NewAddressRepository(db)
	addressHandler := handler.NewAddressHandler(addressRepo)
	addressGroup := v1.Group("/my/addresses")
	addressGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		addressGroup.GET("", addressHandler.ListMyAddresses)
		addressGroup.POST("", addressHandler.CreateAddress)
		addressGroup.PUT("/:id", addressHandler.UpdateAddress)
		addressGroup.DELETE("/:id", addressHandler.DeleteAddress)
		addressGroup.PUT("/:id/default", addressHandler.SetDefaultAddress)
	}

	// bKash Client — shared by both subscription and marketplace payment paths.
	bkashClient := bkash.NewClient(cfg)

	// Billing (ledger, invoices, payouts) and entitlements — shared by the
	// payment services below, which book into the ledger inside the same DB
	// transaction that fulfils a payment.
	billingSvc := service.NewBillingService(db)
	entitlementSvc := service.NewEntitlementService(db)

	// Order routes — admin (API-key gated) and buyer (JWT gated)
	orderRepo := postgres.NewOrderRepository(db)
	orderTxRepo := postgres.NewOrderTransactionRepository(db)
	marketplacePaymentSvc := service.NewMarketplacePaymentService(db, orderRepo, orderTxRepo, bkashClient, cfg.BkashCallbackBaseURL, billingSvc)
	orderNotifier := service.NewOrderNotifier(db, notificationService)
	billingSvc.SetOrderNotifier(orderNotifier)
	marketplacePaymentSvc.SetOrderNotifier(orderNotifier)
	insightsSvc := service.NewInsightsService(db)
	insightsHandler := handler.NewInsightsHandler(insightsSvc, merchantRepo, db)
	orderSvc := service.NewOrderService(db, billingSvc, orderNotifier)
	orderSvc.SetInsights(insightsSvc)
	commissionSvc := service.NewCommissionService(db)
	marketAdmin := handler.NewMarketplaceAdminHandler(db, commissionSvc, insightsSvc, merchantRepo)
	reviewHandler := handler.NewReviewHandler(service.NewReviewService(db, notificationService), merchantRepo)
	orderHandler := handler.NewOrderHandler(orderRepo, productRepo, merchantRepo, addressRepo, marketplacePaymentSvc, orderSvc)
	orderHandler.SetCommission(commissionSvc)

	orderAdminGroup := v1.Group("/orders")
	{
		orderAdminGroup.GET("", guard.Require(middleware.AdminJWT), orderHandler.GetAllOrders)
		orderAdminGroup.GET("/:id", guard.Require(middleware.AdminJWT), orderHandler.GetOrderByID)
		orderAdminGroup.PUT("/:id/status", guard.Require(middleware.AdminJWT), orderHandler.UpdateOrderStatus)
	}

	myOrderGroup := v1.Group("/my/orders")
	myOrderGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myOrderGroup.POST("/checkout", orderHandler.Checkout)
		myOrderGroup.GET("", orderHandler.ListMyOrders)
		myOrderGroup.GET("/:id", orderHandler.GetMyOrder)
		myOrderGroup.POST("/:id/cancel", orderHandler.CancelMyOrder)
		myOrderGroup.GET("/:id/reviewable", reviewHandler.ReviewableItems)
	}

	// Product reviews: buyers (verified purchase), sellers (reply), admin (moderate).
	myProductGroup := v1.Group("/my/products/:id")
	myProductGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myProductGroup.GET("/reviews", reviewHandler.ListProductReviews)
		myProductGroup.PUT("/review", reviewHandler.UpsertReview)
		myProductGroup.DELETE("/review", reviewHandler.DeleteReview)
		myProductGroup.POST("/view", insightsHandler.RecordView)
	}
	v1.GET("/my/merchants/:id/stats", middleware.JWTMiddleware(jwtManager, db), insightsHandler.MerchantStats)
	myMerchantReviewsGroup := v1.Group("/my/merchants/:id/reviews")
	myMerchantReviewsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myMerchantReviewsGroup.GET("", reviewHandler.MerchantReviews)
		myMerchantReviewsGroup.POST("/:rid/reply", reviewHandler.ReplyToReview)
	}
	v1.GET("/my/merchants/:id/commission", middleware.JWTMiddleware(jwtManager, db), marketAdmin.MyCommission)
	v1.GET("/marketplace/promo", guard.Require(middleware.Public), marketAdmin.PublicPromo)
	v1.PUT("/products/:id/feature", guard.Require(middleware.AdminJWT), marketAdmin.FeatureProduct)
	v1.GET("/merchants/:id/stats", guard.Require(middleware.AdminJWT), marketAdmin.MerchantStats)
	v1.GET("/reviews", guard.Require(middleware.AdminJWT), reviewHandler.AdminList)
	v1.PUT("/reviews/:id/hide", guard.Require(middleware.AdminJWT), reviewHandler.AdminSetHidden)

	// Merchant self-service orders: a seller viewing orders containing their
	// own products, scoped by merchant id (ownership-checked) since a user
	// may own several merchants — grouped separately from /my/merchants
	// above only because orderHandler isn't constructed until this point.
	myMerchantOrdersGroup := v1.Group("/my/merchants/:id/orders")
	myMerchantOrdersGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myMerchantOrdersGroup.GET("", orderHandler.ListMerchantOrders)
		// Fulfillment only — shipped/delivered. Payment/cancellation states
		// stay admin-only via PUT /orders/:id/status above.
		myMerchantOrdersGroup.PUT("/:orderId/status", orderHandler.MerchantUpdateOrderStatus)
	}

	// Marketplace payment routes — JWT gated, mirrors /payments/bkash
	mpPaymentGroup := v1.Group("/payments/marketplace")
	mpPaymentGroup.Use(middleware.JWTMiddleware(jwtManager, db), payLimit)
	{
		mpPaymentGroup.POST("/create", orderHandler.CreateMarketplacePayment)
		mpPaymentGroup.POST("/execute", orderHandler.ExecuteMarketplacePayment)
	}

	// bKash Payment Routes — server-owns the entire bKash exchange (grant/
	// create/execute); the app never sees bKash credentials or decides the
	// charged amount. JWT-protected since every action is scoped to "the
	// current user".
	subRepo := postgres.NewSubscriptionRepository(db)
	bkashTxRepo := postgres.NewBkashTransactionRepository(db)
	couponRepo := postgres.NewCouponRepository(db)
	paymentService := service.NewPaymentService(db, subRepo, bkashTxRepo, couponRepo, bkashClient, cfg.BkashCallbackBaseURL, billingSvc)
	bkashHandler := handler.NewBkashHandler(paymentService)
	paymentGroup := v1.Group("/payments/bkash")
	paymentGroup.Use(middleware.JWTMiddleware(jwtManager, db), payLimit)
	{
		paymentGroup.POST("/create", bkashHandler.CreatePayment)
		paymentGroup.POST("/execute", bkashHandler.ExecutePayment)
		paymentGroup.POST("/cancel", bkashHandler.CancelPayment)
		paymentGroup.GET("/transactions", bkashHandler.ListTransactions)
	}

	// Subscription Routes
	subHandler := handler.NewSubscriptionHandler(subRepo, paymentService)
	subGroup := v1.Group("/subscriptions")
	subGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		subGroup.GET("/plans", subHandler.GetPlans)
		subGroup.GET("/user/:uid", subHandler.GetUserSubscription)

		// Admin Routes — RequireAdmin always enforces (these used to be
		// reachable by any logged-in user, who could grant themselves Pro).
		subGroup.GET("", middleware.RequireAdmin(), subHandler.GetAllSubscriptions)
		subGroup.POST("", middleware.RequireAdmin(), subHandler.CreateSubscription)
		subGroup.POST("/admin-grant", middleware.RequireAdmin(), subHandler.AdminGrantPro)

		planGroup := v1.Group("/subscription-plans")
		{
			planGroup.GET("", guard.Require(middleware.AuthJWT), subHandler.GetAllPlans)
			planGroup.POST("", guard.Require(middleware.AdminJWT), subHandler.CreatePlan)
			planGroup.PUT("/:id", guard.Require(middleware.AdminJWT), subHandler.UpdatePlan)
			planGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), subHandler.DeletePlan)
		}
	}

	// Entitlements — what the current user's plan lets them do. The app
	// should gate features from this, not from a bare is_pro flag. Plan
	// entitlements are admin-managed.
	entitlementHandler := handler.NewEntitlementHandler(entitlementSvc)
	v1.GET("/my/entitlements", middleware.JWTMiddleware(jwtManager, db), entitlementHandler.MyEntitlements)
	planEntGroup := v1.Group("/subscription-plans/:id/entitlements")
	planEntGroup.Use(middleware.JWTMiddleware(jwtManager, db), middleware.RequireAdmin())
	{
		planEntGroup.GET("", entitlementHandler.GetPlanEntitlements)
		planEntGroup.PUT("", entitlementHandler.SetPlanEntitlements)
	}

	// Billing — invoices, merchant earnings/payouts, admin ledger.
	billingHandler := handler.NewBillingHandler(billingSvc, merchantRepo)
	myBillingGroup := v1.Group("/my")
	myBillingGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myBillingGroup.GET("/invoices", billingHandler.ListMyInvoices)
		myBillingGroup.GET("/invoices/:id", billingHandler.GetMyInvoice)
		myBillingGroup.GET("/invoices/:id/print", billingHandler.PrintMyInvoice)
		myBillingGroup.GET("/merchants/:id/earnings", billingHandler.MyMerchantEarnings)
		myBillingGroup.GET("/merchants/:id/payouts", billingHandler.ListMyMerchantPayouts)
		myBillingGroup.POST("/merchants/:id/payouts", billingHandler.RequestMyMerchantPayout)
	}
	billingAdminGroup := v1.Group("/billing")
	billingAdminGroup.Use(middleware.JWTMiddleware(jwtManager, db), middleware.RequireAdmin())
	{
		billingAdminGroup.GET("/commission-policy", marketAdmin.GetPolicy)
		billingAdminGroup.PUT("/commission-policy", marketAdmin.SetPolicy)
		billingAdminGroup.GET("/marketplace-metrics", marketAdmin.Metrics)
		billingAdminGroup.GET("/summary", billingHandler.AdminSummary)
		billingAdminGroup.GET("/ledger", billingHandler.AdminLedger)
		billingAdminGroup.GET("/invoices", billingHandler.AdminListInvoices)
		billingAdminGroup.GET("/merchants/:id/balance", billingHandler.AdminMerchantBalance)
		billingAdminGroup.GET("/payouts", billingHandler.AdminListPayouts)
		billingAdminGroup.PUT("/payouts/:id/paid", billingHandler.AdminMarkPayoutPaid)
		billingAdminGroup.PUT("/payouts/:id/reject", billingHandler.AdminRejectPayout)
		billingAdminGroup.GET("/payments", billingHandler.AdminListPayments)
		billingAdminGroup.GET("/refunds", billingHandler.AdminListRefunds)
		billingAdminGroup.POST("/refunds/subscription", billingHandler.AdminRefundSubscription)
		billingAdminGroup.POST("/refunds/order", billingHandler.AdminRefundOrder)
	}

	// Coupon Code Routes (admin)
	couponHandler := handler.NewCouponHandler(couponRepo)
	couponGroup := v1.Group("/coupons")
	couponGroup.Use(middleware.JWTMiddleware(jwtManager, db), middleware.RequireAdmin())
	{
		couponGroup.GET("", couponHandler.GetAll)
		couponGroup.POST("", couponHandler.Create)
		couponGroup.PUT("/:id", couponHandler.Update)
	}

	// Feedback Routes
	feedbackRepo := postgres.NewFeedbackRepository(db)
	feedbackHandler := handler.NewFeedbackHandler(feedbackRepo)
	feedbackGroup := v1.Group("/feedback")
	feedbackGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		feedbackGroup.POST("", feedbackHandler.Create)
		feedbackGroup.GET("/my", feedbackHandler.GetMyFeedbacks)
	}

	adminFeedbackGroup := v1.Group("/feedback")
	adminFeedbackGroup.Use(middleware.JWTMiddleware(jwtManager, db), guard.Require(middleware.AdminJWT))
	{
		adminFeedbackGroup.GET("", feedbackHandler.GetAll)
		adminFeedbackGroup.GET("/:id", feedbackHandler.GetByID)
		adminFeedbackGroup.PUT("/:id", feedbackHandler.Update)
	}

	// Community Routes
	communityRepo := postgres.NewCommunityRepository(db)
	communityUsecase := usecase.NewCommunityUseCase(communityRepo, db)
	communityHandler := handler.NewCommunityHandler(communityUsecase, r2, db)
	communityGroup := v1.Group("/community")
	communityGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		communityGroup.POST("/posts", communityHandler.CreatePost)
		communityGroup.GET("/posts", communityHandler.GetPosts)
		communityGroup.GET("/posts/liked", communityHandler.GetLikedPosts)
		communityGroup.GET("/posts/saved", communityHandler.GetSavedPosts)
		communityGroup.PUT("/posts/:id", communityHandler.UpdatePost)
		communityGroup.DELETE("/posts/:id", communityHandler.DeletePost)
		communityGroup.POST("/posts/:id/like", communityHandler.LikePost)
		communityGroup.POST("/posts/:id/unlike", communityHandler.UnlikePost)
		communityGroup.POST("/posts/:id/save", communityHandler.SavePost)
		communityGroup.POST("/posts/:id/unsave", communityHandler.UnsavePost)
		communityGroup.POST("/posts/:id/comments", communityHandler.AddComment)
		communityGroup.GET("/posts/:id/comments", communityHandler.GetComments)
		communityGroup.POST("/comments/:comment_id/like", communityHandler.LikeComment)
		communityGroup.POST("/comments/:comment_id/unlike", communityHandler.UnlikeComment)
		communityGroup.PUT("/comments/:comment_id", communityHandler.UpdateComment)
		communityGroup.DELETE("/comments/:comment_id", communityHandler.DeleteComment)
	}

	// Notification WebSocket hub (for real-time delivery)
	notifHub := ws.NewNotificationHub()
	notifWSHandler := ws.NewNotificationWSHandler(notifHub)

	// Notification Routes
	notificationHandler := handler.NewNotificationHandler(db, notificationService, notifHub)

	// Admin notification endpoints — API key only (inherited from v1 group)
	adminNotifGroup := v1.Group("/admin/notifications")
	{
		adminNotifGroup.GET("", guard.Require(middleware.AdminJWT), notificationHandler.GetAllAdminNotifications)
		adminNotifGroup.POST("", guard.Require(middleware.AdminJWT), notificationHandler.CreateAdminNotification)
		adminNotifGroup.POST("/preview", guard.Require(middleware.AdminJWT), notificationHandler.PreviewAdminNotification)
		adminNotifGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), notificationHandler.DeleteAdminNotification)
	}

	// User-facing notification endpoints — require JWT
	notifGroup := v1.Group("/notifications")
	notifGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		notifGroup.GET("", notificationHandler.GetNotifications)
		// CreateNotification lets a caller target an arbitrary scope
		// (batch/department/university/custom) — restricted to staff-ish
		// roles so a plain student account can't broadcast to an entire
		// university. CreateAdminNotification (API-key-only, used by the
		// admin dashboard) is a separate, already-trusted path.
		notifGroup.POST("", middleware.RoleMiddleware(
			string(domain.RoleSuperAdmin),
			string(domain.RoleUniversityAdmin),
			string(domain.RoleDepartmentAdmin),
			string(domain.RoleTeacher),
			string(domain.RoleStaff),
		), notificationHandler.CreateNotification)
		notifGroup.POST("/:id/read", notificationHandler.MarkAsRead)
		notifGroup.POST("/read-all", notificationHandler.MarkAllAsRead)
		notifGroup.DELETE("/:id", notificationHandler.DeleteNotification)
	}

	// Device (FCM push token) Routes — require JWT
	deviceHandler := handler.NewDeviceHandler(db, jwtManager, deviceTopicService)
	deviceGroup := v1.Group("/devices")
	deviceGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		deviceGroup.GET("", deviceHandler.ListDevices)
		deviceGroup.POST("", deviceHandler.RegisterDevice)
		deviceGroup.POST("/unregister", deviceHandler.UnregisterDevice)
		deviceGroup.DELETE("/:id", deviceHandler.RemoveDevice)
		deviceGroup.POST("/logout-all", deviceHandler.LogoutAll)
		deviceGroup.POST("/logout-others", deviceHandler.LogoutOthers)
	}

	// Notification preference (mute-by-category) Routes — require JWT
	notifPrefHandler := handler.NewNotificationPreferenceHandler(db, deviceTopicService)
	notifPrefGroup := v1.Group("/my/notification-preferences")
	notifPrefGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		notifPrefGroup.GET("", notifPrefHandler.GetMyPreferences)
		notifPrefGroup.PUT("/:category", notifPrefHandler.UpdatePreference)
	}

	// Chat Routes
	chatRepo := postgres.NewChatRepository(db)
	chatUsecase := usecase.NewChatUseCase(chatRepo)
	chatHub := ws.NewHub()
	chatWSHandler := ws.NewChatWSHandler(chatHub, chatUsecase)
	chatHandler := handler.NewChatHandler(chatUsecase, chatWSHandler)

	// WebSocket routes (JWT-protected, no API key needed)
	wsGroup := r.Group("/ws/chat")
	wsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		wsGroup.GET("/:id", chatWSHandler.ServeWS)
	}

	notifWsGroup := r.Group("/ws/notifications")
	notifWsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		notifWsGroup.GET("", notifWSHandler.ServeWS)
	}

	chatGroup := v1.Group("/conversations")
	chatGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		chatGroup.GET("/contacts", chatHandler.GetContacts)
		chatGroup.GET("", chatHandler.GetConversations)
		chatGroup.GET("/pending", chatHandler.GetPendingConversations)
		chatGroup.POST("", chatHandler.GetOrCreateConversation)
		chatGroup.GET("/:id/messages", chatHandler.GetMessages)
		chatGroup.POST("/:id/messages", chatHandler.SendMessage)
		chatGroup.PUT("/:id/messages/:messageId", chatHandler.UpdateMessage)
		chatGroup.DELETE("/:id/messages/:messageId", chatHandler.DeleteMessage)
		chatGroup.DELETE("/:id", chatHandler.DeleteConversation)
		chatGroup.POST("/:id/read", chatHandler.MarkAsRead)
		chatGroup.POST("/:id/accept", chatHandler.AcceptRequest)
		chatGroup.POST("/:id/block", chatHandler.BlockRequest)
		chatGroup.POST("/:id/archive", chatHandler.ArchiveConversation)
	}

	// Lost & Found Portal
	lostFoundRepo := postgres.NewLostFoundRepository(db)
	lostFoundHandler := handler.NewLostFoundHandler(db, lostFoundRepo, chatUsecase, notificationService)

	registerRoutes[domain.LostFoundCategory](v1, db, "lost-found-categories", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	lostFoundGroup := v1.Group("/lost-found-items")
	{
		lostFoundGroup.GET("", guard.Require(middleware.AuthJWT), lostFoundHandler.GetAllItems)
		lostFoundGroup.GET("/:id", guard.Require(middleware.AuthJWT), lostFoundHandler.GetItemByID)
		lostFoundGroup.PUT("/:id/status", guard.Require(middleware.AdminJWT), lostFoundHandler.SetStatus)
		lostFoundGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), lostFoundHandler.DeleteItem)
	}
	v1.GET("/lost-found-items-by-location", guard.Require(middleware.Public), lostFoundHandler.GetItemsByLocation)

	lostFoundReportsGroup := v1.Group("/lost-found-reports")
	{
		lostFoundReportsGroup.GET("", guard.Require(middleware.AuthJWT), lostFoundHandler.GetAllReports)
		lostFoundReportsGroup.PUT("/:id/resolve", guard.Require(middleware.AdminJWT), lostFoundHandler.ResolveReport)
	}

	// Authenticated actions any student can take against someone else's item
	// (claim it, report it) — same base path as the admin group above but a
	// distinct JWT-gated group, same pattern as associationAuthGroup.
	lostFoundAuthGroup := v1.Group("/lost-found-items")
	lostFoundAuthGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		lostFoundAuthGroup.POST("/:id/claims", lostFoundHandler.CreateClaim)
		lostFoundAuthGroup.POST("/:id/report", lostFoundHandler.ReportItem)
	}

	// Self-service management of the caller's own items.
	myLostFoundGroup := v1.Group("/my/lost-found-items")
	myLostFoundGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myLostFoundGroup.GET("", lostFoundHandler.GetMyItems)
		myLostFoundGroup.POST("", lostFoundHandler.CreateMyItem)
		myLostFoundGroup.PUT("/:id", lostFoundHandler.UpdateMyItem)
		myLostFoundGroup.DELETE("/:id", lostFoundHandler.DeleteMyItem)
		myLostFoundGroup.POST("/:id/resolve", lostFoundHandler.ResolveMyItem)
		myLostFoundGroup.GET("/:id/claims", lostFoundHandler.GetClaimsForMyItem)
		myLostFoundGroup.POST("/:id/claims/:claimId/accept", lostFoundHandler.AcceptClaim)
		myLostFoundGroup.POST("/:id/claims/:claimId/reject", lostFoundHandler.RejectClaim)
	}

	// Career: Circular / My Jobs / Reminders
	careerRepo := postgres.NewCareerRepository(db)
	careerHandler := handler.NewCareerHandler(careerRepo, db)

	registerRoutes[domain.CareerCircularCategory](v1, db, "career-circular-categories", guard, middleware.Access{Read: middleware.AuthJWT, Write: middleware.AdminJWT})

	careerCircularGroup := v1.Group("/career-circulars")
	{
		careerCircularGroup.GET("", guard.Require(middleware.AuthJWT), careerHandler.GetAllCirculars)
		careerCircularGroup.POST("", guard.Require(middleware.AdminJWT), careerHandler.CreateCircular)
		careerCircularGroup.GET("/:id", guard.Require(middleware.AuthJWT), careerHandler.GetCircularByID)
		careerCircularGroup.PUT("/:id", guard.Require(middleware.AdminJWT), careerHandler.UpdateCircular)
		careerCircularGroup.DELETE("/:id", guard.Require(middleware.AdminJWT), careerHandler.DeleteCircular)
		careerCircularGroup.POST("/:id/view", guard.Require(middleware.AdminJWT), careerHandler.ViewCircular)
	}
	v1.GET("/career-circulars-by-location", guard.Require(middleware.Public), careerHandler.GetCircularsByLocation)

	myCareerJobsGroup := v1.Group("/my/career-jobs")
	myCareerJobsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myCareerJobsGroup.GET("", careerHandler.GetMyJobs)
		myCareerJobsGroup.POST("", careerHandler.CreateMyJob)
		myCareerJobsGroup.POST("/from-circular/:circularId", careerHandler.CreateMyJobFromCircular)
		myCareerJobsGroup.PUT("/:id", careerHandler.UpdateMyJob)
		myCareerJobsGroup.PUT("/:id/status", careerHandler.SetMyJobStatus)
		myCareerJobsGroup.DELETE("/:id", careerHandler.DeleteMyJob)
	}

	// Peer-shared Jobs (opt-in visibility, scoped to batch/department/
	// university) — JWT-gated since it needs the viewer's own affiliation,
	// but not "my own resource" so it lives outside /my/.
	careerJobsAuthGroup := v1.Group("/career-jobs-shared")
	careerJobsAuthGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		careerJobsAuthGroup.GET("", careerHandler.GetSharedJobs)
	}

	myCareerRemindersGroup := v1.Group("/my/career-reminders")
	myCareerRemindersGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myCareerRemindersGroup.GET("", careerHandler.GetMyReminders)
		myCareerRemindersGroup.POST("", careerHandler.CreateMyReminder)
		myCareerRemindersGroup.DELETE("/:id", careerHandler.CancelMyReminder)
	}

	// Poll for due reminders and push them via FCM — server-driven delivery
	// instead of a client-scheduled local alarm, so it survives reinstalls/
	// reboots and works across a student's devices.
	service.NewCareerReminderScheduler(db, careerRepo, notificationService).Start(context.Background())

	// Global search: one round trip across resources, notices, courses,
	// clubs, associations, teachers, staff, marketplace products, lost &
	// found items, and career circulars.
	searchRepo := postgres.NewSearchRepository(db)
	searchHandler := handler.NewSearchHandler(searchRepo)
	v1.GET("/search", guard.Require(middleware.AuthJWT), searchHandler.Search)

	return r
}

// registerRoutes wires generic CRUD for T.
//
// The access argument is required and has no meaningful zero value, so every
// call site must state what protects it. That is deliberate: this helper
// previously took no policy at all, and the result was ~20 resources — users,
// students, verification codes — exposed to anyone holding the API key that
// ships inside the client bundles.
func registerRoutes[T any](group *gin.RouterGroup, db *gorm.DB, path string, guard *middleware.Guard, access middleware.Access) {
	repo := postgres.NewGormRepository[T](db)
	uc := usecase.NewGenericUsecase(repo)
	h := handler.NewGenericHandler(uc)

	read := guard.Require(access.Read)
	write := guard.Require(access.Write)

	g := group.Group("/" + path)
	{
		g.POST("", write, h.Create)
		g.GET("", read, h.GetAll)
		g.GET("/:id", read, h.GetByID)
		g.PUT("/:id", write, h.Update)
		g.DELETE("/:id", write, h.Delete)
	}
}
