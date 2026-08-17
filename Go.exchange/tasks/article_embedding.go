package tasks

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	embeddingWorkerPollInterval = time.Second
	embeddingJobLeaseDuration   = 2 * time.Minute
	embeddingDefaultMaxAttempts = 5
)

var newArticleEmbedder = func(cfg config.EmbeddingConfig) (embeddings.Embedder, error) {
	return embeddings.NewOpenAICompatibleEmbedder(cfg)
}

func startArticleEmbeddingWorkers(ctx context.Context, wg *sync.WaitGroup) {
	if config.AppConfig == nil || !config.AppConfig.Embedding.Enabled {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		runArticleEmbeddingWorker(ctx)
	}()
}

func runArticleEmbeddingWorker(ctx context.Context) {
	if global.Db == nil || config.AppConfig == nil {
		return
	}
	embedder, err := newArticleEmbedder(config.AppConfig.Embedding)
	if err != nil {
		log.Printf("[ArticleEmbedding] worker disabled: %v", err)
		return
	}
	workerID := uuid.NewString()
	ticker := time.NewTicker(embeddingWorkerPollInterval)
	defer ticker.Stop()
	for {
		if err := recoverExpiredEmbeddingLeases(global.Db, time.Now().UTC()); err != nil {
			log.Printf("[ArticleEmbedding] recover leases: %v", err)
		}
		job, err := claimArticleEmbeddingJob(global.Db, time.Now().UTC(), workerID, embeddingJobLeaseDuration)
		if err != nil {
			log.Printf("[ArticleEmbedding] claim job: %v", err)
		} else if job != nil {
			if err := processArticleEmbeddingJob(ctx, *job, embedder); err != nil && ctx.Err() == nil {
				log.Printf("[ArticleEmbedding] process job %d: %v", job.ID, err)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func recoverExpiredEmbeddingLeases(db *gorm.DB, now time.Time) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	return db.Model(&models.ArticleEmbeddingJob{}).
		Where("state = ? AND lease_until IS NOT NULL AND lease_until <= ?", models.ArticleEmbeddingJobLeased, now).
		Updates(map[string]interface{}{
			"state": models.ArticleEmbeddingJobRetryWait, "lease_until": nil, "leased_by": "",
			"next_attempt_at": now, "last_error": "embedding lease expired",
		}).Error
}

func claimArticleEmbeddingJob(db *gorm.DB, now time.Time, workerID string, lease time.Duration) (*models.ArticleEmbeddingJob, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	if strings.TrimSpace(workerID) == "" || lease <= 0 {
		return nil, errors.New("embedding worker claim parameters are invalid")
	}
	var job models.ArticleEmbeddingJob
	found := false
	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("state IN ? AND next_attempt_at <= ?", []string{models.ArticleEmbeddingJobQueued, models.ArticleEmbeddingJobRetryWait}, now).
			Order("next_attempt_at ASC, id ASC").
			First(&job)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		leaseUntil := now.Add(lease)
		if err := tx.Model(&job).Updates(map[string]interface{}{
			"state": models.ArticleEmbeddingJobLeased, "attempt_count": gorm.Expr("attempt_count + 1"),
			"lease_until": leaseUntil, "leased_by": workerID, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		job.State = models.ArticleEmbeddingJobLeased
		job.AttemptCount++
		job.LeaseUntil = &leaseUntil
		job.LeasedBy = workerID
		found = true
		return nil
	})
	if err != nil || !found {
		return nil, err
	}
	return &job, nil
}

func processArticleEmbeddingJob(ctx context.Context, job models.ArticleEmbeddingJob, embedder embeddings.Embedder) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	if embedder == nil {
		return errors.New("embedding provider is nil")
	}
	activeVersion := config.ActiveEmbeddingVersion()
	if strings.TrimSpace(activeVersion) == "" {
		return errors.New("active embedding version is required")
	}
	var article models.Article
	if err := global.Db.First(&article, job.ArticleID).Error; err != nil {
		markArticleEmbeddingJobFailure(job, err)
		return err
	}
	text := embeddings.BuildArticleEmbeddingText(article.Title, article.Content)
	contentHash := embeddings.ArticleEmbeddingContentHash(article.Title, article.Content)
	var existing models.ArticleEmbedding
	lookupErr := global.Db.Where("article_id = ?", job.ArticleID).First(&existing).Error
	if lookupErr == nil && existing.ContentHash == contentHash && existing.Version == activeVersion {
		return markArticleEmbeddingJobSucceeded(job)
	}
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		markArticleEmbeddingJobFailure(job, lookupErr)
		return lookupErr
	}
	result, err := embedder.Embed(ctx, []string{text})
	if err != nil {
		markArticleEmbeddingJobFailure(job, err)
		return err
	}
	if len(result.Vectors) != 1 || len(result.Vectors[0]) == 0 || !validArticleEmbeddingVector(result.Vectors[0]) {
		err = errors.New("embedding provider returned an invalid single vector")
		markArticleEmbeddingJobFailure(job, err)
		return err
	}
	modelName := strings.TrimSpace(result.Model)
	if modelName == "" && config.AppConfig != nil {
		modelName = strings.TrimSpace(config.AppConfig.Embedding.Model)
	}
	if modelName == "" {
		err = errors.New("embedding provider returned an empty model")
		markArticleEmbeddingJobFailure(job, err)
		return err
	}
	vector := pgvector.NewVector(result.Vectors[0])
	now := time.Now().UTC()
	err = global.Db.Transaction(func(tx *gorm.DB) error {
		embedding := models.ArticleEmbedding{
			ArticleID: job.ArticleID, Version: activeVersion, Model: modelName,
			Dimensions: len(result.Vectors[0]), Embedding: vector, ContentHash: contentHash,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "article_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"version": activeVersion, "model": modelName, "dimensions": len(result.Vectors[0]),
				"embedding": vector, "content_hash": contentHash, "updated_at": now,
			}),
		}).Create(&embedding).Error; err != nil {
			return err
		}
		return tx.Model(&models.ArticleEmbeddingJob{}).
			Where("id = ? AND state = ? AND leased_by = ?", job.ID, models.ArticleEmbeddingJobLeased, job.LeasedBy).
			Updates(map[string]interface{}{
				"state": models.ArticleEmbeddingJobSucceeded, "lease_until": nil, "leased_by": "",
				"last_error": "", "finished_at": now, "updated_at": now,
			}).Error
	})
	if err != nil {
		markArticleEmbeddingJobFailure(job, err)
	}
	return err
}

func markArticleEmbeddingJobSucceeded(job models.ArticleEmbeddingJob) error {
	now := time.Now().UTC()
	return global.Db.Model(&models.ArticleEmbeddingJob{}).
		Where("id = ? AND state = ? AND leased_by = ?", job.ID, models.ArticleEmbeddingJobLeased, job.LeasedBy).
		Updates(map[string]interface{}{
			"state": models.ArticleEmbeddingJobSucceeded, "lease_until": nil, "leased_by": "",
			"last_error": "", "finished_at": now, "updated_at": now,
		}).Error
}

func markArticleEmbeddingJobFailure(job models.ArticleEmbeddingJob, cause error) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	now := time.Now().UTC()
	lastError := "embedding job failed"
	if cause != nil {
		lastError = cause.Error()
		if len(lastError) > 2000 {
			lastError = lastError[:2000]
		}
	}
	state := models.ArticleEmbeddingJobRetryWait
	backoffExponent := job.AttemptCount - 1
	if backoffExponent < 0 {
		backoffExponent = 0
	}
	nextAttemptAt := now.Add(time.Duration(1<<minInt(backoffExponent, 6)) * time.Second)
	var finishedAt *time.Time
	maxAttempts := job.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = embeddingDefaultMaxAttempts
	}
	if job.AttemptCount >= maxAttempts {
		state = models.ArticleEmbeddingJobDead
		nextAttemptAt = now
		finishedAt = &now
	}
	return global.Db.Model(&models.ArticleEmbeddingJob{}).
		Where("id = ? AND state = ? AND leased_by = ?", job.ID, models.ArticleEmbeddingJobLeased, job.LeasedBy).
		Updates(map[string]interface{}{
			"state": state, "next_attempt_at": nextAttemptAt, "lease_until": nil, "leased_by": "",
			"last_error": lastError, "finished_at": finishedAt, "updated_at": now,
		}).Error
}

func validArticleEmbeddingVector(vector []float32) bool {
	if len(vector) == 0 {
		return false
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
