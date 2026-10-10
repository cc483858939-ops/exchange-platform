package main

import (
	"strings"
	"testing"
	"time"
)

func TestBenchmarkOptionsAreSafeByDefaultAndBounded(t *testing.T) {
	options, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if options.Apply || options.ConfirmDisposable || options.RedisDB != 15 || options.ExpireAfter != 2*time.Second {
		t.Fatalf("unexpected safe defaults: %+v", options)
	}
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{args: []string{"--db=0"}, want: "between 1 and 15"},
		{args: []string{"--db=16"}, want: "between 1 and 15"},
		{args: []string{"--expire-after=1ms"}, want: "between 100ms and 30s"},
		{args: []string{"--hot-operation-samples=10"}, want: "between 100 and 10000"},
	} {
		if _, err := parseOptions(testCase.args); err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("parseOptions(%v) error=%v, want %q", testCase.args, err, testCase.want)
		}
	}
}

func TestBenchmarkDatasetCoversRequiredUserSizes(t *testing.T) {
	groups, posts, err := buildBenchmarkDataset()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 4 || len(posts) != 44440 {
		t.Fatalf("dataset groups=%d posts=%d", len(groups), len(posts))
	}
	for groupIndex, group := range groups {
		if len(group) != len(benchmarkRelationSizes) {
			t.Fatalf("group %d has %d users", groupIndex, len(group))
		}
		for index, user := range group {
			if user.Size != benchmarkRelationSizes[index] || len(user.PostIDs) != user.Size {
				t.Fatalf("group=%d user=%+v", groupIndex, user)
			}
		}
	}
}

func TestQuantileDurationUsesNearestRank(t *testing.T) {
	values := []time.Duration{5, 1, 4, 2, 3}
	if got := quantileDuration(values, .50); got != 3 {
		t.Fatalf("p50=%s want 3ns", got)
	}
	if got := quantileDuration(values, .95); got != 5 {
		t.Fatalf("p95=%s want 5ns", got)
	}
}
