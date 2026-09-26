package recommendation

import (
	"errors"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

// NewGormServiceDependencies builds the persistence adapters used by the API
// and by explicit recommendation verification entry points. A nil Redis
// client leaves history fail-open, matching serving behavior when Redis is
// unavailable at startup.
func NewGormServiceDependencies(db *gorm.DB, redisClient *redis.Client) (ServiceDependencies, error) {
	if db == nil {
		return ServiceDependencies{}, errors.New("recommendation database is nil")
	}
	candidates, err := NewGormCandidateRepository(db)
	if err != nil {
		return ServiceDependencies{}, err
	}
	profiles, err := NewGormProfileRepository(db)
	if err != nil {
		return ServiceDependencies{}, err
	}
	traces, err := NewGormTraceRepository(db)
	if err != nil {
		return ServiceDependencies{}, err
	}
	var history HistoryStore
	if redisClient != nil {
		history, err = NewRedisHistoryStore(redisClient)
		if err != nil {
			return ServiceDependencies{}, err
		}
	}
	versionProvider, err := NewGormServingVersionProvider(db)
	if err != nil {
		return ServiceDependencies{}, err
	}
	return ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles, History: history, Traces: traces},
		ServingVersions:  versionProvider,
	}, nil
}
