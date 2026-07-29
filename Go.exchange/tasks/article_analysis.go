package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	articleAnalysisWorkerCount       = 2
	articleAnalysisDispatchBatchSize = 50
)

var newArticleAnalyzer = func() (ArticleAnalyzer, error) {
	return NewEINOArticleAnalysisAgent(config.AppConfig.AI)
}

var invalidateArticleDetailCache = controllers.InvalidateArticleDetailCacheByID

func startArticleAnalysisWorkers(ctx context.Context, wg *sync.WaitGroup) {
	startArticleAnalysisDispatcher(ctx, wg)
	for i := 0; i < articleAnalysisWorkerCount; i++ {
		wg.Add(1)
		go func(workerNumber int) {
			defer wg.Done()
			runArticleAnalysisConsumerWithRetry(ctx, fmt.Sprintf("%s-analysis-%d", workerIdentity(), workerNumber))
		}(i + 1)
	}
	log.Printf("[ArticleAnalysis] started %d Kafka consumers", articleAnalysisWorkerCount)
}

func startArticleAnalysisDispatcher(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if err := recoverExpiredArticleAnalysisLeases(time.Now().UTC()); err != nil {
				log.Printf("[ArticleAnalysis] recover expired leases: %v", err)
			}
			if err := dispatchDueArticleAnalysisJobs(time.Now().UTC()); err != nil {
				log.Printf("[ArticleAnalysis] dispatch due jobs: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func runArticleAnalysisConsumerWithRetry(ctx context.Context, workerID string) {
	for {
		runArticleAnalysisConsumer(ctx, workerID)
		if ctx.Err() != nil {
			return
		}
		log.Printf("[ArticleAnalysis] consumer %s stopped; retrying in 2s", workerID)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
func runArticleAnalysisConsumer(ctx context.Context, workerID string) {
	analyzer, err := newArticleAnalyzer()
	if err != nil {
		log.Printf("[ArticleAnalysis] consumer disabled: %v", err)
		return
	}

	reader, err := eventing.NewKafkaReader(config.AppConfig.Kafka, config.AppConfig.Kafka.ArticleAnalysisTopic, config.AppConfig.Kafka.ArticleAnalysisGroupID)
	if err != nil {
		log.Printf("[ArticleAnalysis] create Kafka reader: %v", err)
		return
	}
	articleAnalysisConsumers.Add(1)
	defer articleAnalysisConsumers.Add(-1)
	defer reader.Close()

	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[ArticleAnalysis] fetch Kafka message: %v", err)
			}
			return
		}

		event, err := eventing.DecodeEnvelope(message.Value)
		if err != nil {
			log.Printf("[ArticleAnalysis] discard malformed Kafka message: %v", err)
		} else if event.Type == eventing.EventTypeArticleAnalysisRequested {
			if err := processArticleAnalysisEvent(ctx, analyzer, workerID, event); err != nil {
				log.Printf("[ArticleAnalysis] handle event: %v", err)
				return
			}
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			log.Printf("[ArticleAnalysis] commit Kafka message: %v", err)
			return
		}
	}
}

func processArticleAnalysisEvent(ctx context.Context, analyzer ArticleAnalyzer, workerID string, event eventing.Envelope) error {
	var payload eventing.ArticleAnalysisRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode article analysis payload: %w", err)
	}

	job, claimed, err := claimArticleAnalysisJob(payload, workerID, time.Now().UTC())
	if err != nil || !claimed {
		return err
	}

	var article models.Article
	if err := global.Db.First(&article, job.ArticleID).Error; err != nil {
		return finishArticleAnalysisFailure(job, fmt.Errorf("load article: %w", err), time.Now().UTC())
	}

	result, err := analyzer.Analyze(ctx, article)
	if err != nil {
		return finishArticleAnalysisFailure(job, err, time.Now().UTC())
	}
	return finishArticleAnalysisSuccess(job, result, time.Now().UTC())
}

func claimArticleAnalysisJob(payload eventing.ArticleAnalysisRequestedPayload, workerID string, now time.Time) (models.ArticleAnalysisJob, bool, error) {
	var claimed models.ArticleAnalysisJob
	didClaim := false
	leaseUntil := now.Add(jobLeaseDuration())

	err := global.Db.Transaction(func(tx *gorm.DB) error {
		var job models.ArticleAnalysisJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, payload.JobID).Error; err != nil {
			return err
		}
		if job.ArticleID != payload.ArticleID || job.State != models.ArticleAnalysisJobQueued && job.State != models.ArticleAnalysisJobRetryWait || job.NextAttemptAt.After(now) {
			return nil
		}

		updates := map[string]interface{}{
			"state":         models.ArticleAnalysisJobLeased,
			"attempt_count": job.AttemptCount + 1,
			"lease_until":   leaseUntil,
			"leased_by":     workerID,
			"last_error":    "",
		}
		if err := tx.Model(&models.ArticleAnalysisJob{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Article{}).Where("id = ?", job.ArticleID).Update("analysis_state", consts.ArticleAnalysisStateProcessing).Error; err != nil {
			return err
		}
		job.State = models.ArticleAnalysisJobLeased
		job.AttemptCount++
		job.LeaseUntil = &leaseUntil
		job.LeasedBy = workerID
		claimed = job
		didClaim = true
		return nil
	})
	if err == nil && didClaim {
		invalidateArticleDetailCacheBestEffort(claimed.ArticleID)
	}
	return claimed, didClaim, err
}

func finishArticleAnalysisSuccess(job models.ArticleAnalysisJob, result ArticleAnalysisResult, now time.Time) error {
	return global.Db.Transaction(func(tx *gorm.DB) error {
		var current models.ArticleAnalysisJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, job.ID).Error; err != nil {
			return err
		}
		if current.State != models.ArticleAnalysisJobLeased {
			return fmt.Errorf("analysis job %d is no longer leased", current.ID)
		}
		if err := tx.Model(&models.Article{}).Where("id = ?", job.ArticleID).Updates(map[string]interface{}{
			"summary":          result.Summary,
			"tags":             result.Tags,
			"category":         result.Category,
			"analysis_state":   consts.ArticleAnalysisStateCompleted,
			"analysis_version": consts.ArticleAnalysisVersionV1,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&models.ArticleAnalysisJob{}).Where("id = ?", current.ID).Updates(map[string]interface{}{
			"state":       models.ArticleAnalysisJobSucceeded,
			"lease_until": nil,
			"leased_by":   "",
			"finished_at": now,
			"last_error":  "",
		}).Error
	})
}
func finishArticleAnalysisFailure(job models.ArticleAnalysisJob, analysisErr error, now time.Time) error {
	errText := analysisErr.Error()
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		var current models.ArticleAnalysisJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, job.ID).Error; err != nil {
			return err
		}
		if current.State != models.ArticleAnalysisJobLeased {
			return nil
		}

		if current.AttemptCount >= current.MaxAttempts {
			if err := tx.Model(&models.ArticleAnalysisJob{}).Where("id = ?", current.ID).Updates(map[string]interface{}{
				"state":       models.ArticleAnalysisJobDead,
				"lease_until": nil,
				"leased_by":   "",
				"last_error":  errText,
				"finished_at": now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.Article{}).Where("id = ?", current.ArticleID).Update("analysis_state", consts.ArticleAnalysisStateFailed).Error; err != nil {
				return err
			}
			deadEvent, err := eventing.NewOutboxEvent(eventing.EventTypeArticleAnalysisDead, "article", fmt.Sprintf("%d", current.ArticleID), eventing.ArticleAnalysisRequestedPayload{
				JobID:           current.ID,
				ArticleID:       current.ArticleID,
				AnalysisVersion: consts.ArticleAnalysisVersionV1,
			})
			if err != nil {
				return err
			}
			return eventing.AddOutboxEvent(tx, deadEvent)
		}

		retryAt := now.Add(articleAnalysisRetryDelay(current.AttemptCount))
		if err := tx.Model(&models.ArticleAnalysisJob{}).Where("id = ?", current.ID).Updates(map[string]interface{}{
			"state":              models.ArticleAnalysisJobRetryWait,
			"next_attempt_at":    retryAt,
			"lease_until":        nil,
			"leased_by":          "",
			"last_error":         errText,
			"last_dispatched_at": nil,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&models.Article{}).Where("id = ?", current.ArticleID).Update("analysis_state", consts.ArticleAnalysisStatePending).Error
	})
	if err == nil {
		invalidateArticleDetailCacheBestEffort(job.ArticleID)
	}
	return err
}

func dispatchDueArticleAnalysisJobs(now time.Time) error {
	return global.Db.Transaction(func(tx *gorm.DB) error {
		var jobs []models.ArticleAnalysisJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("state IN ? AND next_attempt_at <= ? AND (last_dispatched_at IS NULL OR last_dispatched_at < next_attempt_at)", []string{models.ArticleAnalysisJobQueued, models.ArticleAnalysisJobRetryWait}, now).
			Order("next_attempt_at ASC").
			Limit(articleAnalysisDispatchBatchSize).
			Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			event, err := eventing.NewArticleAnalysisRequested(job, consts.ArticleAnalysisVersionV1)
			if err != nil {
				return err
			}
			if err := eventing.AddOutboxEvent(tx, event); err != nil {
				return err
			}
			if err := tx.Model(&models.ArticleAnalysisJob{}).Where("id = ?", job.ID).Update("last_dispatched_at", now).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func recoverExpiredArticleAnalysisLeases(now time.Time) error {
	return global.Db.Transaction(func(tx *gorm.DB) error {
		var expired []models.ArticleAnalysisJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("state = ? AND lease_until < ?", models.ArticleAnalysisJobLeased, now).
			Find(&expired).Error; err != nil {
			return err
		}
		for _, job := range expired {
			if err := tx.Model(&models.ArticleAnalysisJob{}).Where("id = ?", job.ID).Updates(map[string]interface{}{
				"state":              models.ArticleAnalysisJobRetryWait,
				"next_attempt_at":    now,
				"lease_until":        nil,
				"leased_by":          "",
				"last_dispatched_at": nil,
				"last_error":         "worker lease expired",
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.Article{}).Where("id = ?", job.ArticleID).Update("analysis_state", consts.ArticleAnalysisStatePending).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func articleAnalysisRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute << min(attempt-1, 6)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

func jobLeaseDuration() time.Duration {
	seconds := config.AppConfig.Kafka.JobLeaseSeconds
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func workerIdentity() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "worker"
	}
	return host
}

func invalidateArticleDetailCacheBestEffort(articleID uint) {
	if err := invalidateArticleDetailCache(articleID); err != nil {
		log.Printf("[ArticleAnalysis] invalidate article %d cache: %v", articleID, err)
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
