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
	"campusassistant-api/pkg/storage"
	"context"
	"log"
	netHTTP "net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func NewRouter(cfg *config.Config, db *gorm.DB) *gin.Engine {
	r := gin.Default()
	r.MaxMultipartMemory = 10 << 20 // 10 MB limit for file uploads

	// Middlewares
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(middleware.CORSMiddleware())

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

	// API V1 Group
	v1 := r.Group("/api/v1")

	// Public Auth Routes (No API Key or JWT required)
	authHandler := handler.NewAuthHandler(db, jwtManager, cfg.JWTAccessTokenExpiry)
	authGroup := v1.Group("/auth")
	{
		authGroup.POST("/register", authHandler.Register)
		authGroup.POST("/login", authHandler.Login)
		authGroup.POST("/refresh", authHandler.RefreshToken)
		// Protected route - requires JWT
		authGroup.GET("/me", middleware.JWTMiddleware(jwtManager, db), authHandler.GetMe)
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

	// Protected Routes (Require API Key for now, can add JWT later)
	v1.Use(middleware.APIKeyMiddleware(cfg.APIKey))

	// Helper to register generic routes
	registerRoutes[domain.University](v1, db, "universities")
	registerRoutes[domain.Faculty](v1, db, "faculties")
	registerRoutes[domain.Department](v1, db, "departments")
	registerRoutes[domain.Session](v1, db, "sessions")
	registerRoutes[domain.Batch](v1, db, "batches")
	registerRoutes[domain.User](v1, db, "users")
	registerRoutes[domain.Notice](v1, db, "notices")
	registerRoutes[domain.Contributor](v1, db, "contributors")

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
		studentGroup.POST("", studentHandler.Create)
		studentGroup.POST("/verify-code", studentHandler.VerifyCode)
		studentGroup.POST("/claim-profile", studentHandler.ClaimProfile)
		studentGroup.GET("", studentHandler.GetAll)
		studentGroup.GET("/:id", studentHandler.GetByID)
		studentGroup.PUT("/:id", studentHandler.Update)
		studentGroup.DELETE("/:id", studentHandler.Delete)
	}

	teacherRepo := postgres.NewGormRepositoryWithOrder[domain.Teacher](db, "weight ASC, name ASC")
	teacherUsecase := usecase.NewGenericUsecase(teacherRepo)
	teacherHandler := handler.NewGenericHandler(teacherUsecase)
	teacherGroup := v1.Group("/teachers")
	{
		teacherGroup.POST("", teacherHandler.Create)
		teacherGroup.GET("", teacherHandler.GetAll)
		teacherGroup.GET("/:id", teacherHandler.GetByID)
		teacherGroup.PUT("/:id", teacherHandler.Update)
		teacherGroup.DELETE("/:id", teacherHandler.Delete)
	}
	registerRoutes[domain.Staff](v1, db, "staffs")
	crRepo := postgres.NewGormRepository[domain.CR](db)
	crUsecase := usecase.NewGenericUsecase(crRepo)
	crHandler := handler.NewCrHandler(crUsecase)
	crGroup := v1.Group("/crs")
	{
		crGroup.POST("", crHandler.Create)
		crGroup.GET("", crHandler.GetAll)
		crGroup.GET("/:id", crHandler.GetByID)
		crGroup.PUT("/:id", crHandler.Update)
		crGroup.DELETE("/:id", crHandler.Delete)
	}
	registerRoutes[domain.Verification](v1, db, "verifications")

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
		rg.POST("", resourceHandler.Create)
		rg.GET("", resourceHandler.GetAll)
		rg.GET("/:id", resourceHandler.GetByID)
		rg.PUT("/:id", resourceHandler.Update)
		rg.DELETE("/:id", resourceHandler.Delete)
		// Review workflow
		rg.PATCH("/:id/approve", resourceHandler.ApproveResource)
		rg.PATCH("/:id/reject", resourceHandler.RejectResource)
		// Engagement
		rg.POST("/:id/download", resourceHandler.IncrementDownload)
	}

	registerRoutes[domain.Transport](v1, db, "transports")
	registerRoutes[domain.Attachment](v1, db, "attachments")

	levelRepo := postgres.NewLevelRepository(db)
	levelUsecase := usecase.NewGenericUsecase[domain.Level](levelRepo)
	levelHandler := handler.NewGenericHandler[domain.Level](levelUsecase)
	lg := v1.Group("/levels")
	{
		lg.POST("", levelHandler.Create)
		lg.GET("", levelHandler.GetAll)
		lg.GET("/:id", levelHandler.GetByID)
		lg.PUT("/:id", levelHandler.Update)
		lg.DELETE("/:id", levelHandler.Delete)
	}

	registerRoutes[domain.Hall](v1, db, "halls")
	registerRoutes[domain.Organization](v1, db, "organizations")
	registerRoutes[domain.Alumni](v1, db, "alumni")
	bookmarkRepo := postgres.NewGormRepository[domain.Bookmark](db)
	bookmarkUc := usecase.NewGenericUsecase(bookmarkRepo)
	bookmarkHandler := handler.NewGenericHandler[domain.Bookmark](bookmarkUc)
	bg := v1.Group("/bookmarks")
	{
		bg.POST("", bookmarkHandler.Create)
		bg.GET("", bookmarkHandler.GetAll)
		bg.GET("/:id", bookmarkHandler.GetByID)
		bg.PUT("/:id", bookmarkHandler.Update)
		bg.DELETE("/:id", bookmarkHandler.HardDelete)
	}
	registerRoutes[domain.Routine](v1, db, "routines")

	courseRepo := postgres.NewCourseRepository(db)
	courseUsecase := usecase.NewGenericUsecase[domain.Course](courseRepo)
	courseHandler := handler.NewGenericHandler[domain.Course](courseUsecase)
	cg := v1.Group("/courses")
	{
		cg.POST("", courseHandler.Create)
		cg.GET("", courseHandler.GetAll)
		cg.GET("/:id", courseHandler.GetByID)
		cg.PUT("/:id", courseHandler.Update)
		cg.DELETE("/:id", courseHandler.Delete)
	}

	registerRoutes[domain.CourseCategory](v1, db, "course-categories")
	registerRoutes[domain.CoursePrefix](v1, db, "course-prefixes")
	chapterRepo := postgres.NewChapterRepository(db)
	chapterUsecase := usecase.NewGenericUsecase(chapterRepo)
	chapterHandler := handler.NewGenericHandler(chapterUsecase)
	chg := v1.Group("/chapters")
	{
		chg.POST("", chapterHandler.Create)
		chg.GET("", chapterHandler.GetAll)
		chg.GET("/:id", chapterHandler.GetByID)
		chg.PUT("/:id", chapterHandler.Update)
		chg.DELETE("/:id", chapterHandler.Delete)
	}

	// Specialized Banner Routes
	bannerRepo := postgres.NewBannerRepository(db)
	bannerUsecase := usecase.NewGenericUsecase[domain.Banner](bannerRepo)
	bannerHandler := handler.NewGenericHandler[domain.Banner](bannerUsecase)
	bannerGroup := v1.Group("/banners")
	{
		bannerGroup.POST("", bannerHandler.Create)
		bannerGroup.GET("", bannerHandler.GetAll)
		bannerGroup.GET("/:id", bannerHandler.GetByID)
		bannerGroup.PUT("/:id", bannerHandler.Update)
		bannerGroup.DELETE("/:id", bannerHandler.Delete)
	}

	// Dashboard Stats
	statsRepo := postgres.NewStatsRepository(db)
	statsHandler := handler.NewStatsHandler(statsRepo)
	v1.GET("/stats", statsHandler.GetDashboardStats)

	// Club: dedicated repo/handler (not generic CRUD) because of the
	// denormalized follower count and JWT-scoped follow/suggest actions.
	clubRepo := postgres.NewClubRepository(db)
	clubHandler := handler.NewClubHandler(clubRepo)
	clubGroup := v1.Group("/clubs")
	{
		clubGroup.GET("", clubHandler.GetAllClubs)
		clubGroup.POST("", clubHandler.CreateClub)
		clubGroup.GET("/:id", clubHandler.GetClubByID)
		clubGroup.PUT("/:id", clubHandler.UpdateClub)
		clubGroup.DELETE("/:id", clubHandler.DeleteClub)
	}
	v1.GET("/clubs-by-location", clubHandler.GetPublicClubs)
	v1.GET("/clubs/:id/members", clubHandler.GetPublicClubMembers)

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
	v1.GET("/clubs/:id/events", clubEventHandler.GetPublicClubEvents)
	clubEventGroup := v1.Group("/club-events")
	{
		clubEventGroup.GET("", clubEventHandler.GetAllClubEvents)
		clubEventGroup.POST("", clubEventHandler.CreateClubEvent)
		clubEventGroup.PUT("/:id", clubEventHandler.UpdateClubEvent)
		clubEventGroup.DELETE("/:id", clubEventHandler.DeleteClubEvent)
	}

	// Club self-service management: JWT-gated "/my/clubs/*" surface for a
	// club's own requester/officers (ClubManager role), fully separate from
	// the admin panel's API-key-gated /clubs CRUD above. See club.go's
	// SuggestClub, which seeds the requester as "owner" at request time.
	clubManagementRepo := postgres.NewClubManagementRepository(db)
	clubManageHandler := handler.NewClubManageHandler(clubManagementRepo, clubRepo, db, notificationService)
	v1.GET("/clubs/:id/posts", clubManageHandler.GetPublicClubPosts)
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
	v1.GET("/bd-districts", bdLocationHandler.GetAll)

	// Association: district/sub-district scoped counterpart to Club. Same
	// dedicated-repo/handler shape as Club, for the same reasons.
	associationRepo := postgres.NewAssociationRepository(db)
	associationHandler := handler.NewAssociationHandler(associationRepo)
	associationGroup := v1.Group("/associations")
	{
		associationGroup.GET("", associationHandler.GetAllAssociations)
		associationGroup.POST("", associationHandler.CreateAssociation)
		associationGroup.GET("/:id", associationHandler.GetAssociationByID)
		associationGroup.PUT("/:id", associationHandler.UpdateAssociation)
		associationGroup.DELETE("/:id", associationHandler.DeleteAssociation)
	}
	v1.GET("/associations-by-location", associationHandler.GetPublicAssociations)
	v1.GET("/associations/:id/members", associationHandler.GetPublicAssociationMembers)

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
	v1.GET("/associations/:id/events", associationEventHandler.GetPublicAssociationEvents)
	associationEventGroup := v1.Group("/association-events")
	{
		associationEventGroup.GET("", associationEventHandler.GetAllAssociationEvents)
		associationEventGroup.POST("", associationEventHandler.CreateAssociationEvent)
		associationEventGroup.PUT("/:id", associationEventHandler.UpdateAssociationEvent)
		associationEventGroup.DELETE("/:id", associationEventHandler.DeleteAssociationEvent)
	}

	// Association self-service management: JWT-gated "/my/associations/*"
	// surface, mirrors Club's "/my/clubs/*" split from the admin panel's
	// API-key-gated /associations CRUD above.
	associationManagementRepo := postgres.NewAssociationManagementRepository(db)
	associationManageHandler := handler.NewAssociationManageHandler(associationManagementRepo, associationRepo, db, notificationService)
	v1.GET("/associations/:id/posts", associationManageHandler.GetPublicAssociationPosts)
	myAssociationsGroup := v1.Group("/my/associations")
	myAssociationsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myAssociationsGroup.GET("", associationManageHandler.GetMyAssociations)
		myAssociationsGroup.PUT("/:id", associationManageHandler.UpdateMyAssociation)
		myAssociationsGroup.GET("/:id/followers", associationManageHandler.GetFollowersForPromotion)
		myAssociationsGroup.GET("/:id/managers", associationManageHandler.GetManagers)
		myAssociationsGroup.POST("/:id/managers", associationManageHandler.PromoteManager)
		myAssociationsGroup.DELETE("/:id/managers/:userId", associationManageHandler.RemoveManager)
		myAssociationsGroup.POST("/:id/events", associationManageHandler.CreateMyAssociationEvent)
		myAssociationsGroup.POST("/:id/posts", associationManageHandler.CreateAssociationPost)
	}

	registerRoutes[domain.EmergencyContact](v1, db, "emergency-contacts")

	// R2 Upload Routes
	if r2 != nil {
		uploadHandler := handler.NewUploadHandler(db, r2)
		v1.POST("/upload", uploadHandler.UploadImage)
		v1.DELETE("/upload", uploadHandler.DeleteFile)
		r.GET("/upload", uploadHandler.ShowUploadPage) // Serving the demo page at root /upload
	}

	// Subscription Routes
	subRepo := postgres.NewSubscriptionRepository(db)
	subHandler := handler.NewSubscriptionHandler(subRepo)
	subGroup := v1.Group("/subscriptions")
	{
		subGroup.GET("/plans", subHandler.GetPlans)
		subGroup.GET("/user/:uid", subHandler.GetUserSubscription)

		// Admin Routes
		subGroup.GET("", subHandler.GetAllSubscriptions)
		subGroup.POST("", subHandler.CreateSubscription)

		planGroup := v1.Group("/subscription-plans")
		{
			planGroup.GET("", subHandler.GetAllPlans)
			planGroup.POST("", subHandler.CreatePlan)
			planGroup.PUT("/:id", subHandler.UpdatePlan)
			planGroup.DELETE("/:id", subHandler.DeletePlan)
		}
	}

	// Skill Routes ("Skill Up" home section) — dedicated (not generic CRUD)
	// because Targets need explicit delete-then-save handling on update,
	// same reasoning as SubscriptionPlan. GetSkillsByLocation is registered
	// at a distinct top-level path (not nested under /skills/:id) to avoid
	// any ambiguity between a literal path segment and the :id wildcard.
	skillRepo := postgres.NewSkillRepository(db)
	skillHandler := handler.NewSkillHandler(skillRepo)
	v1.GET("/skills-by-location", skillHandler.GetSkillsByLocation)
	skillGroup := v1.Group("/skills")
	{
		skillGroup.GET("", skillHandler.GetAllSkills)
		skillGroup.POST("", skillHandler.CreateSkill)
		skillGroup.GET("/:id", skillHandler.GetSkillByID)
		skillGroup.PUT("/:id", skillHandler.UpdateSkill)
		skillGroup.DELETE("/:id", skillHandler.DeleteSkill)
	}
	registerRoutes[domain.SkillVideo](v1, db, "skill-videos")

	// Campus Marketplace: Merchant + Product routes. Dedicated (not generic
	// CRUD) for the same reasons as Skill — Targets need explicit
	// delete-then-save handling, and merchant approval/ownership are custom
	// state transitions/checks, not plain field updates.
	merchantRepo := postgres.NewMerchantRepository(db)
	merchantHandler := handler.NewMerchantHandler(merchantRepo)
	v1.GET("/merchants/platform", merchantHandler.GetPlatformMerchant)
	merchantGroup := v1.Group("/merchants")
	{
		merchantGroup.GET("", merchantHandler.GetAllMerchants)
		merchantGroup.GET("/:id", merchantHandler.GetMerchantByID)
		merchantGroup.PUT("/:id", merchantHandler.UpdateMerchant)
		merchantGroup.PUT("/:id/approve", merchantHandler.ApproveMerchant)
		merchantGroup.PUT("/:id/reject", merchantHandler.RejectMerchant)
		merchantGroup.DELETE("/:id", merchantHandler.DeleteMerchant)
	}

	productRepo := postgres.NewProductRepository(db)
	productHandler := handler.NewProductHandler(productRepo, merchantRepo)
	v1.GET("/products-by-location", productHandler.GetProductsByLocation)
	productGroup := v1.Group("/products")
	{
		productGroup.GET("", productHandler.GetAllProducts)
		productGroup.POST("", productHandler.CreateProduct)
		productGroup.GET("/:id", productHandler.GetProductByID)
		productGroup.PUT("/:id", productHandler.UpdateProduct)
		productGroup.DELETE("/:id", productHandler.DeleteProduct)
	}

	// Merchant self-service surface: JWT-gated "/my/*" routes for a merchant
	// managing their own application/products, mirroring the "/my/clubs"
	// split from the admin panel's API-key-gated /clubs and /products CRUD.
	myMerchantGroup := v1.Group("/my/merchant")
	myMerchantGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myMerchantGroup.POST("/apply", merchantHandler.ApplyForMerchant)
		myMerchantGroup.GET("", merchantHandler.GetMyMerchant)
	}
	myProductsGroup := v1.Group("/my/products")
	myProductsGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		myProductsGroup.GET("", productHandler.GetMyProducts)
		myProductsGroup.POST("", productHandler.CreateMyProduct)
		myProductsGroup.PUT("/:id", productHandler.UpdateMyProduct)
		myProductsGroup.DELETE("/:id", productHandler.DeleteMyProduct)
	}

	// bKash Payment Routes — server-owns the entire bKash exchange (grant/
	// create/execute); the app never sees bKash credentials or decides the
	// charged amount. JWT-protected since every action is scoped to "the
	// current user".
	bkashClient := bkash.NewClient(cfg)
	bkashTxRepo := postgres.NewBkashTransactionRepository(db)
	paymentService := service.NewPaymentService(db, subRepo, bkashTxRepo, bkashClient, cfg.BkashCallbackBaseURL)
	bkashHandler := handler.NewBkashHandler(paymentService)
	paymentGroup := v1.Group("/payments/bkash")
	paymentGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		paymentGroup.POST("/create", bkashHandler.CreatePayment)
		paymentGroup.POST("/execute", bkashHandler.ExecutePayment)
		paymentGroup.GET("/transactions", bkashHandler.ListTransactions)
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
		adminNotifGroup.GET("", notificationHandler.GetAllAdminNotifications)
		adminNotifGroup.POST("", notificationHandler.CreateAdminNotification)
		adminNotifGroup.DELETE("/:id", notificationHandler.DeleteAdminNotification)
	}

	// User-facing notification endpoints — require JWT
	notifGroup := v1.Group("/notifications")
	notifGroup.Use(middleware.JWTMiddleware(jwtManager, db))
	{
		notifGroup.GET("", notificationHandler.GetNotifications)
		notifGroup.POST("", notificationHandler.CreateNotification)
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

	return r
}

func registerRoutes[T any](group *gin.RouterGroup, db *gorm.DB, path string) {
	repo := postgres.NewGormRepository[T](db)
	uc := usecase.NewGenericUsecase(repo)
	h := handler.NewGenericHandler(uc)

	g := group.Group("/" + path)
	{
		g.POST("", h.Create)
		g.GET("", h.GetAll)
		g.GET("/:id", h.GetByID)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
}
