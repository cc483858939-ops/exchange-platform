package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const requeueArticleEmbeddingMaxAttempts = 5

func main() {
	config.InitDatabaseConfig()

	count, err := requeueArticleEmbeddings(global.Db, config.ActiveEmbeddingVersion(), time.Now().UTC())
	if err != nil {
		log.Fatalf("failed to requeue article embeddings: %v", err)
	}
	log.Printf("article embedding requeue completed: version=%s queued=%d", config.ActiveEmbeddingVersion(), count)
}

func requeueArticleEmbeddings(db *gorm.DB, activeVersion string, now time.Time) (int, error) {
	if db == nil {
		return 0, errors.New("database is not initialized")
	}
	activeVersion = strings.TrimSpace(activeVersion)
	if activeVersion == "" {
		return 0, errors.New("active embedding version is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var articleIDs []uint
	if err := db.Table("articles AS a").
		Select("a.id").
		Joins("LEFT JOIN article_embeddings AS ae ON ae.article_id = a.id").
		Where("a.deleted_at IS NULL AND ae.version IS DISTINCT FROM ?", activeVersion).
		Order("a.id ASC").
		Pluck("a.id", &articleIDs).Error; err != nil {
		return 0, fmt.Errorf("find articles requiring embeddings: %w", err)
	}

	for _, articleID := range articleIDs {
		job := models.ArticleEmbeddingJob{
			ArticleID:     articleID,
			State:         models.ArticleEmbeddingJobQueued,
			AttemptCount:  0,
			MaxAttempts:   requeueArticleEmbeddingMaxAttempts,
			NextAttemptAt: now,
			LeasedBy:      "",
			LastError:     "",
			FinishedAt:    nil,
		}
		if err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "article_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"state":           models.ArticleEmbeddingJobQueued,
				"attempt_count":   0,
				"max_attempts":    requeueArticleEmbeddingMaxAttempts,
				"next_attempt_at": now,
				"lease_until":     nil,
				"leased_by":       "",
				"last_error":      "",
				"finished_at":     nil,
				"updated_at":      now,
			}),
		}).Create(&job).Error; err != nil {
			return 0, fmt.Errorf("requeue article %d: %w", articleID, err)
		}
	}

	return len(articleIDs), nil
}
