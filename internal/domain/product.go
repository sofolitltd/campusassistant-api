package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Product is an item listed in the Campus Marketplace, owned by a Merchant
// (in-house products belong to the synthetic platform Merchant). Targets
// scope visibility to a university/department, same pattern as Skill.
type Product struct {
	Base
	MerchantID  uuid.UUID           `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Merchant    Merchant            `gorm:"foreignKey:MerchantID;references:ID" json:"merchant,omitempty"`
	Title       string              `gorm:"size:255;not null" json:"title"`
	Description string              `gorm:"type:text" json:"description"`
	Price       int                 `gorm:"not null" json:"price"` // smallest currency unit (poisha)
	Stock       int                 `gorm:"default:0" json:"stock"`
	ImageURLs   pq.StringArray      `gorm:"type:text[];default:'{}'" json:"image_urls"`
	CategoryID  uuid.UUID           `gorm:"type:uuid;index" json:"category_id"` // uuid.Nil = uncategorized
	Category    MarketplaceCategory `gorm:"foreignKey:CategoryID;references:ID" json:"category,omitempty"`
	IsPublished bool                `gorm:"default:false;index" json:"is_published"`
	Targets     []ProductTarget     `gorm:"foreignKey:ProductID;constraint:OnDelete:CASCADE" json:"targets"`

	// Review aggregates. Read-only through GORM ("->") so a product edit can
	// never overwrite them; ReviewService recomputes them with raw SQL.
	RatingAvg   float64 `gorm:"->;default:0" json:"rating_avg"`
	RatingCount int     `gorm:"->;default:0" json:"rating_count"`
	// ViewCount counts product-page opens (see ProductHandler.RecordView).
	ViewCount int `gorm:"->;default:0" json:"view_count"`
	// FeaturedUntil, when in the future, pins the product to the top of the
	// market and the home carousel. Admin-granted only, through its own
	// endpoint — read-only here so a seller can't set it from a product edit.
	FeaturedUntil *time.Time `gorm:"->;index" json:"featured_until,omitempty"`
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
	// Pass categoryID != uuid.Nil to filter by category.
	GetProductsByLocation(ctx context.Context, universityID, departmentID, categoryID uuid.UUID) ([]Product, error)

	// SearchProducts is GetProductsByLocation plus text search, price and
	// stock filters, sorting and pagination. It also returns the total number
	// of matches ignoring Limit/Offset.
	SearchProducts(ctx context.Context, f ProductFilter) ([]Product, int64, error)

	// GetPublishedByIDs returns the published products among ids (any
	// targeting), for revalidating a cart or wishlist. Missing, unpublished
	// and deleted products are simply absent from the result.
	GetPublishedByIDs(ctx context.Context, ids []uuid.UUID) ([]Product, error)
}

// ProductSort orders a product listing.
type ProductSort string

const (
	ProductSortNewest    ProductSort = "new"
	ProductSortPriceAsc  ProductSort = "price_asc"
	ProductSortPriceDesc ProductSort = "price_desc"
	ProductSortTopRated  ProductSort = "top_rated"
	ProductSortPopular   ProductSort = "popular"
)

// ProductFilter describes a marketplace browse/search request. Zero values
// mean "no filter"; Limit 0 means "no limit".
type ProductFilter struct {
	UniversityID uuid.UUID
	DepartmentID uuid.UUID
	CategoryID   uuid.UUID
	MerchantID   uuid.UUID
	Query        string
	MinPrice     int
	MaxPrice     int
	InStock      bool
	FeaturedOnly bool
	Sort         ProductSort
	Limit        int
	Offset       int
}
