package devdata

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCuratedRegistryHasExactlyTheControlledFifteenAccounts(t *testing.T) {
	registry, err := LoadCuratedRegistry(filepath.Join("testdata", "x_sources_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.EnabledAccounts()) != 15 {
		t.Fatalf("enabled accounts=%d, want 15", len(registry.EnabledAccounts()))
	}
	for _, key := range []string{"thsottiaux", "MKBHD", "F1", "NASA", "neiltyson"} {
		if _, ok := registry.AccountByKey(key); !ok {
			t.Fatalf("registry missing %q", key)
		}
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
