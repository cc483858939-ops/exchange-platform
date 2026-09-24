package embeddingstate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"Go.exchange/models"

	"gorm.io/gorm"
)

const (
	ServingStateID        uint = 1
	DefaultServingVersion      = "post_embedding_v1"
)

func LoadServingVersion(ctx context.Context, db *gorm.DB) (string, error) {
	if ctx == nil {
		return "", errors.New("serving state context is nil")
	}
	if db == nil {
		return "", errors.New("database is not initialized")
	}
	var state models.EmbeddingServingState
	err := db.WithContext(ctx).Select("serving_version").Where("id = ?", ServingStateID).Take(&state).Error
	if err != nil {
		return "", fmt.Errorf("load embedding serving state: %w", err)
	}
	version := strings.TrimSpace(state.ServingVersion)
	if version == "" {
		return "", errors.New("embedding serving state version is blank")
	}
	return version, nil
}

func SetServingVersion(ctx context.Context, db *gorm.DB, version string) error {
	if ctx == nil {
		return errors.New("serving state context is nil")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return errors.New("serving version must not be blank")
	}
	if db == nil {
		return errors.New("database is not initialized")
	}
	result := db.WithContext(ctx).Exec(`
UPDATE embedding_serving_state
SET serving_version = ?, updated_at = NOW()
WHERE id = ?`, version, ServingStateID)
	if result.Error != nil {
		return fmt.Errorf("set embedding serving version: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("set embedding serving version: expected singleton row %d, updated %d rows", ServingStateID, result.RowsAffected)
	}
	return nil
}
