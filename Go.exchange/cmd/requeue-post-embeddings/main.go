package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/metrics"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const requeuePostEmbeddingPageSize = 500

type requeueStats struct {
	Scanned      int
	Missing      int
	StaleContent int
	Published    int
}

type requeuePost struct {
	ID                   uint
	Content              string
	EmbeddingPostID      *uint
	EmbeddingContentHash *string
}

type postEmbeddingReconciliationScanner interface {
	ListPage(context.Context, uint, int, string, time.Time, time.Time) ([]requeuePost, error)
}

type gormPostEmbeddingReconciliationScanner struct {
	db *gorm.DB
}

func (s gormPostEmbeddingReconciliationScanner) ListPage(ctx context.Context, lastID uint, pageSize int, buildVersion string, windowStart, windowEnd time.Time) ([]requeuePost, error) {
	if s.db == nil {
		return nil, errors.New("database is not initialized")
	}
	var rows []requeuePost
	err := s.db.WithContext(ctx).
		Table("posts AS p").
		Select("p.id, p.content, pe.post_id AS embedding_post_id, pe.content_hash AS embedding_content_hash").
		Joins("LEFT JOIN post_embeddings AS pe ON pe.post_id = p.id AND pe.version = ?", buildVersion).
		Where("p.deleted_at IS NULL AND p.visibility = ? AND p.reply_to_post_id IS NULL", "public").
		Where("p.created_at >= ? AND p.created_at <= ?", windowStart.UTC(), windowEnd.UTC()).
		Where("p.id > ?", lastID).
		Where(`EXISTS (
			SELECT 1 FROM users AS u
			WHERE u.id = p.author_id AND u.deleted_at IS NULL
		)`).
		Order("p.id ASC").
		Limit(pageSize).
		Scan(&rows).Error
	return rows, err
}

func main() {
	config.InitWorkerDatabaseConfig()
	defer config.CloseDatabasePools()

	if config.AppConfig == nil || !config.AppConfig.Embedding.Enabled {
		log.Printf("post embedding reconciliation skipped: embedding is disabled")
		return
	}
	publisher, err := eventing.NewKafkaPublisher(config.AppConfig.Kafka)
	if err != nil {
		log.Fatalf("initialize post embedding Kafka publisher: %v", err)
	}
	defer publisher.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	now := time.Now().UTC()
	primaryWindowDays := config.NormalizeRecommendationPrimaryWindowDays(config.AppConfig.Recommendation.PrimaryWindowDays)
	stats, err := requeuePostEmbeddings(ctx, global.WorkerDb, publisher, config.BuildEmbeddingVersion(), now, primaryWindowDays)
	if err != nil {
		log.Fatalf("failed to requeue post embeddings: %v", err)
	}
	windowStart, windowEnd := config.RecommendationPrimaryWindowBounds(now, primaryWindowDays)
	log.Printf("post embedding reconciliation completed: primary_window_days=%d window_start=%s window_end=%s scanned=%d missing=%d stale_content=%d published=%d",
		primaryWindowDays, windowStart.Format(time.RFC3339Nano), windowEnd.Format(time.RFC3339Nano),
		stats.Scanned, stats.Missing, stats.StaleContent, stats.Published)
}

func requeuePostEmbeddings(ctx context.Context, db *gorm.DB, publisher eventing.BatchPublisher, buildVersion string, now time.Time, primaryWindowDays int) (requeueStats, error) {
	if db == nil {
		return requeueStats{}, errors.New("database is not initialized")
	}
	return reconcilePostEmbeddings(ctx, gormPostEmbeddingReconciliationScanner{db: db}, publisher, buildVersion, now, primaryWindowDays)
}

func reconcilePostEmbeddings(ctx context.Context, scanner postEmbeddingReconciliationScanner, publisher eventing.BatchPublisher, buildVersion string, now time.Time, primaryWindowDays int) (requeueStats, error) {
	stats := requeueStats{}
	if scanner == nil {
		return stats, errors.New("post embedding reconciliation scanner is nil")
	}
	if ctx == nil {
		return stats, errors.New("post embedding reconciliation context is nil")
	}
	buildVersion = strings.TrimSpace(buildVersion)
	if buildVersion == "" {
		return stats, errors.New("build embedding version is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	windowStart, windowEnd := config.RecommendationPrimaryWindowBounds(now, primaryWindowDays)

	var lastID uint
	for {
		rows, err := scanner.ListPage(ctx, lastID, requeuePostEmbeddingPageSize, buildVersion, windowStart, windowEnd)
		if err != nil {
			return stats, fmt.Errorf("scan posts requiring embeddings: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		stats.Scanned += len(rows)
		events := make([]eventing.Envelope, 0, len(rows))

		for _, row := range rows {
			currentHash := embeddings.PostEmbeddingContentHash(row.Content)
			if row.EmbeddingPostID != nil && row.EmbeddingContentHash != nil && *row.EmbeddingContentHash == currentHash {
				continue
			}
			if row.EmbeddingPostID == nil {
				stats.Missing++
			} else {
				stats.StaleContent++
			}
			event, err := eventing.NewPostEmbeddingRequestedEnvelope(uuid.NewString(), row.ID, now)
			if err != nil {
				return stats, fmt.Errorf("build post %d embedding event: %w", row.ID, err)
			}
			events = append(events, event)
		}
		if len(events) > 0 {
			if publisher == nil {
				metrics.RecordPostEmbeddingPublishFailure("requeue")
				return stats, errors.New("post embedding publisher is nil")
			}
			if err := publisher.PublishBatch(ctx, events); err != nil {
				metrics.RecordPostEmbeddingPublishFailure("requeue")
				return stats, fmt.Errorf("publish post embedding reconciliation page: %w", err)
			}
			stats.Published += len(events)
		}
		lastID = rows[len(rows)-1].ID
	}
	return stats, nil
}
