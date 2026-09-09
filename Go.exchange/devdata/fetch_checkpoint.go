package devdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	FetchCheckpointVersion  = "x_fetch_checkpoint_v1"
	FetchCheckpointSource   = "rsshub"
	maxFetchCheckpointBytes = 64 << 20
)

// FetchAccountData is the independently persisted result for one enabled
// registry account. It is deliberately not a Snapshot so incomplete work can
// never be mistaken for a complete desired-state snapshot.
type FetchAccountData struct {
	Account SnapshotAccount    `json:"account"`
	Posts   []SnapshotPost     `json:"posts"`
	Report  FetchAccountReport `json:"report"`
}

type FetchFailure struct {
	Attempts     int       `json:"attempts"`
	LastError    string    `json:"last_error"`
	LastFailedAt time.Time `json:"last_failed_at"`
}

type FetchCheckpoint struct {
	Version             string                      `json:"version"`
	Source              string                      `json:"source"`
	RegistryFingerprint string                      `json:"registry_fingerprint"`
	StartedAt           time.Time                   `json:"started_at"`
	UpdatedAt           time.Time                   `json:"updated_at"`
	Completed           map[string]FetchAccountData `json:"completed"`
	Failures            map[string]FetchFailure     `json:"failures,omitempty"`
}

type fetchFingerprintAccount struct {
	Key      string `json:"key"`
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
	Category string `json:"category"`
	MaxPosts int    `json:"max_posts"`
	Enabled  bool   `json:"enabled"`
}

type fetchFingerprintPayload struct {
	Version            string                    `json:"version"`
	EligibilityVersion string                    `json:"eligibility_version"`
	DefaultMaxPosts    int                       `json:"default_max_posts"`
	Accounts           []fetchFingerprintAccount `json:"accounts"`
}

// RegistryFingerprint returns a stable lowercase SHA-256 digest for the
// enabled source configuration and current eligibility policy. The explicit
// slice order and struct fields avoid depending on Go map or JSON map ordering.
func RegistryFingerprint(registry SourceRegistry) string {
	return registryFingerprintWithEligibilityPolicy(registry, SourceEligibilityPolicyVersion)
}

func registryFingerprintWithEligibilityPolicy(registry SourceRegistry, eligibilityVersion string) string {
	accounts := make([]fetchFingerprintAccount, 0, len(registry.Accounts))
	for _, account := range registry.Accounts {
		if !account.Enabled {
			continue
		}
		accounts = append(accounts, fetchFingerprintAccount{
			Key:      strings.TrimSpace(account.Key),
			Platform: strings.ToLower(strings.TrimSpace(account.Platform)),
			Handle:   strings.TrimSpace(account.Handle),
			Category: strings.TrimSpace(account.Category),
			MaxPosts: account.MaxPosts,
			Enabled:  account.Enabled,
		})
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Key != accounts[j].Key {
			return accounts[i].Key < accounts[j].Key
		}
		return accounts[i].Handle < accounts[j].Handle
	})
	payload, _ := json.Marshal(fetchFingerprintPayload{
		Version:            strings.TrimSpace(registry.Version),
		EligibilityVersion: strings.TrimSpace(eligibilityVersion),
		DefaultMaxPosts:    registry.DefaultMaxPosts,
		Accounts:           accounts,
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func WriteFetchCheckpointAtomic(path string, checkpoint FetchCheckpoint) error {
	if err := validateFetchCheckpointShape(checkpoint); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create fetch checkpoint directory: %w", err)
	}
	payload, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fetch checkpoint: %w", err)
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(directory, ".x_fetch_checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("create fetch checkpoint temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict fetch checkpoint temporary file: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write fetch checkpoint temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync fetch checkpoint temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close fetch checkpoint temporary file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("atomically replace fetch checkpoint: %w", err)
	}
	removeTemporary = false
	return nil
}

func ReadFetchCheckpoint(path string) (FetchCheckpoint, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FetchCheckpoint{}, fmt.Errorf("fetch checkpoint does not exist: %w", err)
		}
		return FetchCheckpoint{}, fmt.Errorf("open fetch checkpoint: %w", err)
	}
	defer file.Close()
	if info, statErr := file.Stat(); statErr == nil && info.Size() > maxFetchCheckpointBytes {
		return FetchCheckpoint{}, fmt.Errorf("fetch checkpoint exceeds %d bytes", maxFetchCheckpointBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxFetchCheckpointBytes+1))
	decoder.DisallowUnknownFields()
	var checkpoint FetchCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return FetchCheckpoint{}, fmt.Errorf("decode fetch checkpoint: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return FetchCheckpoint{}, errors.New("fetch checkpoint contains trailing JSON")
		}
		return FetchCheckpoint{}, fmt.Errorf("decode trailing fetch checkpoint data: %w", err)
	}
	if err := validateFetchCheckpointShape(checkpoint); err != nil {
		return FetchCheckpoint{}, err
	}
	return checkpoint, nil
}

func RemoveFetchCheckpoint(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove fetch checkpoint: %w", err)
	}
	return nil
}

// ValidateFetchCheckpoint checks compatibility with the currently loaded
// registry. A fingerprint mismatch is intentionally actionable rather than a
// reason to silently discard operator-visible progress.
func ValidateFetchCheckpoint(checkpoint FetchCheckpoint, registry SourceRegistry, source string) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	if err := validateFetchCheckpointShape(checkpoint); err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(checkpoint.Source)) != strings.ToLower(strings.TrimSpace(source)) {
		return fmt.Errorf("fetch checkpoint source %q does not match requested source %q; use --reset-checkpoint to discard it", checkpoint.Source, source)
	}
	if checkpoint.RegistryFingerprint != RegistryFingerprint(registry) {
		return errors.New("fetch checkpoint does not match current fetch configuration; use --reset-checkpoint to discard it")
	}
	enabled := make(map[string]struct{}, len(registry.EnabledAccounts()))
	for _, account := range registry.EnabledAccounts() {
		enabled[account.Key] = struct{}{}
	}
	for key, data := range checkpoint.Completed {
		if _, ok := enabled[key]; !ok {
			return fmt.Errorf("fetch checkpoint contains unknown or disabled completed account %q; use --reset-checkpoint to discard it", key)
		}
		if data.Account.RegistryKey != key || data.Report.RegistryKey != key {
			return fmt.Errorf("fetch checkpoint completed account %q has inconsistent registry identity", key)
		}
		for _, post := range data.Posts {
			if post.RegistryKey != key {
				return fmt.Errorf("fetch checkpoint account %q contains a post for %q", key, post.RegistryKey)
			}
		}
	}
	for key := range checkpoint.Failures {
		if _, ok := enabled[key]; !ok {
			return fmt.Errorf("fetch checkpoint contains unknown or disabled failure account %q; use --reset-checkpoint to discard it", key)
		}
	}
	return nil
}

func AssembleSnapshotFromCheckpoint(checkpoint FetchCheckpoint, registry SourceRegistry, fetchedAt time.Time) (Snapshot, error) {
	if err := ValidateFetchCheckpoint(checkpoint, registry, FetchCheckpointSource); err != nil {
		return Snapshot{}, err
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	accounts := registry.EnabledAccounts()
	snapshot := Snapshot{
		Version:   DefaultSnapshotVersion,
		FetchedAt: fetchedAt.UTC(),
		Accounts:  make([]SnapshotAccount, 0, len(accounts)),
		Posts:     make([]SnapshotPost, 0, len(accounts)*DefaultMaxPosts),
	}
	for _, account := range accounts {
		data, ok := checkpoint.Completed[account.Key]
		if !ok {
			return Snapshot{}, fmt.Errorf("fetch checkpoint is incomplete: missing account %q", account.Key)
		}
		snapshot.Accounts = append(snapshot.Accounts, data.Account)
		snapshot.Posts = append(snapshot.Posts, data.Posts...)
	}
	sortSnapshotAccounts(snapshot.Accounts)
	sortSnapshotPosts(snapshot.Posts)
	normalizeSnapshotLanguages(&snapshot)
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// FetchReportFromCheckpoint aggregates persisted account metrics. APIRequests
// is supplied by the caller and represents source requests made by the
// current process; persisted per-account reports retain historical metrics.
func FetchReportFromCheckpoint(checkpoint FetchCheckpoint, registry SourceRegistry, currentRunRequests int) (FetchReport, error) {
	if err := ValidateFetchCheckpoint(checkpoint, registry, FetchCheckpointSource); err != nil {
		return FetchReport{}, err
	}
	if currentRunRequests < 0 {
		currentRunRequests = 0
	}
	accounts := registry.EnabledAccounts()
	report := FetchReport{APIRequests: currentRunRequests, PerAccount: make([]FetchAccountReport, 0, len(checkpoint.Completed))}
	for _, account := range accounts {
		data, ok := checkpoint.Completed[account.Key]
		if !ok {
			continue
		}
		report.PerAccount = append(report.PerAccount, data.Report)
		report.SourcePostsScanned += data.Report.SourcePostsScanned
		report.EligibleSelected += data.Report.EligibleSelected
	}
	return report, nil
}

func validateFetchCheckpointShape(checkpoint FetchCheckpoint) error {
	if checkpoint.Version != FetchCheckpointVersion {
		return fmt.Errorf("unsupported fetch checkpoint version %q", checkpoint.Version)
	}
	if strings.TrimSpace(checkpoint.Source) == "" {
		return errors.New("fetch checkpoint source is required")
	}
	if strings.TrimSpace(checkpoint.RegistryFingerprint) == "" {
		return errors.New("fetch checkpoint registry fingerprint is required")
	}
	if checkpoint.StartedAt.IsZero() || checkpoint.UpdatedAt.IsZero() {
		return errors.New("fetch checkpoint timestamps are required")
	}
	for key, data := range checkpoint.Completed {
		if strings.TrimSpace(key) == "" || data.Account.RegistryKey == "" || data.Report.RegistryKey == "" {
			return errors.New("fetch checkpoint contains an incomplete completed account")
		}
		if data.Report.APIRequests < 0 || data.Report.SourcePostsScanned < 0 || data.Report.EligibleSelected < 0 {
			return fmt.Errorf("fetch checkpoint account %q contains negative report values", key)
		}
	}
	for key, failure := range checkpoint.Failures {
		if strings.TrimSpace(key) == "" {
			return errors.New("fetch checkpoint contains an empty failure account")
		}
		if failure.Attempts < 0 {
			return fmt.Errorf("fetch checkpoint failure %q contains negative attempts", key)
		}
	}
	return nil
}
