package servicecatalog

import (
	"context"
	"errors"
	"strings"
	"time"

	"novaapm/internal/database"
	"novaapm/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ProductRepository struct {
	store database.ProductStore
}

func NewProductRepository(store database.ProductStore) ProductRepository {
	return ProductRepository{store: store}
}

func (r ProductRepository) List(ctx context.Context) ([]Product, error) {
	var products []Product
	if err := r.store.FindAll(ctx, &products); err != nil {
		return nil, err
	}
	return products, nil
}

func (r ProductRepository) Get(ctx context.Context, id string) (Product, error) {
	var product Product
	err := r.store.FindByID(ctx, strings.TrimSpace(id), &product)
	return product, err
}

func (r ProductRepository) Create(ctx context.Context, product Product) (Product, error) {
	product.Name = strings.TrimSpace(product.Name)
	product.DisplayName = strings.TrimSpace(product.DisplayName)
	product.Description = strings.TrimSpace(product.Description)
	if product.Name == "" {
		return Product{}, apperr.InvalidRequest("产品名称不能为空")
	}
	product.ID = primitive.NewObjectID().Hex()
	if product.DisplayName == "" {
		product.DisplayName = product.Name
	}
	product.Status = "active"
	now := time.Now().UTC()
	product.CreatedAt = now
	product.UpdatedAt = now
	for attempts := 0; attempts < 8; attempts++ {
		projectID, err := generateTenantID()
		if err != nil {
			return Product{}, err
		}
		product.ProjectID = projectID
		if err := r.store.Insert(ctx, product); err == nil {
			return product, nil
		} else if !errors.Is(err, database.ErrConflict) {
			return Product{}, err
		}
		products, listErr := r.List(ctx)
		if listErr != nil {
			return Product{}, listErr
		}
		for _, existing := range products {
			if strings.EqualFold(existing.Name, product.Name) {
				return Product{}, apperr.Conflict("产品名称已存在")
			}
		}
	}
	return Product{}, apperr.Conflict("产品 ProjectID 分配冲突，请重试")
}
