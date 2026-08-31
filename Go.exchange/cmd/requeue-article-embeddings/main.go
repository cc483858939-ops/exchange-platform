package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
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
	StaleVersion int
	StaleContent int
	Published    int
}

type requeuePost struct {
	ID                   uint
	Title                string
	Preview              string
	Content              string
	EmbeddingPostID      *uint
	EmbeddingVersion     *string
	EmbeddingContentHash *string
}

type postEmbeddingReconciliationScanner interface {
	ListPage(context.Context, uint, int) ([]requeuePost, error)
}

type gormPostEmbeddingReconciliationScanner struct {
	db *gorm.DB
}

func (s gormPostEmbeddingReconciliationScanner) ListPage(ctx context.Context, lastID uint, pageSize int) ([]requeuePost, error) {
	if s.db == nil {
		return nil, errors.New("database is not initialized")
	}
	var rows []requeuePost
	err := s.db.WithContext(ctx).
		Table("posts AS p").
		Select("p.id, COALESCE(pa.title, '') AS title, COALESCE(pa.preview, '') AS preview, p.content, pe.post_id AS embedding_post_id, pe.version AS embedding_version, pe.content_hash AS embedding_content_hash").
		Joins("LEFT JOIN post_articles AS pa ON pa.post_id = p.id").
		Joins("LEFT JOIN post_embeddings AS pe ON pe.post_id = p.id").
		Where("p.deleted_at IS NULL AND p.id > ?", lastID).
		Order("p.id ASC").
		Limit(pageSize).
		Scan(&rows).Error
	return rows, err
}

func main() {
	config.InitDatabaseConfig()

	if config.AppConfig == nil || !config.AppConfig.Embedding.Enabled {
		log.Printf("post embedding reconciliation skipped: embedding is disabled")
		return
	}
	publisher, err := eventing.NewKafkaPublisher(config.AppConfig.Kafka)
	if err != nil {
		log.Fatalf("initialize post embedding Kafka publisher: %v", err)
	}
	defer publisher.Close()

	stats, err := requeuePostEmbeddings(context.Background(), global.Db, publisher, config.ActiveEmbeddingVersion(), time.Now().UTC())
	if err != nil {
		log.Fatalf("failed to requeue post embeddings: %v", err)
	}
	log.Printf("post embedding reconciliation completed: scanned=%d missing=%d stale_version=%d stale_content=%d published=%d",
		stats.Scanned, stats.Missing, stats.StaleVersion, stats.StaleContent, stats.Published)
}

func requeuePostEmbeddings(ctx context.Context, db *gorm.DB, publisher eventing.BatchPublisher, activeVersion string, now time.Time) (requeueStats, error) {
	if db == nil {
		return requeueStats{}, errors.New("database is not initialized")
	}
	return reconcilePostEmbeddings(ctx, gormPostEmbeddingReconciliationScanner{db: db}, publisher, activeVersion, now)
}

func reconcilePostEmbeddings(ctx context.Context, scanner postEmbeddingReconciliationScanner, publisher eventing.BatchPublisher, activeVersion string, now time.Time) (requeueStats, error) {
	stats := requeueStats{}
	if scanner == nil {
		return stats, errors.New("post embedding reconciliation scanner is nil")
	}
	if ctx == nil {
		return stats, errors.New("post embedding reconciliation context is nil")
	}
	activeVersion = strings.TrimSpace(activeVersion)
	if activeVersion == "" {
		return stats, errors.New("active embedding version is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var lastID uint
	for {
		rows, err := scanner.ListPage(ctx, lastID, requeuePostEmbeddingPageSize)
		if err != nil {
			return stats, fmt.Errorf("scan posts requiring embeddings: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		stats.Scanned += len(rows)
		events := make([]eventing.Envelope, 0, len(rows))

		for _, row := range rows {
			currentHash := embeddings.PostEmbeddingContentHash(row.Title, row.Preview, row.Content)
			if row.EmbeddingPostID != nil && row.EmbeddingVersion != nil && row.EmbeddingContentHash != nil &&
				*row.EmbeddingVersion == activeVersion && *row.EmbeddingContentHash == currentHash {
				continue
			}
			if row.EmbeddingPostID == nil {
				stats.Missing++
			} else if row.EmbeddingVersion == nil || *row.EmbeddingVersion != activeVersion {
				stats.StaleVersion++
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
