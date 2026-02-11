package http

import (
	"campusassistant-api/internal/config"
	"campusassistant-api/internal/delivery/http/handler"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/internal/usecase"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func NewRouter(cfg *config.Config, db *gorm.DB) *gin.Engine {
	r := gin.Default()

	// Middleware (CORS, Auth could be added here)
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	// API V1 Group
	v1 := r.Group("/api/v1")

	// Helper to register generic routes
	registerRoutes[domain.University](v1, db, "universities")
	registerRoutes[domain.Department](v1, db, "departments")
	registerRoutes[domain.Session](v1, db, "sessions")
	registerRoutes[domain.Batch](v1, db, "batches")
	registerRoutes[domain.User](v1, db, "users")
	registerRoutes[domain.Student](v1, db, "students") // Note: Student profile
	registerRoutes[domain.Teacher](v1, db, "teachers")
	registerRoutes[domain.Staff](v1, db, "staffs")
	registerRoutes[domain.Verification](v1, db, "verifications")
	registerRoutes[domain.Book](v1, db, "books")
	registerRoutes[domain.Question](v1, db, "questions")
	registerRoutes[domain.Note](v1, db, "notes")
	registerRoutes[domain.Syllabus](v1, db, "syllabuses")
	registerRoutes[domain.Transport](v1, db, "transports")

	return r
}

func registerRoutes[T any](group *gin.RouterGroup, db *gorm.DB, path string) {
	repo := postgres.NewGormRepository[T](db)
	uc := usecase.NewGenericUsecase[T](repo)
	h := handler.NewGenericHandler[T](uc)

	g := group.Group("/" + path)
	{
		g.POST("", h.Create)
		g.GET("", h.GetAll)
		g.GET("/:id", h.GetByID)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
}
