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

const requeueArticleEmbeddingPageSize = 500

type requeueStats struct {
	Scanned      int
	Missing      int
	StaleVersion int
	StaleContent int
	Published    int
}

type requeueArticle struct {
	ID                   uint
	Title                string
	Content              string
	EmbeddingArticleID   *uint
	EmbeddingVersion     *string
	EmbeddingContentHash *string
}

func main() {
	config.InitDatabaseConfig()

	if config.AppConfig == nil || !config.AppConfig.Embedding.Enabled {
		log.Printf("article embedding reconciliation skipped: embedding is disabled")
		return
	}
	publisher, err := eventing.NewKafkaPublisher(config.AppConfig.Kafka)
	if err != nil {
		log.Fatalf("initialize article embedding Kafka publisher: %v", err)
	}
	defer publisher.Close()

	stats, err := requeueArticleEmbeddings(global.Db, publisher, config.ActiveEmbeddingVersion(), time.Now().UTC())
	if err != nil {
		log.Fatalf("failed to requeue article embeddings: %v", err)
	}
	log.Printf("article embedding reconciliation completed: scanned=%d missing=%d stale_version=%d stale_content=%d published=%d",
		stats.Scanned, stats.Missing, stats.StaleVersion, stats.StaleContent, stats.Published)
}

func requeueArticleEmbeddings(db *gorm.DB, publisher eventing.BatchPublisher, activeVersion string, now time.Time) (requeueStats, error) {
	stats := requeueStats{}
	if db == nil {
		return stats, errors.New("database is not initialized")
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
		var rows []requeueArticle
		if err := db.Table("articles AS a").
			Select("a.id, a.title, a.content, ae.article_id AS embedding_article_id, ae.version AS embedding_version, ae.content_hash AS embedding_content_hash").
			Joins("LEFT JOIN article_embeddings AS ae ON ae.article_id = a.id").
			Where("a.deleted_at IS NULL AND a.publication_state = ? AND a.published_at IS NOT NULL AND a.id > ?", "published", lastID).
			Order("a.id ASC").
			Limit(requeueArticleEmbeddingPageSize).
			Scan(&rows).Error; err != nil {
			return stats, fmt.Errorf("scan articles requiring embeddings: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		stats.Scanned += len(rows)
		events := make([]eventing.Envelope, 0, len(rows))

		for _, row := range rows {
			currentHash := embeddings.ArticleEmbeddingContentHash(row.Title, row.Content)
			if row.EmbeddingArticleID != nil && row.EmbeddingVersion != nil && row.EmbeddingContentHash != nil &&
				*row.EmbeddingVersion == activeVersion && *row.EmbeddingContentHash == currentHash {
				continue
			}
			if row.EmbeddingArticleID == nil {
				stats.Missing++
			} else if row.EmbeddingVersion == nil || *row.EmbeddingVersion != activeVersion {
				stats.StaleVersion++
			} else {
				stats.StaleContent++
			}
			event, err := eventing.NewArticleEmbeddingRequestedEnvelope(uuid.NewString(), row.ID, now)
			if err != nil {
				return stats, fmt.Errorf("build article %d embedding event: %w", row.ID, err)
			}
			events = append(events, event)
		}
		if len(events) > 0 {
			if publisher == nil {
				metrics.RecordArticleEmbeddingPublishFailure("requeue")
				return stats, errors.New("article embedding publisher is nil")
			}
			if err := publisher.PublishBatch(context.Background(), events); err != nil {
				metrics.RecordArticleEmbeddingPublishFailure("requeue")
				return stats, fmt.Errorf("publish article embedding reconciliation page: %w", err)
			}
			stats.Published += len(events)
		}
		lastID = rows[len(rows)-1].ID
		if len(rows) < requeueArticleEmbeddingPageSize {
			break
		}
	}
	return stats, nil
}
