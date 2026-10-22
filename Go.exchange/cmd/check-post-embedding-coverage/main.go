package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/global"

	"gorm.io/gorm"
)

const postEmbeddingCoveragePageSize = 500

type postEmbeddingCoverageRow struct {
	PostID       uint
	Content      string
	HasEmbedding bool
	ContentHash  sql.NullString
}

type postEmbeddingCoverageScanner interface {
	ScanEligible(context.Context, string, time.Time, time.Time, int, func([]postEmbeddingCoverageRow) error) error
}

type gormPostEmbeddingCoverageScanner struct {
	db *gorm.DB
}

type postEmbeddingCoverage struct {
	TargetVersion     string
	PrimaryWindowDays int
	WindowStart       time.Time
	WindowEnd         time.Time
	Eligible          int64
	Ready             int64
	Missing           int64
	StaleContent      int64
	Percent           float64
	EmptyCandidateSet bool
}

func (s gormPostEmbeddingCoverageScanner) ScanEligible(ctx context.Context, targetVersion string, windowStart, windowEnd time.Time, pageSize int, visit func([]postEmbeddingCoverageRow) error) error {
	if s.db == nil {
		return errors.New("database is not initialized")
	}
	if pageSize <= 0 {
		return errors.New("post embedding coverage page size must be positive")
	}
	if visit == nil {
		return errors.New("post embedding coverage page visitor is nil")
	}
	var lastPostID uint
	for {
		var rows []postEmbeddingCoverageRow
		err := eligiblePostCoverageQuery(s.db.WithContext(ctx), windowStart, windowEnd).
			Joins("LEFT JOIN post_embeddings AS pe ON pe.post_id = p.id AND pe.version = ?", targetVersion).
			Select("p.id AS post_id, p.content, pe.post_id IS NOT NULL AS has_embedding, pe.content_hash").
			Where("p.id > ?", lastPostID).
			Order("p.id ASC").
			Limit(pageSize).
			Scan(&rows).Error
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := visit(rows); err != nil {
			return err
		}
		lastPostID = rows[len(rows)-1].PostID
		if len(rows) < pageSize {
			return nil
		}
	}
}

func eligiblePostCoverageQuery(db *gorm.DB, windowStart, windowEnd time.Time) *gorm.DB {
	if windowStart.IsZero() || windowEnd.IsZero() || windowEnd.Before(windowStart) {
		return db.Table("posts AS p").Where("1 = 0")
	}
	return db.Table("posts AS p").
		Where("p.deleted_at IS NULL").
		Where("p.visibility = ?", "public").
		Where("p.reply_to_post_id IS NULL").
		Where("p.created_at >= ? AND p.created_at <= ?", windowStart.UTC(), windowEnd.UTC()).
		Where(`EXISTS (
			SELECT 1
			FROM users AS u
			WHERE u.id = p.author_id
			  AND u.deleted_at IS NULL
		)`)
}

func calculatePostEmbeddingCoverage(ctx context.Context, scanner postEmbeddingCoverageScanner, targetVersion string, now time.Time, primaryWindowDays int) (postEmbeddingCoverage, error) {
	targetVersion = strings.TrimSpace(targetVersion)
	if targetVersion == "" {
		return postEmbeddingCoverage{}, errors.New("target build embedding version is required")
	}
	if scanner == nil {
		return postEmbeddingCoverage{}, errors.New("post embedding coverage scanner is nil")
	}
	if ctx == nil {
		return postEmbeddingCoverage{}, errors.New("post embedding coverage context is nil")
	}
	if now.IsZero() {
		return postEmbeddingCoverage{}, errors.New("post embedding coverage time is required")
	}
	primaryWindowDays = config.NormalizeRecommendationPrimaryWindowDays(primaryWindowDays)
	windowStart, windowEnd := config.RecommendationPrimaryWindowBounds(now, primaryWindowDays)
	coverage := postEmbeddingCoverage{
		TargetVersion: targetVersion, PrimaryWindowDays: primaryWindowDays,
		WindowStart: windowStart, WindowEnd: windowEnd,
	}
	err := scanner.ScanEligible(ctx, targetVersion, windowStart, windowEnd, postEmbeddingCoveragePageSize, func(rows []postEmbeddingCoverageRow) error {
		for _, row := range rows {
			coverage.Eligible++
			if !row.HasEmbedding {
				continue
			}
			if row.ContentHash.Valid && row.ContentHash.String == embeddings.PostEmbeddingContentHash(row.Content) {
				coverage.Ready++
			} else {
				coverage.StaleContent++
			}
		}
		return nil
	})
	if err != nil {
		return postEmbeddingCoverage{}, fmt.Errorf("scan eligible posts: %w", err)
	}
	coverage.Missing = coverage.Eligible - coverage.Ready
	if coverage.Eligible > 0 {
		coverage.Percent = float64(coverage.Ready) / float64(coverage.Eligible) * 100
	} else {
		coverage.EmptyCandidateSet = true
	}
	return coverage, nil
}

func writePostEmbeddingCoverage(w io.Writer, coverage postEmbeddingCoverage) error {
	if w == nil {
		return errors.New("coverage output writer is nil")
	}
	_, err := fmt.Fprintf(w,
		"post embedding coverage:\ntarget_version=%s\nprimary_window_days=%d\nwindow_start=%s\nwindow_end=%s\nempty_candidate_set=%t\neligible=%d\nready=%d\nmissing=%d\nstale_content=%d\ncoverage=%.4f%%\n",
		coverage.TargetVersion, coverage.PrimaryWindowDays,
		coverage.WindowStart.Format(time.RFC3339Nano), coverage.WindowEnd.Format(time.RFC3339Nano), coverage.EmptyCandidateSet,
		coverage.Eligible, coverage.Ready, coverage.Missing, coverage.StaleContent, coverage.Percent,
	)
	return err
}

func run(w io.Writer) error {
	config.InitWorkerDatabaseConfig()
	defer config.CloseDatabasePools()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	now := time.Now().UTC()
	primaryWindowDays := config.NormalizeRecommendationPrimaryWindowDays(config.AppConfig.Recommendation.PrimaryWindowDays)
	coverage, err := calculatePostEmbeddingCoverage(
		ctx,
		gormPostEmbeddingCoverageScanner{db: global.WorkerDb},
		config.BuildEmbeddingVersion(),
		now,
		primaryWindowDays,
	)
	if err != nil {
		return err
	}
	return writePostEmbeddingCoverage(w, coverage)
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
