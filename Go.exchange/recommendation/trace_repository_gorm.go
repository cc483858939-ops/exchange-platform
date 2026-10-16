package recommendation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrTraceConflict = errors.New("recommendation serving trace conflicts with an existing request")

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
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&request)
		if created.Error != nil {
			return fmt.Errorf("create recommendation request: %w", created.Error)
		}
		if created.RowsAffected == 0 {
			var existing models.RecommendationRequest
			if err := tx.Where("request_id = ?", request.RequestID).First(&existing).Error; err != nil {
				return fmt.Errorf("load existing recommendation request: %w", err)
			}
			var existingResults []models.RecommendationResultTrace
			if err := tx.Where("request_id = ?", request.RequestID).Order("position ASC").Find(&existingResults).Error; err != nil {
				return fmt.Errorf("load existing recommendation result traces: %w", err)
			}
			if !sameRecommendationTrace(existing, request, existingResults, results) {
				return ErrTraceConflict
			}
			return nil
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

func sameRecommendationTrace(existing, incoming models.RecommendationRequest, existingResults, incomingResults []models.RecommendationResultTrace) bool {
	existing.CreatedAt = existing.CreatedAt.UTC().Truncate(time.Microsecond)
	incoming.CreatedAt = incoming.CreatedAt.UTC().Truncate(time.Microsecond)
	if !reflect.DeepEqual(existing, incoming) || len(existingResults) != len(incomingResults) {
		return false
	}
	for index := range existingResults {
		left, right := existingResults[index], incomingResults[index]
		left.CreatedAt = left.CreatedAt.UTC().Truncate(time.Microsecond)
		right.CreatedAt = right.CreatedAt.UTC().Truncate(time.Microsecond)
		left.ExpiresAt = left.ExpiresAt.UTC().Truncate(time.Microsecond)
		right.ExpiresAt = right.ExpiresAt.UTC().Truncate(time.Microsecond)
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

var _ TraceRepository = (*GormTraceRepository)(nil)
