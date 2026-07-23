package postgres

import (
	"context"

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

func (r *productRepository) GetProductsByLocation(ctx context.Context, universityID, departmentID, categoryID uuid.UUID) ([]domain.Product, error) {
	var products []domain.Product
	q := r.db.WithContext(ctx).
		Distinct("products.*").
		Joins("LEFT JOIN product_targets ON product_targets.product_id = products.id").
		Where("products.is_published = ?", true).
		Where("product_targets.id IS NULL OR (product_targets.university_id = ? AND (product_targets.department_id = ? OR product_targets.department_id = ?))",
			universityID, departmentID, uuid.Nil)

	if categoryID != uuid.Nil {
		q = q.Where("products.category_id = ?", categoryID)
	}

	err := q.Order("products.created_at desc").
		Preload("Merchant").
		Preload("Category").
		Find(&products).Error
	return products, err
}
