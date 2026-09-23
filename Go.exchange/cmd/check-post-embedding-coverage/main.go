package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"Go.exchange/config"
	"Go.exchange/global"

	"gorm.io/gorm"
)

type postEmbeddingCoverageScanner interface {
	CountEligible(context.Context) (int64, error)
	CountReady(context.Context, string) (int64, error)
}

type gormPostEmbeddingCoverageScanner struct {
	db *gorm.DB
}

type postEmbeddingCoverage struct {
	TargetVersion string
	Eligible      int64
	Ready         int64
	Missing       int64
	Percent       float64
}

func (s gormPostEmbeddingCoverageScanner) CountEligible(ctx context.Context) (int64, error) {
	if s.db == nil {
		return 0, errors.New("database is not initialized")
	}
	var count int64
	err := eligiblePostCoverageQuery(s.db.WithContext(ctx)).Count(&count).Error
	return count, err
}

func (s gormPostEmbeddingCoverageScanner) CountReady(ctx context.Context, targetVersion string) (int64, error) {
	if s.db == nil {
		return 0, errors.New("database is not initialized")
	}
	var count int64
	err := eligiblePostCoverageQuery(s.db.WithContext(ctx)).
		Where(`EXISTS (
			SELECT 1
			FROM post_embeddings AS pe
			WHERE pe.post_id = p.id
			  AND pe.version = ?
		)`, targetVersion).
		Count(&count).Error
	return count, err
}

func eligiblePostCoverageQuery(db *gorm.DB) *gorm.DB {
	return db.Table("posts AS p").
		Where("p.deleted_at IS NULL").
		Where("p.visibility = ?", "public").
		Where("p.reply_to_post_id IS NULL").
		Where(`EXISTS (
			SELECT 1
			FROM users AS u
			WHERE u.id = p.author_id
			  AND u.deleted_at IS NULL
		)`)
}

func calculatePostEmbeddingCoverage(ctx context.Context, scanner postEmbeddingCoverageScanner, targetVersion string) (postEmbeddingCoverage, error) {
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
	eligible, err := scanner.CountEligible(ctx)
	if err != nil {
		return postEmbeddingCoverage{}, fmt.Errorf("count eligible posts: %w", err)
	}
	ready, err := scanner.CountReady(ctx, targetVersion)
	if err != nil {
		return postEmbeddingCoverage{}, fmt.Errorf("count target-version embeddings: %w", err)
	}
	coverage := postEmbeddingCoverage{
		TargetVersion: targetVersion,
		Eligible:      eligible,
		Ready:         ready,
		Missing:       eligible - ready,
		Percent:       100,
	}
	if eligible > 0 {
		coverage.Percent = float64(ready) / float64(eligible) * 100
	}
	return coverage, nil
}

func writePostEmbeddingCoverage(w io.Writer, coverage postEmbeddingCoverage) error {
	if w == nil {
		return errors.New("coverage output writer is nil")
	}
	_, err := fmt.Fprintf(w,
		"post embedding coverage:\ntarget_version=%s\neligible=%d\nready=%d\nmissing=%d\ncoverage=%.4f%%\n",
		coverage.TargetVersion, coverage.Eligible, coverage.Ready, coverage.Missing, coverage.Percent,
	)
	return err
}

func run(w io.Writer) error {
	config.InitDatabaseConfig()
	coverage, err := calculatePostEmbeddingCoverage(
		context.Background(),
		gormPostEmbeddingCoverageScanner{db: global.Db},
		config.BuildEmbeddingVersion(),
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
