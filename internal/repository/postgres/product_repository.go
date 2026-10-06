package postgres

import (
	"context"
	"gorm.io/gorm/clause"
	"strings"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type productRepository struct {
	db *gorm.DB
}

func NewProductRepository(db *gorm.DB) domain.ProductRepository {
	return &productRepository{db: db}
}

func (r *productRepository) GetAllProducts(ctx context.Context, merchantID uuid.UUID) ([]domain.Product, error) {
	var products []domain.Product
	q := r.db.WithContext(ctx).Preload("Targets").Preload("Merchant").Preload("Category")
	if merchantID != uuid.Nil {
		q = q.Where("merchant_id = ?", merchantID)
	}
	err := q.Order("created_at desc").Find(&products).Error
	return products, err
}

func (r *productRepository) GetProductByID(ctx context.Context, id uuid.UUID) (*domain.Product, error) {
	var product domain.Product
	err := r.db.WithContext(ctx).Preload("Targets").Preload("Merchant").Preload("Category").First(&product, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *productRepository) CreateProduct(ctx context.Context, product *domain.Product) error {
	return r.db.WithContext(ctx).Create(product).Error
}

func (r *productRepository) UpdateProduct(ctx context.Context, product *domain.Product) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete old targets first to handle multi-select updates correctly
		// (same reasoning as skillRepository.UpdateSkill).
		if err := tx.Where("product_id = ?", product.ID).Delete(&domain.ProductTarget{}).Error; err != nil {
			return err
		}
		return tx.Save(product).Error
	})
}

func (r *productRepository) DeleteProduct(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Product{}, id).Error
}

// likePattern turns user text into a safe, case-insensitive LIKE pattern.
func likePattern(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
	return "%" + s + "%"
}

func (r *productRepository) SearchProducts(ctx context.Context, f domain.ProductFilter) ([]domain.Product, int64, error) {
	now := time.Now()
	base := func() *gorm.DB {
		// Targeting by subquery (not a join) so each product appears once and
		// the result can be ordered by computed expressions.
		q := r.db.WithContext(ctx).Model(&domain.Product{}).
			Where("products.is_published = ?", true).
			Where(`(NOT EXISTS (SELECT 1 FROM product_targets pt WHERE pt.product_id = products.id AND pt.deleted_at IS NULL)
				OR EXISTS (SELECT 1 FROM product_targets pt WHERE pt.product_id = products.id AND pt.deleted_at IS NULL
					AND pt.university_id = ? AND (pt.department_id = ? OR pt.department_id = ?)))`,
				f.UniversityID, f.DepartmentID, uuid.Nil)
		if f.CategoryID != uuid.Nil {
			q = q.Where("products.category_id = ?", f.CategoryID)
		}
		if f.MerchantID != uuid.Nil {
			q = q.Where("products.merchant_id = ?", f.MerchantID)
		}
		if strings.TrimSpace(f.Query) != "" {
			p := likePattern(f.Query)
			q = q.Where(`(LOWER(products.title) LIKE ? ESCAPE '\' OR LOWER(products.description) LIKE ? ESCAPE '\')`, p, p)
		}
		if f.MinPrice > 0 {
			q = q.Where("products.price >= ?", f.MinPrice)
		}
		if f.MaxPrice > 0 {
			q = q.Where("products.price <= ?", f.MaxPrice)
		}
		if f.InStock {
			q = q.Where("products.stock > 0")
		}
		if f.FeaturedOnly {
			q = q.Where("products.featured_until IS NOT NULL AND products.featured_until > ?", now)
		}
		return q
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	order := "products.created_at desc"
	switch f.Sort {
	case domain.ProductSortPriceAsc:
		order = "products.price asc, products.created_at desc"
	case domain.ProductSortPriceDesc:
		order = "products.price desc, products.created_at desc"
	case domain.ProductSortTopRated:
		order = "products.rating_avg desc, products.rating_count desc, products.created_at desc"
	case domain.ProductSortPopular:
		order = "products.view_count desc, products.created_at desc"
	}

	var products []domain.Product
	q := base()
	if f.Sort == "" || f.Sort == domain.ProductSortNewest {
		// Featured products lead the default ordering. One expression, because
		// GORM ignores plain column orders added next to an expression order.
		q = q.Order(clause.OrderBy{Expression: clause.Expr{
			SQL: "CASE WHEN products.featured_until IS NOT NULL AND products.featured_until > ? THEN 0 ELSE 1 END, " +
				order + ", products.id",
			Vars: []interface{}{now}, WithoutParentheses: true,
		}})
	} else {
		q = q.Order(order).Order("products.id")
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit).Offset(f.Offset)
	}
	err := q.Preload("Merchant").Preload("Category").Find(&products).Error
	return products, total, err
}

func (r *productRepository) GetProductsByLocation(ctx context.Context, universityID, departmentID, categoryID uuid.UUID) ([]domain.Product, error) {
	products, _, err := r.SearchProducts(ctx, domain.ProductFilter{
		UniversityID: universityID, DepartmentID: departmentID, CategoryID: categoryID,
	})
	return products, err
}

func (r *productRepository) GetPublishedByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.Product, error) {
	var products []domain.Product
	if len(ids) == 0 {
		return products, nil
	}
	err := r.db.WithContext(ctx).
		Where("id IN ? AND is_published = ?", ids, true).
		Preload("Merchant").Preload("Category").
		Find(&products).Error
	return products, err
}
