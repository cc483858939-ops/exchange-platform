package recommendation

import (
	"context"
	"errors"
	"fmt"

	"Go.exchange/models"

	"gorm.io/gorm"
)

type GormTraceRepository struct {
	db *gorm.DB
}

func NewGormTraceRepository(db *gorm.DB) (*GormTraceRepository, error) {
	if db == nil {
		return nil, errors.New("recommendation trace repository database is nil")
	}
	return &GormTraceRepository{db: db}, nil
}

func (r *GormTraceRepository) PersistServing(ctx context.Context, request models.RecommendationRequest, results []models.RecommendationResultTrace) error {
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return fmt.Errorf("persist recommendation serving trace: %w", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&request).Error; err != nil {
			return fmt.Errorf("create recommendation request: %w", err)
		}
		if len(results) == 0 {
			return nil
		}
		if err := tx.Create(&results).Error; err != nil {
			return fmt.Errorf("create recommendation result traces: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("persist recommendation serving trace transaction: %w", err)
	}
	return nil
}

var _ TraceRepository = (*GormTraceRepository)(nil)
