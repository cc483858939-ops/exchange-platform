package devdata

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// RegistryReplacement describes the one-account registry transition supported
// by targeted replacement. The old key is retired and the new key is fetched.
type RegistryReplacement struct {
	OldKey string
	NewKey string
}

// ReadReplacementBaseline reads a rolling snapshot before a registry
// replacement. Unlike ReadIncrementalBaseline, it validates the snapshot's
// structure independently from the current enabled-account set and then
// validates the explicitly declared old-to-new transition.
func ReadReplacementBaseline(path string, currentRegistry SourceRegistry, replacement RegistryReplacement) (Snapshot, string, error) {
	if err := ValidateRegistry(currentRegistry); err != nil {
		return Snapshot{}, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	if len(raw) > maxXResponseBytes {
		return Snapshot{}, "", fmt.Errorf("%w: snapshot exceeds %d bytes", ErrIncrementalBaselineRequired, maxXResponseBytes)
	}
	snapshot, err := decodeSnapshotJSON(raw)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	if err := ValidateReplacementBaseline(snapshot, currentRegistry, replacement); err != nil {
		return Snapshot{}, "", fmt.Errorf("%w: %v", ErrIncrementalBaselineRequired, err)
	}
	digest := sha256.Sum256(raw)
	return snapshot, hex.EncodeToString(digest[:]), nil
}

// ValidateReplacementBaseline validates the old snapshot structurally and
// then validates the exact one-account registry transition. It intentionally
// does not call ValidateSnapshot against currentRegistry because the old key
// is expected to be absent or disabled and the new key is not in the baseline.
func ValidateReplacementBaseline(baseline Snapshot, currentRegistry SourceRegistry, replacement RegistryReplacement) error {
	if err := ValidateRegistry(currentRegistry); err != nil {
		return err
	}
	replacement = normalizedReplacement(replacement)
	if err := validateReplacementKeys(replacement); err != nil {
		return err
	}
	if err := validateReplacementBaselineStructure(baseline, currentRegistry); err != nil {
		return err
	}
	return ValidateRegistryReplacement(baseline, currentRegistry, replacement.OldKey, replacement.NewKey)
}

// ValidateRegistryReplacement validates that oldKey -> newKey is the only
// enabled-account set change and that unchanged account identity/configuration
// has not been altered under cover of the replacement.
func ValidateRegistryReplacement(baseline Snapshot, currentRegistry SourceRegistry, oldKey, newKey string) error {
	if err := ValidateRegistry(currentRegistry); err != nil {
		return err
	}
	replacement := normalizedReplacement(RegistryReplacement{OldKey: oldKey, NewKey: newKey})
	if err := validateReplacementKeys(replacement); err != nil {
		return err
	}

	baselineByKey := make(map[string]SnapshotAccount, len(baseline.Accounts))
	for _, account := range baseline.Accounts {
		key := strings.TrimSpace(account.RegistryKey)
		if _, exists := baselineByKey[key]; exists {
			return fmt.Errorf("replacement baseline contains duplicate registry key %q", key)
		}
		baselineByKey[key] = account
	}
	oldAccount, exists := baselineByKey[replacement.OldKey]
	if !exists {
		return fmt.Errorf("replacement old registry key %q does not exist in baseline snapshot", replacement.OldKey)
	}
	if _, exists := baselineByKey[replacement.NewKey]; exists {
		return fmt.Errorf("replacement new registry key %q already exists in baseline snapshot", replacement.NewKey)
	}

	if configured, exists := currentRegistry.AccountByKey(replacement.OldKey); exists && configured.Enabled {
		return fmt.Errorf("replacement old registry key %q is still enabled", replacement.OldKey)
	}
	newAccount, exists := currentRegistry.AccountByKey(replacement.NewKey)
	if !exists {
		return fmt.Errorf("replacement new registry key %q does not exist in current registry", replacement.NewKey)
	}
	if !newAccount.Enabled {
		return fmt.Errorf("replacement new registry key %q is not enabled", replacement.NewKey)
	}
	if oldAccount.RegistryKey == newAccount.Key {
		return fmt.Errorf("replacement old registry key %q and new registry key %q are not distinct", replacement.OldKey, replacement.NewKey)
	}

	for key, previous := range baselineByKey {
		if key == replacement.OldKey {
			continue
		}
		configured, exists := currentRegistry.AccountByKey(key)
		if !exists || !configured.Enabled {
			return fmt.Errorf("replacement transition contains additional removed account %q", key)
		}
		if configured.Handle != previous.Handle {
			return fmt.Errorf("replacement transition changed handle for unchanged account %q", key)
		}
		if configured.Category != previous.Category {
			return fmt.Errorf("replacement transition changed category for unchanged account %q", key)
		}
		if configured.Platform != "x" {
			return fmt.Errorf("replacement transition changed platform for unchanged account %q", key)
		}
		if configured.MaxPosts != currentRegistry.DefaultMaxPosts {
			return fmt.Errorf("replacement transition changed max_posts for unchanged account %q", key)
		}
	}
	for _, configured := range currentRegistry.EnabledAccounts() {
		if configured.Key == replacement.NewKey {
			continue
		}
		if _, exists := baselineByKey[configured.Key]; !exists {
			return fmt.Errorf("replacement transition contains additional new account %q", configured.Key)
		}
	}
	return nil
}

func validateReplacementKeys(replacement RegistryReplacement) error {
	if replacement.OldKey == "" {
		return errors.New("replacement old registry key is required")
	}
	if replacement.NewKey == "" {
		return errors.New("replacement new registry key is required")
	}
	if replacement.OldKey == replacement.NewKey {
		return errors.New("replacement old and new registry keys must differ")
	}
	return nil
}

func normalizedReplacement(replacement RegistryReplacement) RegistryReplacement {
	replacement.OldKey = strings.TrimSpace(replacement.OldKey)
	replacement.NewKey = strings.TrimSpace(replacement.NewKey)
	return replacement
}

func validateReplacementBaselineStructure(baseline Snapshot, currentRegistry SourceRegistry) error {
	if baseline.Version != DefaultSnapshotVersion {
		return fmt.Errorf("replacement baseline has unsupported snapshot version %q", baseline.Version)
	}
	if baseline.FetchedAt.IsZero() {
		return errors.New("replacement baseline fetched_at is required")
	}
	structuralRegistry := replacementBaselineRegistry(baseline, currentRegistry)
	if err := ValidateSnapshot(baseline, structuralRegistry); err != nil {
		return fmt.Errorf("invalid replacement baseline structure: %w", err)
	}
	return nil
}

// replacementBaselineRegistry supplies only validation metadata for accounts
// present in the old snapshot. For a removed key, the snapshot itself is the
// source of handle/category identity and the current registry default is the
// only available max_posts policy. This keeps transition validation controlled
// without pretending the old key is part of the current enabled registry.
func replacementBaselineRegistry(baseline Snapshot, currentRegistry SourceRegistry) SourceRegistry {
	accounts := make([]SourceAccount, 0, len(baseline.Accounts))
	for _, source := range baseline.Accounts {
		configured, exists := currentRegistry.AccountByKey(source.RegistryKey)
		maxPosts := currentRegistry.DefaultMaxPosts
		if exists && configured.MaxPosts > 0 {
			maxPosts = configured.MaxPosts
		}
		accounts = append(accounts, SourceAccount{
			Key:      source.RegistryKey,
			Platform: "x",
			Handle:   source.Handle,
			Category: source.Category,
			MaxPosts: maxPosts,
			Enabled:  true,
		})
	}
	return SourceRegistry{
		Version:         currentRegistry.Version,
		DefaultMaxPosts: currentRegistry.DefaultMaxPosts,
		Accounts:        accounts,
	}
}

// MergeReplacementSnapshot removes the retired account and its posts, adds
// the fetched new account, and leaves every retained account/post unchanged
// except for global ordering and FetchedAt.
func MergeReplacementSnapshot(baseline Snapshot, batch TargetedRefreshBatch, currentRegistry SourceRegistry, oldKey string, now time.Time) (Snapshot, error) {
	replacement := normalizedReplacement(RegistryReplacement{OldKey: oldKey, NewKey: batch.RegistryKey})
	oldKey = replacement.OldKey
	if err := ValidateReplacementBaseline(baseline, currentRegistry, replacement); err != nil {
		return Snapshot{}, err
	}
	if err := ValidateTargetedRefreshBatch(batch, currentRegistry); err != nil {
		return Snapshot{}, err
	}
	newAccount := batch.Accounts[0]
	oldAccount := replacementBaselineAccountByKey(baseline, replacement.OldKey)
	if newAccount.SourceUserID == oldAccount.SourceUserID {
		return Snapshot{}, fmt.Errorf("%w: replacement new source user conflicts with retired account %q", ErrSourceIdentityMismatch, replacement.OldKey)
	}
	for _, retained := range baseline.Accounts {
		if retained.RegistryKey == replacement.OldKey {
			continue
		}
		if retained.SourceUserID == newAccount.SourceUserID {
			return Snapshot{}, fmt.Errorf("%w: replacement new source user conflicts with retained account %q", ErrSourceIdentityMismatch, retained.RegistryKey)
		}
	}

	next := Snapshot{
		Version:   baseline.Version,
		FetchedAt: batch.FetchedAt.UTC(),
		Accounts:  make([]SnapshotAccount, 0, len(baseline.Accounts)),
		Posts:     make([]SnapshotPost, 0, len(baseline.Posts)+len(batch.Posts)),
	}
	for _, account := range baseline.Accounts {
		if account.RegistryKey != replacement.OldKey {
			next.Accounts = append(next.Accounts, account)
		}
	}
	next.Accounts = append(next.Accounts, newAccount)
	for _, post := range baseline.Posts {
		if post.RegistryKey != replacement.OldKey {
			next.Posts = append(next.Posts, cloneSnapshotPost(post))
		}
	}
	newPosts := cloneSnapshotPosts(batch.Posts)
	sort.SliceStable(newPosts, func(i, j int) bool {
		if !newPosts[i].CreatedAt.Equal(newPosts[j].CreatedAt) {
			return newPosts[i].CreatedAt.After(newPosts[j].CreatedAt)
		}
		return newPosts[i].SourcePostID > newPosts[j].SourcePostID
	})
	configuredNew, _ := currentRegistry.AccountByKey(replacement.NewKey)
	if len(newPosts) > configuredNew.MaxPosts {
		newPosts = newPosts[:configuredNew.MaxPosts]
	}
	next.Posts = append(next.Posts, newPosts...)
	if now.IsZero() {
		now = batch.FetchedAt
	}
	next.FetchedAt = now.UTC()
	sortSnapshotAccounts(next.Accounts)
	sortSnapshotPosts(next.Posts)
	if err := ValidateSnapshot(next, currentRegistry); err != nil {
		return Snapshot{}, fmt.Errorf("validate merged replacement snapshot: %w", err)
	}
	return next, nil
}

func replacementBaselineAccountByKey(snapshot Snapshot, key string) SnapshotAccount {
	for _, account := range snapshot.Accounts {
		if account.RegistryKey == key {
			return account
		}
	}
	return SnapshotAccount{}
}
