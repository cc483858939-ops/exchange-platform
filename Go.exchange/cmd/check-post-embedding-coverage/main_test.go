package main

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

type coverageTestScanner struct {
	eligible int64
	ready    int64
	err      error
	version  string
}

func (s *coverageTestScanner) CountEligible(context.Context) (int64, error) {
	return s.eligible, s.err
}

func (s *coverageTestScanner) CountReady(_ context.Context, version string) (int64, error) {
	s.version = version
	return s.ready, s.err
}

func TestCalculatePostEmbeddingCoverage(t *testing.T) {
	scanner := &coverageTestScanner{eligible: 3, ready: 2}
	coverage, err := calculatePostEmbeddingCoverage(context.Background(), scanner, " post_embedding_v2 ")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.TargetVersion != "post_embedding_v2" || coverage.Eligible != 3 || coverage.Ready != 2 || coverage.Missing != 1 || math.Abs(coverage.Percent-66.6666666667) > 0.000001 {
		t.Fatalf("coverage=%+v", coverage)
	}
	if scanner.version != "post_embedding_v2" {
		t.Fatalf("ready count target version=%q", scanner.version)
	}
	var output bytes.Buffer
	if err := writePostEmbeddingCoverage(&output, coverage); err != nil {
		t.Fatal(err)
	}
	want := "post embedding coverage:\ntarget_version=post_embedding_v2\neligible=3\nready=2\nmissing=1\ncoverage=66.6667%\n"
	if output.String() != want {
		t.Fatalf("output=%q want=%q", output.String(), want)
	}
}

func TestCalculatePostEmbeddingCoverageTreatsEmptySetAsComplete(t *testing.T) {
	coverage, err := calculatePostEmbeddingCoverage(context.Background(), &coverageTestScanner{}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Eligible != 0 || coverage.Ready != 0 || coverage.Missing != 0 || coverage.Percent != 100 {
		t.Fatalf("empty coverage=%+v", coverage)
	}
}

func TestCalculatePostEmbeddingCoverageReportsQueryError(t *testing.T) {
	sentinel := errors.New("database unavailable")
	_, err := calculatePostEmbeddingCoverage(context.Background(), &coverageTestScanner{err: sentinel}, "v1")
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "count eligible posts") {
		t.Fatalf("error=%v", err)
	}
}
