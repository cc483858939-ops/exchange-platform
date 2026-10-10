package main

import (
	"strings"
	"testing"
	"time"
)

func TestFindSlowlogScriptDurationMatchesExactEvalSHA(t *testing.T) {
	entries := []interface{}{
		[]interface{}{int64(3), int64(10), int64(900), []interface{}{"PING"}},
		[]interface{}{int64(2), int64(10), int64(321), []interface{}{"EVALSHA", "other-sha", "1"}},
		[]interface{}{int64(1), int64(10), int64(4567), []interface{}{[]byte("EVALSHA"), []byte("wanted-sha"), []byte("6")}},
	}
	duration, err := findSlowlogScriptDuration(entries, "wanted-sha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if duration != 4567*time.Microsecond {
		t.Fatalf("duration=%s want=%s", duration, 4567*time.Microsecond)
	}
}

func TestFindSlowlogScriptDurationRequiresMatchingEntry(t *testing.T) {
	entries := []interface{}{
		[]interface{}{int64(1), int64(10), int64(4567), []interface{}{"EVALSHA", "different-sha", "6"}},
	}
	if _, err := findSlowlogScriptDuration(entries, "wanted-sha", 0); err == nil || !strings.Contains(err.Error(), "no new EVALSHA entry") {
		t.Fatalf("findSlowlogScriptDuration error=%v, want missing exact script entry", err)
	}
}

func TestFindSlowlogScriptDurationIgnoresEntriesBeforeBaseline(t *testing.T) {
	entries := []interface{}{
		[]interface{}{int64(9), int64(10), int64(4567), []interface{}{"EVALSHA", "wanted-sha", "6"}},
	}
	if _, err := findSlowlogScriptDuration(entries, "wanted-sha", 9); err == nil {
		t.Fatal("findSlowlogScriptDuration matched an entry at the baseline ID")
	}
}

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
	if len(groups) != 4 || len(posts) != 84396 {
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
