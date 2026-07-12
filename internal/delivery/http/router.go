package http

import (
	"campusassistant-api/internal/config"
	"campusassistant-api/internal/delivery/http/handler"
	"campusassistant-api/internal/delivery/http/middleware"
	ws "campusassistant-api/internal/delivery/http/websocket"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/internal/usecase"
	"campusassistant-api/pkg/auth"
	"campusassistant-api/pkg/storage"
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
		authGroup.GET("/me", middleware.JWTMiddleware(jwtManager), authHandler.GetMe)
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
	registerRoutes[domain.Department](v1, db, "departments")
	registerRoutes[domain.Session](v1, db, "sessions")
	registerRoutes[domain.Batch](v1, db, "batches")
	registerRoutes[domain.User](v1, db, "users")

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

	r2Storage, r2Err := storage.NewR2Storage(cfg)
	resourceRepo := postgres.NewResourceRepository(db)
	resourceUsecase := usecase.NewGenericUsecase(resourceRepo)
	var r2 *storage.R2Storage
	if r2Err == nil {
		r2 = r2Storage
	}
	resourceHandler := handler.NewResourceHandler(resourceUsecase, r2)
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
	registerRoutes[domain.Bookmark](v1, db, "bookmarks")
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

	registerRoutes[domain.Club](v1, db, "clubs")
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

	// Community Routes
	communityRepo := postgres.NewCommunityRepository(db)
	communityUsecase := usecase.NewCommunityUseCase(communityRepo)
	communityHandler := handler.NewCommunityHandler(communityUsecase)
	communityGroup := v1.Group("/community")
	communityGroup.Use(middleware.JWTMiddleware(jwtManager))
	{
		communityGroup.POST("/posts", communityHandler.CreatePost)
		communityGroup.GET("/posts", communityHandler.GetPosts)
		communityGroup.GET("/posts/saved", communityHandler.GetSavedPosts)
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

	// Chat Routes
	chatRepo := postgres.NewChatRepository(db)
	chatUsecase := usecase.NewChatUseCase(chatRepo)
	chatHub := ws.NewHub()
	chatWSHandler := ws.NewChatWSHandler(chatHub, chatUsecase)
	chatHandler := handler.NewChatHandler(chatUsecase, chatWSHandler)

	// WebSocket routes (JWT-protected, no API key needed)
	wsGroup := r.Group("/ws/chat")
	wsGroup.Use(middleware.JWTMiddleware(jwtManager))
	{
		wsGroup.GET("/:id", chatWSHandler.ServeWS)
	}

	chatGroup := v1.Group("/conversations")
	chatGroup.Use(middleware.JWTMiddleware(jwtManager))
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
