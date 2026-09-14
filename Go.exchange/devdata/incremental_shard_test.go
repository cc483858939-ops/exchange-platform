package devdata

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func incrementalTestRegistry(count int) SourceRegistry {
	accounts := make([]SourceAccount, 0, count)
	for index := 0; index < count; index++ {
		key := "account_" + strings.Repeat("x", index%3) + string(rune('a'+index))
		accounts = append(accounts, SourceAccount{
			Key: key, Platform: "x", Handle: key, Category: "test", MaxPosts: 40, Enabled: true,
		})
	}
	return SourceRegistry{Version: SourceRegistryVersion, DefaultMaxPosts: 40, Accounts: accounts}
}

func TestBuildIncrementalShardAssignmentsAreBalancedAndOrderIndependent(t *testing.T) {
	registry := incrementalTestRegistry(20)
	first, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		t.Fatal(err)
	}
	counts := make([]int, IncrementalShardCount)
	for key, shard := range first {
		if shard < 0 || shard >= IncrementalShardCount {
			t.Fatalf("account %q assigned to invalid shard %d", key, shard)
		}
		counts[shard]++
	}
	if got, want := len(first), 20; got != want {
		t.Fatalf("assigned accounts=%d want %d", got, want)
	}
	for shard, count := range counts {
		if count != 5 {
			t.Fatalf("shard %d count=%d want 5 (all=%v)", shard, count, counts)
		}
	}
	for left, right := 0, len(registry.Accounts)-1; left < right; left, right = left+1, right-1 {
		registry.Accounts[left], registry.Accounts[right] = registry.Accounts[right], registry.Accounts[left]
	}
	second, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("assignment sizes first=%d second=%d", len(first), len(second))
	}
	for key, want := range first {
		if got := second[key]; got != want {
			t.Fatalf("account %q moved from shard %d to %d after reorder", key, want, got)
		}
	}
}

func TestIncrementalAutoShardUsesHourlyUTCBuckets(t *testing.T) {
	base := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	want := IncrementalShardForTime(base)
	for offset := 0; offset <= 4; offset++ {
		got := IncrementalShardForTime(base.Add(time.Duration(offset)*time.Hour + 30*time.Minute))
		if got != (want+offset)%IncrementalShardCount {
			t.Fatalf("offset=%dh shard=%d want=%d", offset, got, (want+offset)%IncrementalShardCount)
		}
	}
}

func TestCuratedRegistryIncrementalShardsAreFiveEach(t *testing.T) {
	registry, err := LoadCuratedRegistry(filepath.Join("testdata", "x_sources_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		t.Fatal(err)
	}
	counts := make([]int, IncrementalShardCount)
	for _, shard := range assignments {
		counts[shard]++
	}
	for shard, count := range counts {
		if count != 5 {
			t.Fatalf("curated shard %d count=%d all=%v assignments=%v", shard, count, counts, assignments)
		}
	}
	if len(assignments) != 20 {
		t.Fatalf("curated shard counts=%v assignments=%v", counts, assignments)
	}
}

func TestParseIncrementalShard(t *testing.T) {
	now := time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)
	if got, err := ParseIncrementalShard("auto", now); err != nil || got != IncrementalShardForTime(now) {
		t.Fatalf("auto shard=%d err=%v", got, err)
	}
	for _, raw := range []string{"0", "1", "2", "3"} {
		if _, err := ParseIncrementalShard(raw, now); err != nil {
			t.Fatalf("manual shard %q rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{"-1", "4", "x", "auto-old", ""} {
		if _, err := ParseIncrementalShard(raw, now); err == nil {
			t.Fatalf("invalid shard %q accepted", raw)
		}
	}
}
