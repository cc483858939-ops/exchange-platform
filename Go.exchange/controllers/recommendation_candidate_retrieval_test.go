package controllers

import (
	"reflect"
	"testing"
)

func TestTopPositiveRecommendationInterests(t *testing.T) {
	got := topPositiveRecommendationInterests(map[string]float64{
		" backend ": 0.9,
		"ai":        0.5,
		"travel":    -0.7,
		"":          0,
	}, recommendationTopCategoryCount)
	want := []recommendationInterest{
		{Label: "backend", Affinity: 0.9},
		{Label: "ai", Affinity: 0.5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interests=%#v want=%#v", got, want)
	}
}

func TestMergeRulesV3CandidateIDsPreservesSourceOrderAndDeduplicates(t *testing.T) {
	got := mergeRulesV3CandidateIDs(20,
		[]uint{7, 3, 7, 0},
		[]uint{2, 3, 5},
		[]uint{1, 5},
	)
	want := []uint{7, 3, 2, 5, 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged=%v want=%v", got, want)
	}
}

func TestMergeRulesV3CandidateIDsHardCap(t *testing.T) {
	first := make([]uint, recommendationCategoryCandidateCap)
	second := make([]uint, recommendationMergedCandidateCap)
	for i := range first {
		first[i] = uint(i + 1)
	}
	for i := range second {
		second[i] = uint(recommendationCategoryCandidateCap + i + 1)
	}
	got := mergeRulesV3CandidateIDs(recommendationMergedCandidateCap, first, second)
	if len(got) != recommendationMergedCandidateCap {
		t.Fatalf("merged=%d want=%d", len(got), recommendationMergedCandidateCap)
	}
	if got[0] != 1 || got[len(got)-1] != recommendationMergedCandidateCap {
		t.Fatalf("cap order endpoints=%v,%v", got[0], got[len(got)-1])
	}
}

func TestRulesV3CandidateRetrievalHelpersAreDeterministic(t *testing.T) {
	values := map[string]float64{
		" Travel ": 0.4,
		"backend":  0.8,
		"AI":       0.8,
		"zero":     0,
		"negative": -1,
	}
	wantInterests := topPositiveRecommendationInterests(values, recommendationTopCategoryCount)
	for i := 0; i < 100; i++ {
		if got := topPositiveRecommendationInterests(values, recommendationTopCategoryCount); !reflect.DeepEqual(got, wantInterests) {
			t.Fatalf("iteration %d interests=%#v want=%#v", i, got, wantInterests)
		}
	}
	sources := [][]uint{{9, 4, 9, 2}, {3, 4, 1}, {8, 7, 6}}
	wantMerged := mergeRulesV3CandidateIDs(recommendationMergedCandidateCap, sources...)
	for i := 0; i < 100; i++ {
		if got := mergeRulesV3CandidateIDs(recommendationMergedCandidateCap, sources...); !reflect.DeepEqual(got, wantMerged) {
			t.Fatalf("iteration %d merged=%v want=%v", i, got, wantMerged)
		}
	}
}
