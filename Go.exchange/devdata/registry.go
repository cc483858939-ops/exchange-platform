package devdata

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	SourceRegistryVersion  = "x_sources_v1"
	DefaultMaxPosts        = 40
	DefaultMaxScanned      = 200
	DefaultSnapshotVersion = "nexus_x_mirror_v1"
	DefaultXAPIBaseURL     = "https://api.x.com"
	DefaultSnapshotRelPath = ".devdata/x_latest.json"
	DefaultRegistryRelPath = "devdata/testdata/x_sources_v1.json"
)

// SourceRegistry is repository-controlled configuration. It is deliberately
// not populated from X discovery results.
type SourceRegistry struct {
	Version         string          `json:"version"`
	DefaultMaxPosts int             `json:"default_max_posts"`
	Accounts        []SourceAccount `json:"accounts"`
}

type SourceAccount struct {
	Key      string `json:"key"`
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
	Category string `json:"category"`
	MaxPosts int    `json:"max_posts"`
	Enabled  bool   `json:"enabled"`
}

var xHandlePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

var curatedV1Accounts = map[string]SourceAccount{
	"thsottiaux":      {Key: "thsottiaux", Platform: "x", Handle: "thsottiaux", Category: "technology_ai"},
	"MKBHD":           {Key: "MKBHD", Platform: "x", Handle: "MKBHD", Category: "technology_ai"},
	"levelsio":        {Key: "levelsio", Platform: "x", Handle: "levelsio", Category: "business_creator"},
	"F1":              {Key: "F1", Platform: "x", Handle: "F1", Category: "sports"},
	"StephenCurry30":  {Key: "StephenCurry30", Platform: "x", Handle: "StephenCurry30", Category: "sports"},
	"NintendoAmerica": {Key: "NintendoAmerica", Platform: "x", Handle: "NintendoAmerica", Category: "gaming"},
	"IGN":             {Key: "IGN", Platform: "x", Handle: "IGN", Category: "gaming"},
	"MrBeast":         {Key: "MrBeast", Platform: "x", Handle: "MrBeast", Category: "entertainment_creator"},
	"letterboxd":      {Key: "letterboxd", Platform: "x", Handle: "letterboxd", Category: "entertainment_creator"},
	"billboard":       {Key: "billboard", Platform: "x", Handle: "billboard", Category: "entertainment_creator"},
	"GordonRamsay":    {Key: "GordonRamsay", Platform: "x", Handle: "GordonRamsay", Category: "lifestyle_food_humor"},
	"dog_rates":       {Key: "dog_rates", Platform: "x", Handle: "dog_rates", Category: "lifestyle_food_humor"},
	"Reuters":         {Key: "Reuters", Platform: "x", Handle: "Reuters", Category: "news"},
	"NASA":            {Key: "NASA", Platform: "x", Handle: "NASA", Category: "science"},
	"neiltyson":       {Key: "neiltyson", Platform: "x", Handle: "neiltyson", Category: "science"},
}

func DefaultRegistryPath(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	return filepath.Join(baseDir, filepath.FromSlash(DefaultRegistryRelPath))
}

func DefaultSnapshotPath(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	return filepath.Join(baseDir, filepath.FromSlash(DefaultSnapshotRelPath))
}

func LoadRegistry(path string) (SourceRegistry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SourceRegistry{}, fmt.Errorf("read source registry: %w", err)
	}
	var registry SourceRegistry
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return SourceRegistry{}, fmt.Errorf("decode source registry: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return SourceRegistry{}, errors.New("source registry contains trailing JSON")
		}
		return SourceRegistry{}, fmt.Errorf("decode trailing source registry data: %w", err)
	}
	if err := ValidateRegistry(registry); err != nil {
		return SourceRegistry{}, err
	}
	return registry, nil
}

func LoadCuratedRegistry(path string) (SourceRegistry, error) {
	registry, err := LoadRegistry(path)
	if err != nil {
		return SourceRegistry{}, err
	}
	if err := ValidateCuratedV1Registry(registry); err != nil {
		return SourceRegistry{}, err
	}
	return registry, nil
}

// ValidateRegistry validates the generic registry shape. The stricter
// ValidateCuratedV1Registry check is used by the shipped runtime registry.
func ValidateRegistry(registry SourceRegistry) error {
	if registry.Version != SourceRegistryVersion {
		return fmt.Errorf("unsupported source registry version %q", registry.Version)
	}
	if registry.DefaultMaxPosts <= 0 || registry.DefaultMaxPosts > DefaultMaxPosts {
		return fmt.Errorf("default_max_posts must be between 1 and %d", DefaultMaxPosts)
	}
	if len(registry.Accounts) == 0 {
		return errors.New("source registry has no accounts")
	}
	seenKeys := make(map[string]struct{}, len(registry.Accounts))
	seenHandles := make(map[string]struct{}, len(registry.Accounts))
	for index, account := range registry.Accounts {
		account.Key = strings.TrimSpace(account.Key)
		account.Platform = strings.ToLower(strings.TrimSpace(account.Platform))
		account.Handle = strings.TrimSpace(account.Handle)
		account.Category = strings.TrimSpace(account.Category)
		if account.Key == "" {
			return fmt.Errorf("account %d has empty key", index)
		}
		if account.Platform != "x" {
			return fmt.Errorf("account %q has unsupported platform %q", account.Key, account.Platform)
		}
		if !xHandlePattern.MatchString(account.Handle) {
			return fmt.Errorf("account %q has invalid X handle", account.Key)
		}
		if account.Category == "" {
			return fmt.Errorf("account %q has empty category", account.Key)
		}
		if account.MaxPosts <= 0 || account.MaxPosts > registry.DefaultMaxPosts {
			return fmt.Errorf("account %q max_posts must be between 1 and %d", account.Key, registry.DefaultMaxPosts)
		}
		if _, exists := seenKeys[account.Key]; exists {
			return fmt.Errorf("duplicate registry key %q", account.Key)
		}
		seenKeys[account.Key] = struct{}{}
		handleKey := strings.ToLower(account.Handle)
		if _, exists := seenHandles[handleKey]; exists {
			return fmt.Errorf("duplicate source handle %q", account.Handle)
		}
		seenHandles[handleKey] = struct{}{}
	}
	return nil
}

func ValidateCuratedV1Registry(registry SourceRegistry) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	enabled := make(map[string]SourceAccount)
	for _, account := range registry.Accounts {
		if account.Enabled {
			enabled[account.Key] = account
		}
	}
	if len(enabled) != len(curatedV1Accounts) {
		return fmt.Errorf("curated X registry must have exactly %d enabled accounts, got %d", len(curatedV1Accounts), len(enabled))
	}
	for key, expected := range curatedV1Accounts {
		actual, exists := enabled[key]
		if !exists {
			return fmt.Errorf("curated X registry is missing enabled account %q", key)
		}
		if actual.Handle != expected.Handle || actual.Platform != expected.Platform || actual.Category != expected.Category {
			return fmt.Errorf("curated X registry account %q does not match the controlled definition", key)
		}
		if actual.MaxPosts != DefaultMaxPosts {
			return fmt.Errorf("curated X registry account %q must have max_posts=%d", key, DefaultMaxPosts)
		}
	}
	return nil
}

func (registry SourceRegistry) EnabledAccounts() []SourceAccount {
	accounts := make([]SourceAccount, 0, len(registry.Accounts))
	for _, account := range registry.Accounts {
		if account.Enabled {
			accounts = append(accounts, account)
		}
	}
	return accounts
}

func (registry SourceRegistry) AccountByKey(key string) (SourceAccount, bool) {
	for _, account := range registry.Accounts {
		if account.Key == key {
			return account, true
		}
	}
	return SourceAccount{}, false
}

func (registry SourceRegistry) EnabledKeys() []string {
	keys := make([]string, 0)
	for _, account := range registry.EnabledAccounts() {
		keys = append(keys, account.Key)
	}
	return keys
}

func MirrorUsername(registryKey string) string { return "x_" + registryKey }

func sortedRegistryKeys(registry SourceRegistry) []string {
	keys := append([]string(nil), registry.EnabledKeys()...)
	sort.Strings(keys)
	return keys
}
