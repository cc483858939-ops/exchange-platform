package recommendation

import (
	"context"
	"errors"

	"Go.exchange/embeddingstate"
	"gorm.io/gorm"
)

type GormServingVersionProvider struct {
	db *gorm.DB
}

func NewGormServingVersionProvider(db *gorm.DB) (*GormServingVersionProvider, error) {
	if db == nil {
		return nil, errors.New("recommendation serving version database is nil")
	}
	return &GormServingVersionProvider{db: db}, nil
}

func (provider *GormServingVersionProvider) LoadServingVersion(ctx context.Context) (string, error) {
	if provider == nil || provider.db == nil {
		return "", errors.New("recommendation serving version database is nil")
	}
	return embeddingstate.LoadServingVersion(ctx, provider.db)
}

var _ ServingVersionProvider = (*GormServingVersionProvider)(nil)
