package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
)

type coverageTestScanner struct {
	rows     []postEmbeddingCoverageRow
	err      error
	version  string
	pageSize int
	start    time.Time
	end      time.Time
}

func (s *coverageTestScanner) ScanEligible(_ context.Context, version string, start, end time.Time, pageSize int, visit func([]postEmbeddingCoverageRow) error) error {
	s.version = version
	s.pageSize = pageSize
	s.start = start
	s.end = end
	if s.err != nil {
		return s.err
	}
	return visit(s.rows)
}

func TestCalculatePostEmbeddingCoverage(t *testing.T) {
	content := "matching content"
	scanner := &coverageTestScanner{rows: []postEmbeddingCoverageRow{
		{PostID: 1, Content: content, HasEmbedding: true, ContentHash: sql.NullString{String: embeddings.PostEmbeddingContentHash(content), Valid: true}},
		{PostID: 2, Content: "stale content", HasEmbedding: true, ContentHash: sql.NullString{String: "old-hash", Valid: true}},
		{PostID: 3, Content: "missing content"},
	}}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	coverage, err := calculatePostEmbeddingCoverage(context.Background(), scanner, " post_embedding_v2 ", now, 5)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.TargetVersion != "post_embedding_v2" || coverage.Eligible != 3 || coverage.Ready != 1 || coverage.Missing != 2 || coverage.StaleContent != 1 || math.Abs(coverage.Percent-33.3333333333) > 0.000001 {
		t.Fatalf("coverage=%+v", coverage)
	}
	if scanner.version != "post_embedding_v2" || scanner.pageSize != postEmbeddingCoveragePageSize ||
		!scanner.start.Equal(now.AddDate(0, 0, -5)) || !scanner.end.Equal(now) || coverage.PrimaryWindowDays != 5 {
		t.Fatalf("scan version=%q page size=%d range=[%s,%s] coverage=%+v", scanner.version, scanner.pageSize, scanner.start, scanner.end, coverage)
	}
	var output bytes.Buffer
	if err := writePostEmbeddingCoverage(&output, coverage); err != nil {
		t.Fatal(err)
	}
	want := "post embedding coverage:\ntarget_version=post_embedding_v2\nprimary_window_days=5\nwindow_start=2026-10-14T12:00:00Z\nwindow_end=2026-10-19T12:00:00Z\nempty_candidate_set=false\neligible=3\nready=1\nmissing=2\nstale_content=1\ncoverage=33.3333%\n"
	if output.String() != want {
		t.Fatalf("output=%q want=%q", output.String(), want)
	}
}

func TestCalculatePostEmbeddingCoverageReportsEmptyCandidateSet(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	coverage, err := calculatePostEmbeddingCoverage(context.Background(), &coverageTestScanner{}, "v1", now, config.DefaultRecommendationPrimaryWindowDays)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Eligible != 0 || coverage.Ready != 0 || coverage.Missing != 0 || coverage.StaleContent != 0 || coverage.Percent != 0 || !coverage.EmptyCandidateSet {
		t.Fatalf("empty coverage=%+v", coverage)
	}
}

func TestCalculatePostEmbeddingCoverageReportsScanError(t *testing.T) {
	sentinel := errors.New("database unavailable")
	_, err := calculatePostEmbeddingCoverage(context.Background(), &coverageTestScanner{err: sentinel}, "v1", time.Now().UTC(), 7)
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "scan eligible posts") {
		t.Fatalf("error=%v", err)
	}
}
