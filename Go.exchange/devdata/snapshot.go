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
	"unicode"
	"unicode/utf8"
)

const (
	MinTextRunes          = 10
	MinTextRunesWithMedia = 40
	MaxTextRunes          = 2000
)

type SourceMetrics struct {
	LikeCount   int64 `json:"source_like_count"`
	ReplyCount  int64 `json:"source_reply_count"`
	RepostCount int64 `json:"source_repost_count"`
	QuoteCount  int64 `json:"source_quote_count"`
}

type SnapshotAccount struct {
	RegistryKey     string `json:"registry_key"`
	SourceUserID    string `json:"source_user_id"`
	Handle          string `json:"handle"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	ProfileImageURL string `json:"profile_image_url"`
	Category        string `json:"category"`
}

type SnapshotPost struct {
	RegistryKey       string        `json:"registry_key"`
	SourcePostID      string        `json:"source_post_id"`
	SourceURL         string        `json:"source_url"`
	Text              string        `json:"text"`
	CreatedAt         time.Time     `json:"created_at"`
	Language          string        `json:"language"`
	PossiblySensitive bool          `json:"possibly_sensitive"`
	HasMedia          bool          `json:"has_media"`
	SourceMetrics     SourceMetrics `json:"source_metrics"`
}

type Snapshot struct {
	Version   string            `json:"version"`
	FetchedAt time.Time         `json:"fetched_at"`
	Accounts  []SnapshotAccount `json:"accounts"`
	Posts     []SnapshotPost    `json:"posts"`
}

type FetchAccountReport struct {
	RegistryKey        string `json:"registry_key"`
	APIRequests        int    `json:"api_requests"`
	SourcePostsScanned int    `json:"source_posts_scanned"`
	EligibleSelected   int    `json:"eligible_posts_selected"`
}

type FetchReport struct {
	APIRequests        int                  `json:"api_requests"`
	SourcePostsScanned int                  `json:"source_posts_scanned"`
	EligibleSelected   int                  `json:"eligible_posts_selected"`
	PerAccount         []FetchAccountReport `json:"per_account"`
}

func NormalizeSourceText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	var builder strings.Builder
	for _, r := range raw {
		switch r {
		case '\u200B', '\u200C', '\u200D', '\u2060', '\uFEFF':
			continue
		default:
			if unicode.Is(unicode.Cf, r) {
				continue
			}
			builder.WriteRune(r)
		}
	}
	return strings.TrimSpace(builder.String())
}

func SourceText(post XPost) string {
	if post.NoteTweet != nil {
		if text := NormalizeSourceText(post.NoteTweet.Text); text != "" {
			return text
		}
	}
	if post.Article != nil {
		for _, raw := range []string{post.Article.PlainText, post.Article.Text} {
			if text := NormalizeSourceText(raw); text != "" {
				return text
			}
		}
	}
	return NormalizeSourceText(post.Text)
}

func SourceTextContentHash(text string) string {
	sum := sha256.Sum256([]byte(NormalizeSourceText(text)))
	return hex.EncodeToString(sum[:])
}

func EligibleSourcePost(post XPost, sourceUserID string) (bool, string) {
	if strings.TrimSpace(post.ID) == "" {
		return false, "missing_id"
	}
	if sourceUserID != "" && strings.TrimSpace(post.AuthorID) != strings.TrimSpace(sourceUserID) {
		if strings.TrimSpace(post.AuthorID) == "" {
			return false, "missing_author"
		}
		return false, "different_author"
	}
	if post.InReplyToUserID != nil && strings.TrimSpace(*post.InReplyToUserID) != "" {
		return false, "reply"
	}
	if len(post.ReferencedTweets) > 0 {
		return false, "referenced_post"
	}
	if post.PossiblySensitive {
		return false, "possibly_sensitive"
	}
	if post.CreatedAt.IsZero() {
		return false, "missing_created_at"
	}
	text := SourceText(post)
	if text == "" {
		return false, "empty_text"
	}
	textRunes := utf8.RuneCountInString(text)
	hasMedia := len(post.Attachments.MediaKeys) > 0
	if hasMedia && textRunes < MinTextRunesWithMedia {
		return false, "media_dependent_text"
	}
	if !hasMedia && textRunes < MinTextRunes {
		return false, "short_text"
	}
	if textRunes > MaxTextRunes {
		return false, "text_too_long"
	}
	return true, ""
}

func BuildSnapshotPost(account SourceAccount, post XPost) SnapshotPost {
	text := SourceText(post)
	return SnapshotPost{
		RegistryKey:       account.Key,
		SourcePostID:      strings.TrimSpace(post.ID),
		SourceURL:         fmt.Sprintf("https://x.com/%s/status/%s", account.Handle, strings.TrimSpace(post.ID)),
		Text:              text,
		CreatedAt:         post.CreatedAt.UTC(),
		Language:          strings.TrimSpace(post.Lang),
		PossiblySensitive: post.PossiblySensitive,
		HasMedia:          len(post.Attachments.MediaKeys) > 0,
		SourceMetrics: SourceMetrics{
			LikeCount:   post.PublicMetrics.LikeCount,
			ReplyCount:  post.PublicMetrics.ReplyCount,
			RepostCount: post.PublicMetrics.RetweetCount,
			QuoteCount:  post.PublicMetrics.QuoteCount,
		},
	}
}

func ValidateSnapshot(snapshot Snapshot, registry SourceRegistry) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	if snapshot.Version != DefaultSnapshotVersion {
		return fmt.Errorf("unsupported snapshot version %q", snapshot.Version)
	}
	if snapshot.FetchedAt.IsZero() {
		return errors.New("snapshot fetched_at is required")
	}
	enabled := registry.EnabledAccounts()
	accountsByKey := make(map[string]SnapshotAccount, len(snapshot.Accounts))
	usersByID := make(map[string]struct{}, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		if _, exists := accountsByKey[account.RegistryKey]; exists {
			return fmt.Errorf("snapshot contains duplicate registry key %q", account.RegistryKey)
		}
		configured, exists := registry.AccountByKey(account.RegistryKey)
		if !exists || !configured.Enabled {
			return fmt.Errorf("snapshot contains unknown or disabled registry key %q", account.RegistryKey)
		}
		if !isNumericSourceID(account.SourceUserID) {
			return fmt.Errorf("snapshot account %q has invalid source user ID", account.RegistryKey)
		}
		if !xHandlePattern.MatchString(strings.TrimSpace(account.Handle)) || strings.TrimSpace(account.Name) == "" {
			return fmt.Errorf("snapshot account %q is missing handle or name", account.RegistryKey)
		}
		if _, exists := usersByID[account.SourceUserID]; exists {
			return fmt.Errorf("snapshot contains duplicate source user ID %q", account.SourceUserID)
		}
		usersByID[account.SourceUserID] = struct{}{}
		if account.Category != configured.Category {
			return fmt.Errorf("snapshot account %q category does not match registry", account.RegistryKey)
		}
		accountsByKey[account.RegistryKey] = account
	}
	if len(accountsByKey) != len(enabled) {
		return fmt.Errorf("snapshot must contain %d enabled accounts, got %d", len(enabled), len(accountsByKey))
	}
	for _, account := range enabled {
		if _, exists := accountsByKey[account.Key]; !exists {
			return fmt.Errorf("snapshot is missing enabled account %q", account.Key)
		}
	}

	postsByID := make(map[string]struct{}, len(snapshot.Posts))
	postsPerAccount := make(map[string]int, len(enabled))
	for _, post := range snapshot.Posts {
		if _, exists := accountsByKey[post.RegistryKey]; !exists {
			return fmt.Errorf("snapshot Post %q belongs to unknown account %q", post.SourcePostID, post.RegistryKey)
		}
		if !isNumericSourceID(post.SourcePostID) {
			return fmt.Errorf("snapshot Post for %q has invalid source Post ID", post.RegistryKey)
		}
		if _, exists := postsByID[post.SourcePostID]; exists {
			return fmt.Errorf("snapshot contains duplicate source Post ID %q", post.SourcePostID)
		}
		postsByID[post.SourcePostID] = struct{}{}
		if post.CreatedAt.IsZero() {
			return fmt.Errorf("snapshot Post %q is missing created_at", post.SourcePostID)
		}
		if post.PossiblySensitive {
			return fmt.Errorf("snapshot Post %q is sensitive", post.SourcePostID)
		}
		if strings.TrimSpace(post.SourceURL) == "" {
			return fmt.Errorf("snapshot Post %q is missing source_url", post.SourcePostID)
		}
		expectedURL := fmt.Sprintf("https://x.com/%s/status/%s", strings.TrimSpace(accountsByKey[post.RegistryKey].Handle), post.SourcePostID)
		if post.SourceURL != expectedURL {
			return fmt.Errorf("snapshot Post %q has unexpected source_url", post.SourcePostID)
		}
		text := NormalizeSourceText(post.Text)
		if text != post.Text {
			return fmt.Errorf("snapshot Post %q text is not normalized", post.SourcePostID)
		}
		textRunes := utf8.RuneCountInString(text)
		if post.HasMedia && textRunes < MinTextRunesWithMedia || !post.HasMedia && textRunes < MinTextRunes {
			return fmt.Errorf("snapshot Post %q does not meet text eligibility", post.SourcePostID)
		}
		if textRunes > MaxTextRunes {
			return fmt.Errorf("snapshot Post %q exceeds maximum text length", post.SourcePostID)
		}
		metrics := post.SourceMetrics
		if metrics.LikeCount < 0 || metrics.ReplyCount < 0 || metrics.RepostCount < 0 || metrics.QuoteCount < 0 {
			return fmt.Errorf("snapshot Post %q has negative source metrics", post.SourcePostID)
		}
		postsPerAccount[post.RegistryKey]++
	}
	for _, account := range enabled {
		if postsPerAccount[account.Key] > account.MaxPosts {
			return fmt.Errorf("snapshot account %q exceeds max_posts=%d", account.Key, account.MaxPosts)
		}
	}
	return nil
}

func ReadSnapshot(path string, registry SourceRegistry) (Snapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, fmt.Errorf("real X snapshot does not exist: %w", err)
		}
		return Snapshot{}, fmt.Errorf("open X snapshot: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxXResponseBytes))
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode X snapshot: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Snapshot{}, errors.New("X snapshot contains trailing JSON")
		}
		return Snapshot{}, fmt.Errorf("decode trailing X snapshot data: %w", err)
	}
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func WriteSnapshotAtomic(path string, snapshot Snapshot, registry SourceRegistry) error {
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create X snapshot directory: %w", err)
	}
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode X snapshot: %w", err)
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(directory, ".x_latest-*.tmp")
	if err != nil {
		return fmt.Errorf("create X snapshot temporary file: %w", err)
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
		return fmt.Errorf("restrict X snapshot temporary file: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write X snapshot temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync X snapshot temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close X snapshot temporary file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("atomically replace X snapshot: %w", err)
	}
	removeTemporary = false
	return nil
}

func sortSnapshotPosts(posts []SnapshotPost) {
	sort.Slice(posts, func(i, j int) bool {
		if posts[i].RegistryKey != posts[j].RegistryKey {
			return posts[i].RegistryKey < posts[j].RegistryKey
		}
		if !posts[i].CreatedAt.Equal(posts[j].CreatedAt) {
			return posts[i].CreatedAt.After(posts[j].CreatedAt)
		}
		return posts[i].SourcePostID > posts[j].SourcePostID
	})
}

func sortSnapshotAccounts(accounts []SnapshotAccount) {
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].RegistryKey < accounts[j].RegistryKey })
}
