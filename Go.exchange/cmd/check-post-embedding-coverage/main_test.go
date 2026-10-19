package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	"Go.exchange/embeddings"
)

type coverageTestScanner struct {
	rows     []postEmbeddingCoverageRow
	err      error
	version  string
	pageSize int
}

func (s *coverageTestScanner) ScanEligible(_ context.Context, version string, pageSize int, visit func([]postEmbeddingCoverageRow) error) error {
	s.version = version
	s.pageSize = pageSize
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
	coverage, err := calculatePostEmbeddingCoverage(context.Background(), scanner, " post_embedding_v2 ")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.TargetVersion != "post_embedding_v2" || coverage.Eligible != 3 || coverage.Ready != 1 || coverage.Missing != 2 || coverage.StaleContent != 1 || math.Abs(coverage.Percent-33.3333333333) > 0.000001 {
		t.Fatalf("coverage=%+v", coverage)
	}
	if scanner.version != "post_embedding_v2" || scanner.pageSize != postEmbeddingCoveragePageSize {
		t.Fatalf("scan version=%q page size=%d", scanner.version, scanner.pageSize)
	}
	var output bytes.Buffer
	if err := writePostEmbeddingCoverage(&output, coverage); err != nil {
		t.Fatal(err)
	}
	want := "post embedding coverage:\ntarget_version=post_embedding_v2\neligible=3\nready=1\nmissing=2\nstale_content=1\ncoverage=33.3333%\n"
	if output.String() != want {
		t.Fatalf("output=%q want=%q", output.String(), want)
	}
}

func TestCalculatePostEmbeddingCoverageTreatsEmptySetAsComplete(t *testing.T) {
	coverage, err := calculatePostEmbeddingCoverage(context.Background(), &coverageTestScanner{}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Eligible != 0 || coverage.Ready != 0 || coverage.Missing != 0 || coverage.StaleContent != 0 || coverage.Percent != 100 {
		t.Fatalf("empty coverage=%+v", coverage)
	}
}

func TestCalculatePostEmbeddingCoverageReportsScanError(t *testing.T) {
	sentinel := errors.New("database unavailable")
	_, err := calculatePostEmbeddingCoverage(context.Background(), &coverageTestScanner{err: sentinel}, "v1")
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "scan eligible posts") {
		t.Fatalf("error=%v", err)
	}
}
