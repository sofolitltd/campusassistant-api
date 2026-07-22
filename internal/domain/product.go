package domain

import (
	"context"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Product is an item listed in the Campus Marketplace, owned by a Merchant
// (in-house products belong to the synthetic platform Merchant). Targets
// scope visibility to a university/department, same pattern as Skill.
type Product struct {
	Base
	MerchantID  uuid.UUID       `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Merchant    Merchant        `gorm:"foreignKey:MerchantID;references:ID" json:"merchant,omitempty"`
	Title       string          `gorm:"size:255;not null" json:"title"`
	Description string          `gorm:"type:text" json:"description"`
	Price       int             `gorm:"not null" json:"price"` // smallest currency unit (poisha)
	Stock       int             `gorm:"default:0" json:"stock"`
	ImageURLs   pq.StringArray  `gorm:"type:text[];default:'{}'" json:"image_urls"`
	Category    string          `gorm:"size:120;index" json:"category"`
	IsPublished bool            `gorm:"default:false;index" json:"is_published"`
	Targets     []ProductTarget `gorm:"foreignKey:ProductID;constraint:OnDelete:CASCADE" json:"targets"`
}

// ProductTarget links a Product to a specific university or department. A
// Product with zero targets is global (visible to every user), mirrors
// SkillTarget.
type ProductTarget struct {
	Base
	ProductID    uuid.UUID `gorm:"type:uuid;not null;index" json:"product_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	// DepartmentID uuid.Nil means "whole university" (every department).
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}

// ProductRepository is dedicated (not generic CRUD) because Targets need
// explicit delete-then-save handling on update and merchant-scoped/
// location-scoped listing needs custom queries, same reasoning as Skill.
type ProductRepository interface {
	GetAllProducts(ctx context.Context, merchantID uuid.UUID) ([]Product, error)
	GetProductByID(ctx context.Context, id uuid.UUID) (*Product, error)
	CreateProduct(ctx context.Context, product *Product) error
	UpdateProduct(ctx context.Context, product *Product) error
	DeleteProduct(ctx context.Context, id uuid.UUID) error

	// GetProductsByLocation returns published products that are either
	// global (no targets) or targeted to this university/department.
	GetProductsByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]Product, error)
}
