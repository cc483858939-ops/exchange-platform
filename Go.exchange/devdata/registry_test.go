package devdata

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCuratedRegistryHasExactlyTheControlledTwentyAccounts(t *testing.T) {
	registry, err := LoadCuratedRegistry(filepath.Join("testdata", "x_sources_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.EnabledAccounts()) != 20 {
		t.Fatalf("enabled accounts=%d, want 20", len(registry.EnabledAccounts()))
	}
	targetKeys := []string{
		"thsottiaux", "sugales_noah", "dotey", "naval", "RayDalio", "ahistoryinart", "japanvistamedia",
		"visualsofearth1", "SpaceX", "NintendoAmerica", "kasu_ps", "MrBeast", "letterboxd",
		"historyinmemes", "naenano78", "CuddlyCutePets", "wenqiangjp", "KobeissiLetter", "NASA", "neiltyson",
	}
	for _, key := range targetKeys {
		account, ok := registry.AccountByKey(key)
		if !ok {
			t.Fatalf("registry missing %q", key)
		}
		if account.Key != key || account.Handle != key || account.Platform != "x" || account.MaxPosts != 40 || !account.Enabled {
			t.Fatalf("account %q does not match controlled shape: %#v", key, account)
		}
	}
	for _, key := range []string{"MKBHD", "levelsio", "laozhouhengmei", "JamesAI", "Svwang1", "StephenCurry30", "IGN", "billboard", "Reuters"} {
		if _, ok := registry.AccountByKey(key); ok {
			t.Fatalf("removed registry key %q is still present", key)
		}
	}
	categoryAssertions := map[string]string{
		"naval":           "business_creator",
		"sugales_noah":    "entertainment_creator",
		"japanvistamedia": "travel_nature",
		"SpaceX":          "science",
		"kasu_ps":         "gaming",
		"historyinmemes":  "entertainment_creator",
		"KobeissiLetter":  "news",
		"visualsofearth1": "travel_nature",
		"CuddlyCutePets":  "lifestyle_food_humor",
	}
	for key, wantCategory := range categoryAssertions {
		account, ok := registry.AccountByKey(key)
		if !ok || account.Category != wantCategory {
			t.Fatalf("account %q category=%q exists=%t, want %q", key, account.Category, ok, wantCategory)
		}
	}
	if got := MirrorUsername("sugales_noah"); got != "x_sugales_noah" {
		t.Fatalf("mirror username=%q", got)
	}
}

func TestValidateRegistryRejectsDuplicateKeysAndHandles(t *testing.T) {
	base := SourceRegistry{
		Version: SourceRegistryVersion, DefaultMaxPosts: 2,
		Accounts: []SourceAccount{
			{Key: "one", Platform: "x", Handle: "one", Category: "test", MaxPosts: 2, Enabled: true},
			{Key: "two", Platform: "x", Handle: "two", Category: "test", MaxPosts: 2, Enabled: true},
		},
	}
	duplicateKey := base
	duplicateKey.Accounts = append([]SourceAccount(nil), base.Accounts...)
	duplicateKey.Accounts[1].Key = duplicateKey.Accounts[0].Key
	if err := ValidateRegistry(duplicateKey); err == nil || !strings.Contains(err.Error(), "duplicate registry key") {
		t.Fatalf("duplicate key error=%v", err)
	}
	duplicateHandle := base
	duplicateHandle.Accounts = append([]SourceAccount(nil), base.Accounts...)
	duplicateHandle.Accounts[1].Handle = duplicateHandle.Accounts[0].Handle
	if err := ValidateRegistry(duplicateHandle); err == nil || !strings.Contains(err.Error(), "duplicate source handle") {
		t.Fatalf("duplicate handle error=%v", err)
	}
}

func TestValidateCuratedRegistryUsesRegistryPolicyInsteadOfFixedAccountIdentity(t *testing.T) {
	registry := SourceRegistry{
		Version: SourceRegistryVersion, DefaultMaxPosts: DefaultMaxPosts,
		Accounts: []SourceAccount{
			{Key: "replacement_a", Platform: "x", Handle: "replacement_a", Category: "test", MaxPosts: DefaultMaxPosts, Enabled: true},
			{Key: "retired_b", Platform: "x", Handle: "retired_b", Category: "test", MaxPosts: DefaultMaxPosts, Enabled: false},
		},
	}
	if err := ValidateCuratedV1Registry(registry); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCuratedRegistryRejectsPolicyViolations(t *testing.T) {
	base := SourceRegistry{
		Version: SourceRegistryVersion, DefaultMaxPosts: DefaultMaxPosts,
		Accounts: []SourceAccount{{Key: "source", Platform: "x", Handle: "source", Category: "test", MaxPosts: DefaultMaxPosts, Enabled: true}},
	}
	base.Accounts[0].MaxPosts = DefaultMaxPosts - 1
	if err := ValidateCuratedV1Registry(base); err == nil || !strings.Contains(err.Error(), "max_posts") {
		t.Fatalf("max_posts policy error=%v", err)
	}
	base.Accounts[0].MaxPosts = DefaultMaxPosts
	base.Accounts[0].Enabled = false
	if err := ValidateCuratedV1Registry(base); err == nil || !strings.Contains(err.Error(), "at least one enabled") {
		t.Fatalf("enabled account policy error=%v", err)
	}
}
