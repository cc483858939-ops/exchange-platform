package devdata

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	IncrementalShardCount    = 4
	IncrementalShardInterval = time.Hour
)

type incrementalShardAccount struct {
	Key  string
	Hash uint64
}

// BuildIncrementalShardAssignments assigns every enabled registry account to
// exactly one balanced shard. Hashing before the modulo operation keeps the
// result independent of the registry file's input order.
func BuildIncrementalShardAssignments(registry SourceRegistry) (map[string]int, error) {
	if err := ValidateRegistry(registry); err != nil {
		return nil, err
	}
	accounts := registry.EnabledAccounts()
	ordered := make([]incrementalShardAccount, 0, len(accounts))
	assignments := make(map[string]int, len(accounts))
	for _, account := range accounts {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(account.Key))
		ordered = append(ordered, incrementalShardAccount{Key: account.Key, Hash: hasher.Sum64()})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Hash != ordered[j].Hash {
			return ordered[i].Hash < ordered[j].Hash
		}
		return ordered[i].Key < ordered[j].Key
	})
	for index, account := range ordered {
		assignments[account.Key] = index % IncrementalShardCount
	}
	return assignments, nil
}

// AssignIncrementalShards is a convenience wrapper for callers that already
// validated the registry. Invalid input produces an empty assignment rather
// than a partial map.
func AssignIncrementalShards(registry SourceRegistry) map[string]int {
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		return map[string]int{}
	}
	return assignments
}

func IncrementalShardForTime(now time.Time) int {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	bucket := now.UTC().Unix() / int64(IncrementalShardInterval/time.Second)
	return int(bucket % IncrementalShardCount)
}

func AutoIncrementalShard(now time.Time) int {
	return IncrementalShardForTime(now)
}

func ParseIncrementalShard(raw string, now time.Time) (int, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || raw == "auto" {
		if raw == "" {
			return 0, errors.New("shard must be auto or an integer from 0 to 3")
		}
		return IncrementalShardForTime(now), nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value >= IncrementalShardCount {
		return 0, fmt.Errorf("shard must be auto or an integer from 0 to %d", IncrementalShardCount-1)
	}
	return value, nil
}

func SelectIncrementalShardAccounts(registry SourceRegistry, shard int) ([]SourceAccount, error) {
	if shard < 0 || shard >= IncrementalShardCount {
		return nil, fmt.Errorf("shard must be between 0 and %d", IncrementalShardCount-1)
	}
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		return nil, err
	}
	selected := make([]SourceAccount, 0)
	for _, account := range registry.EnabledAccounts() {
		if assignments[account.Key] == shard {
			selected = append(selected, account)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Key < selected[j].Key })
	return selected, nil
}
