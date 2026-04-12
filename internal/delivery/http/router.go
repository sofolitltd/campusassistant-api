package http

import (
	"campusassistant-api/internal/config"
	"campusassistant-api/internal/delivery/http/handler"
	"campusassistant-api/internal/delivery/http/middleware"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/internal/usecase"
	"campusassistant-api/pkg/auth"
	"campusassistant-api/pkg/storage"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func NewRouter(cfg *config.Config, db *gorm.DB) *gin.Engine {
	r := gin.Default()

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
	authHandler := handler.NewAuthHandler(db, jwtManager)
	authGroup := v1.Group("/auth")
	{
		authGroup.POST("/register", authHandler.Register)
		authGroup.POST("/login", authHandler.Login)
		authGroup.POST("/refresh", authHandler.RefreshToken)
		// Protected route - requires JWT
		authGroup.GET("/me", middleware.JWTMiddleware(jwtManager), authHandler.GetMe)
	}

	// Protected Routes (Require API Key for now, can add JWT later)
	v1.Use(middleware.APIKeyMiddleware(cfg.APIKey))

	// Helper to register generic routes
	registerRoutes[domain.University](v1, db, "universities")
	registerRoutes[domain.Department](v1, db, "departments")
	registerRoutes[domain.Session](v1, db, "sessions")
	registerRoutes[domain.Batch](v1, db, "batches")
	registerRoutes[domain.User](v1, db, "users")

	// Specialized Student Routes
	studentRepo := postgres.NewGormRepository[domain.Student](db)
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

	registerRoutes[domain.Teacher](v1, db, "teachers")
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

	resourceRepo := postgres.NewResourceRepository(db)
	resourceUsecase := usecase.NewGenericUsecase(resourceRepo)
	resourceHandler := handler.NewResourceHandler(resourceUsecase)
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

	semesterRepo := postgres.NewSemesterRepository(db)
	semesterUsecase := usecase.NewGenericUsecase[domain.Semester](semesterRepo)
	semesterHandler := handler.NewGenericHandler[domain.Semester](semesterUsecase)
	sg := v1.Group("/semesters")
	{
		sg.POST("", semesterHandler.Create)
		sg.GET("", semesterHandler.GetAll)
		sg.GET("/:id", semesterHandler.GetByID)
		sg.PUT("/:id", semesterHandler.Update)
		sg.DELETE("/:id", semesterHandler.Delete)
	}

	registerRoutes[domain.Hall](v1, db, "halls")
	registerRoutes[domain.Alumni](v1, db, "alumni")
	registerRoutes[domain.Bookmark](v1, db, "bookmarks")

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

	// specialized Banner Routes
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

	registerRoutes[domain.EmergencyContact](v1, db, "emergency-contacts")

	// R2 Upload Routes
	storage, err := storage.NewR2Storage(cfg)
	if err == nil {
		uploadHandler := handler.NewUploadHandler(db, storage)
		v1.POST("/upload", uploadHandler.UploadImage)
		r.GET("/upload", uploadHandler.ShowUploadPage) // Serving the demo page at root /upload
	}

	// Subscription Routes
	subRepo := postgres.NewSubscriptionRepository(db)
	subHandler := handler.NewSubscriptionHandler(subRepo)
	subGroup := v1.Group("/subscriptions")
	{
		subGroup.GET("/plans", subHandler.GetPlans)
		subGroup.GET("/features", subHandler.GetFeatures)
		subGroup.GET("/user/:uid", subHandler.GetUserSubscription)
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
