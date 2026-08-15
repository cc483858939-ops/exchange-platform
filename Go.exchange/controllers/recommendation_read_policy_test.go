package controllers

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyRecommendationReadV1(t *testing.T) {
	tests := []struct {
		name       string
		foreground int64
		progress   int
		estimated  int64
		want       string
	}{
		{name: "tiny short post bounce", foreground: 1000, progress: 0, estimated: 3000, want: recommendationReadOutcomeQuickBounce},
		{name: "short post dwell qualifies", foreground: 3000, progress: 0, estimated: 3000, want: recommendationReadOutcomeQualified},
		{name: "fast scroll alone does not qualify", foreground: 2000, progress: 100, estimated: 60 * 1000, want: recommendationReadOutcomeNeutral},
		{name: "engaged partial reading qualifies", foreground: 20 * 1000, progress: 50, estimated: 60 * 1000, want: recommendationReadOutcomeQualified},
		{name: "strong dwell qualifies", foreground: 45 * 1000, progress: 0, estimated: 60 * 1000, want: recommendationReadOutcomeQualified},
		{name: "intermediate is neutral", foreground: 10 * 1000, progress: 20, estimated: 60 * 1000, want: recommendationReadOutcomeNeutral},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyRecommendationRead(tc.foreground, tc.progress, tc.estimated, recommendationReadPolicyVersion)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("outcome=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestClassifyRecommendationReadRejectsInvalidMeasurements(t *testing.T) {
	tests := []struct {
		name       string
		foreground int64
		progress   int
		estimated  int64
		policy     string
	}{
		{name: "negative foreground", foreground: -1, progress: 0, estimated: 3000, policy: recommendationReadPolicyVersion},
		{name: "foreground above max", foreground: recommendationReadMaxForegroundMS + 1, progress: 0, estimated: 3000, policy: recommendationReadPolicyVersion},
		{name: "negative progress", foreground: 1000, progress: -1, estimated: 3000, policy: recommendationReadPolicyVersion},
		{name: "progress above max", foreground: 1000, progress: 101, estimated: 3000, policy: recommendationReadPolicyVersion},
		{name: "missing estimate", foreground: 1000, progress: 0, estimated: 0, policy: recommendationReadPolicyVersion},
		{name: "unknown policy", foreground: 1000, progress: 0, estimated: 3000, policy: "read_v2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := classifyRecommendationRead(tc.foreground, tc.progress, tc.estimated, tc.policy); err == nil {
				t.Fatal("expected invalid measurement error")
			}
		})
	}
}

func TestEstimateArticleReadTime(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    time.Duration
	}{
		{name: "empty", content: "", want: 3 * time.Second},
		{name: "tiny", content: "hello", want: 3 * time.Second},
		{name: "english", content: strings.Repeat("word ", 220), want: time.Minute},
		{name: "chinese", content: strings.Repeat("中", 300), want: time.Minute},
		{name: "japanese", content: strings.Repeat("あ", 300), want: time.Minute},
		{name: "korean", content: strings.Repeat("한", 300), want: time.Minute},
		{name: "mixed", content: strings.Repeat("中", 150) + strings.Repeat("word ", 110), want: time.Minute},
		{name: "minimum clamp", content: "one word", want: 3 * time.Second},
		{name: "maximum clamp", content: strings.Repeat("中", 1000), want: 120 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimateArticleReadTime(tc.content); got != tc.want {
				t.Fatalf("estimate=%s want=%s", got, tc.want)
			}
		})
	}
}
