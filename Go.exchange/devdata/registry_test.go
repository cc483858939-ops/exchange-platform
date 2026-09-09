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
	for _, key := range []string{"thsottiaux", "MKBHD", "dotey", "laozhouhengmei", "JamesAI", "Svwang1", "wenqiangjp", "visualsofearth1", "CuddlyCutePets", "NASA", "neiltyson"} {
		if _, ok := registry.AccountByKey(key); !ok {
			t.Fatalf("registry missing %q", key)
		}
	}
	for _, key := range []string{"F1", "dog_rates"} {
		if _, ok := registry.AccountByKey(key); ok {
			t.Fatalf("removed registry key %q is still present", key)
		}
	}
	if account, ok := registry.AccountByKey("visualsofearth1"); !ok || account.Category != "travel_nature" || account.Handle != "visualsofearth1" {
		t.Fatalf("landscape account=%#v exists=%t", account, ok)
	}
	if account, ok := registry.AccountByKey("CuddlyCutePets"); !ok || account.Category != "lifestyle_food_humor" || account.Handle != "CuddlyCutePets" {
		t.Fatalf("cute animals account=%#v exists=%t", account, ok)
	}
	if got := MirrorUsername("MKBHD"); got != "x_MKBHD" {
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
