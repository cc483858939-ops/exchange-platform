package devdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

var ErrIncrementalSnapshotChanged = errors.New("rolling snapshot changed during incremental refresh")

// ErrIncrementalSnapshotConflict is retained as a compatibility alias for
// callers that used the original name before the conflict decision was
// centralized in WriteIncrementalSnapshotIfUnchanged.
var ErrIncrementalSnapshotConflict = ErrIncrementalSnapshotChanged

// ReadIncrementalBaseline loads the complete rolling snapshot and returns the
// fingerprint of the exact bytes that were decoded. A partial, stale, or
// malformed baseline is never accepted as an incremental starting point.
func ReadIncrementalBaseline(path string, registry SourceRegistry) (Snapshot, string, error) {
	if err := ValidateRegistry(registry); err != nil {
		return Snapshot{}, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	if len(raw) > maxXResponseBytes {
		return Snapshot{}, "", fmt.Errorf("%w: snapshot exceeds %d bytes", ErrIncrementalBaselineRequired, maxXResponseBytes)
	}
	snapshot, err := decodeSnapshotBytes(raw, registry)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	return snapshot, sha256Hex(raw), nil
}

func SnapshotFingerprint(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash snapshot: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash snapshot: %w", err)
	}
	digest := hasher.Sum(nil)
	return hex.EncodeToString(digest), nil
}

// WriteIncrementalSnapshotIfUnchanged writes snapshot only when the rolling
// baseline still has the fingerprint observed before the incremental fetch.
// The fingerprint check intentionally remains a small check-then-rename
// window; callers coordinate normal DevData mutations with the shared DB lock.
func WriteIncrementalSnapshotIfUnchanged(path string, expectedFingerprint string, snapshot Snapshot, registry SourceRegistry) error {
	currentFingerprint, err := SnapshotFingerprint(path)
	if err != nil {
		return fmt.Errorf("read incremental snapshot fingerprint: %w", err)
	}
	if currentFingerprint != expectedFingerprint {
		return ErrIncrementalSnapshotChanged
	}
	return WriteSnapshotAtomic(path, snapshot, registry)
}

func sha256Hex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func decodeSnapshotBytes(raw []byte, registry SourceRegistry) (Snapshot, error) {
	snapshot, err := decodeSnapshotJSON(raw)
	if err != nil {
		return Snapshot{}, err
	}
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func decodeSnapshotJSON(raw []byte) (Snapshot, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode X snapshot: %w", err)
	}
	normalizeSnapshotLanguages(&snapshot)
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Snapshot{}, errors.New("X snapshot contains trailing JSON")
		}
		return Snapshot{}, fmt.Errorf("decode trailing X snapshot data: %w", err)
	}
	return snapshot, nil
}

// ValidateIncrementalBatch validates a batch as a shard-scoped desired-state
// fragment. It deliberately validates the fragment through the same complete
// snapshot rules using a temporary selected-account registry.
func ValidateIncrementalBatch(batch IncrementalBatch, registry SourceRegistry) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	if batch.FetchedAt.IsZero() {
		return errors.New("incremental batch fetched_at is required")
	}
	selected, err := SelectIncrementalShardAccounts(registry, batch.Shard)
	if err != nil {
		return err
	}
	seenAccounts := make(map[string]struct{}, len(batch.Accounts))
	selectedKeys := make(map[string]struct{}, len(selected))
	for _, account := range selected {
		selectedKeys[account.Key] = struct{}{}
	}
	for _, account := range batch.Accounts {
		if _, ok := selectedKeys[account.RegistryKey]; !ok {
			return fmt.Errorf("incremental batch contains account outside shard: %q", account.RegistryKey)
		}
		if _, exists := seenAccounts[account.RegistryKey]; exists {
			return fmt.Errorf("incremental batch contains duplicate account %q", account.RegistryKey)
		}
		seenAccounts[account.RegistryKey] = struct{}{}
	}
	if len(seenAccounts) != len(selected) {
		return fmt.Errorf("incremental batch must contain %d selected accounts, got %d", len(selected), len(seenAccounts))
	}
	for _, account := range selected {
		if _, ok := seenAccounts[account.Key]; !ok {
			return fmt.Errorf("incremental batch is missing selected account %q", account.Key)
		}
	}
	if len(selected) == 0 {
		if len(batch.Posts) != 0 {
			return errors.New("incremental batch with no selected accounts contains posts")
		}
		return validateCoverageWindowAccounts(batch, selectedKeys)
	}

	subset := SourceRegistry{
		Version:         registry.Version,
		DefaultMaxPosts: registry.DefaultMaxPosts,
		Accounts:        selected,
	}
	fragment := Snapshot{
		Version:   DefaultSnapshotVersion,
		FetchedAt: batch.FetchedAt.UTC(),
		Accounts:  append([]SnapshotAccount(nil), batch.Accounts...),
		Posts:     cloneSnapshotPosts(batch.Posts),
	}
	if err := ValidateSnapshot(fragment, subset); err != nil {
		return fmt.Errorf("invalid incremental batch: %w", err)
	}
	return validateCoverageWindowAccounts(batch, selectedKeys)
}

func ValidateIncrementalBatchForShard(batch IncrementalBatch, registry SourceRegistry, shard int) error {
	batch.Shard = shard
	return ValidateIncrementalBatch(batch, registry)
}

func validateCoverageWindowAccounts(batch IncrementalBatch, selected map[string]struct{}) error {
	seen := make(map[string]struct{}, len(batch.CoverageWindowExhausted))
	for _, key := range batch.CoverageWindowExhausted {
		key = strings.TrimSpace(key)
		if _, ok := selected[key]; !ok {
			return fmt.Errorf("coverage exhaustion references account outside shard: %q", key)
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("coverage exhaustion contains duplicate account %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// MergeIncrementalSnapshot merges only the selected shard into a complete
// rolling snapshot. New representations replace old ones by source identity;
// absence from a source window never removes an old post.
func MergeIncrementalSnapshot(baseline Snapshot, batch IncrementalBatch, registry SourceRegistry, now time.Time) (Snapshot, error) {
	if err := ValidateSnapshot(baseline, registry); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	if err := ValidateIncrementalBatch(batch, registry); err != nil {
		return Snapshot{}, err
	}
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		return Snapshot{}, err
	}
	selectedKeys := make(map[string]struct{})
	for _, account := range batch.Accounts {
		if assignments[account.RegistryKey] != batch.Shard {
			return Snapshot{}, fmt.Errorf("incremental batch account %q is assigned to shard %d, not %d", account.RegistryKey, assignments[account.RegistryKey], batch.Shard)
		}
		selectedKeys[account.RegistryKey] = struct{}{}
	}
	return mergeSelectedSnapshot(baseline, batch.Accounts, batch.Posts, registry, selectedKeys, batch.FetchedAt, now, "incremental")
}

func mergeSelectedSnapshot(baseline Snapshot, updatedAccounts []SnapshotAccount, updatedPosts []SnapshotPost, registry SourceRegistry, selectedKeys map[string]struct{}, fetchedAt, now time.Time, mode string) (Snapshot, error) {
	baselineAccounts := make(map[string]SnapshotAccount, len(baseline.Accounts))
	for _, account := range baseline.Accounts {
		baselineAccounts[account.RegistryKey] = account
	}
	for _, account := range updatedAccounts {
		if previous, ok := baselineAccounts[account.RegistryKey]; ok && previous.SourceUserID != account.SourceUserID {
			return Snapshot{}, fmt.Errorf("%w: registry key %q changed source user from %q to %q", ErrSourceIdentityMismatch, account.RegistryKey, previous.SourceUserID, account.SourceUserID)
		}
	}
	next := cloneSnapshot(baseline)
	accountsByKey := make(map[string]*SnapshotAccount, len(next.Accounts))
	for index := range next.Accounts {
		accountsByKey[next.Accounts[index].RegistryKey] = &next.Accounts[index]
	}
	for _, account := range updatedAccounts {
		current := accountsByKey[account.RegistryKey]
		if current == nil {
			return Snapshot{}, fmt.Errorf("baseline is missing selected account %q", account.RegistryKey)
		}
		*current = account
	}

	postsByKey := make(map[SourcePostKey]SnapshotPost, len(next.Posts)+len(updatedPosts))
	for _, post := range next.Posts {
		postsByKey[SourcePostKey{RegistryKey: post.RegistryKey, SourcePostID: post.SourcePostID}] = cloneSnapshotPost(post)
	}
	for _, post := range updatedPosts {
		key := SourcePostKey{RegistryKey: post.RegistryKey, SourcePostID: post.SourcePostID}
		postsByKey[key] = cloneSnapshotPost(post)
	}

	postsByAccount := make(map[string][]SnapshotPost, len(next.Accounts))
	for _, post := range postsByKey {
		postsByAccount[post.RegistryKey] = append(postsByAccount[post.RegistryKey], post)
	}
	next.Posts = next.Posts[:0]
	for _, account := range registry.EnabledAccounts() {
		posts := postsByAccount[account.Key]
		if _, selected := selectedKeys[account.Key]; selected {
			sort.SliceStable(posts, func(i, j int) bool {
				if !posts[i].CreatedAt.Equal(posts[j].CreatedAt) {
					return posts[i].CreatedAt.After(posts[j].CreatedAt)
				}
				return posts[i].SourcePostID > posts[j].SourcePostID
			})
			if len(posts) > account.MaxPosts {
				posts = posts[:account.MaxPosts]
			}
		}
		next.Posts = append(next.Posts, posts...)
	}
	if now.IsZero() {
		now = fetchedAt
	}
	next.FetchedAt = now.UTC()
	sortSnapshotAccounts(next.Accounts)
	sortSnapshotPosts(next.Posts)
	if err := ValidateSnapshot(next, registry); err != nil {
		return Snapshot{}, fmt.Errorf("validate merged %s snapshot: %w", mode, err)
	}
	return next, nil
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := Snapshot{
		Version:   snapshot.Version,
		FetchedAt: snapshot.FetchedAt,
		Accounts:  make([]SnapshotAccount, len(snapshot.Accounts)),
		Posts:     make([]SnapshotPost, len(snapshot.Posts)),
	}
	copy(clone.Accounts, snapshot.Accounts)
	for index, post := range snapshot.Posts {
		clone.Posts[index] = cloneSnapshotPost(post)
	}
	return clone
}

func cloneSnapshotPosts(posts []SnapshotPost) []SnapshotPost {
	clone := make([]SnapshotPost, len(posts))
	for index, post := range posts {
		clone[index] = cloneSnapshotPost(post)
	}
	return clone
}

func cloneSnapshotPost(post SnapshotPost) SnapshotPost {
	post.Media = append([]SnapshotMedia(nil), post.Media...)
	return post
}
