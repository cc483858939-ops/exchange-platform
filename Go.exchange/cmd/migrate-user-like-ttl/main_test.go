package main

import (
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
)

func TestParseMigrationOptionsDefaultsToDryRun(t *testing.T) {
	options, err := parseMigrationOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if options.Mode != userLikeTTLModeArm || options.Apply || options.PageSize != 200 || options.StartAfterUserID != 0 {
		t.Fatalf("unexpected default options: %+v", options)
	}
}

func TestParseMigrationOptionsBoundsAndModeInputs(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{name: "large page", args: []string{"--page-size=1001"}, want: "page-size"},
		{name: "unknown mode", args: []string{"--mode=scan"}, want: "mode"},
		{name: "orphan requires id", args: []string{"--mode=audit-orphan"}, want: "requires --user-id"},
		{name: "orphan cannot use keyset", args: []string{"--mode=audit-orphan", "--user-id=42", "--start-after-user-id=10"}, want: "does not accept"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := parseMigrationOptions(testCase.args)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("parse error=%v, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestApplyRequiresSafeFeatureSwitchAndConfirmations(t *testing.T) {
	base := config.UserLikeLifecycleConfig{
		ArmingEnabled: true, RestoreEnabled: true, SetTTL: 72 * time.Hour,
		RestoreLockTTL: 30 * time.Second, RestoreRequestTimeout: 2 * time.Second,
	}
	options, err := parseMigrationOptions([]string{"--mode=arm", "--apply"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationSettings(options, base); err == nil || !strings.Contains(err.Error(), "confirm-all-api-instances") {
		t.Fatalf("arm validation error=%v", err)
	}
	options.ConfirmAllAPIsArmingEnabled = true
	if err := validateMigrationSettings(options, base); err != nil {
		t.Fatalf("confirmed arm validation error=%v", err)
	}

	rollback, err := parseMigrationOptions([]string{"--mode=rollback", "--apply"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationSettings(rollback, base); err == nil || !strings.Contains(err.Error(), "rollback apply requires") {
		t.Fatalf("rollback accepted while arming is enabled: %v", err)
	}
	base.ArmingEnabled = false
	rollback.ConfirmOldArmingStopped = true
	rollback.ConfirmAllAPIsArmingOff = true
	if err := validateMigrationSettings(rollback, base); err != nil {
		t.Fatalf("confirmed rollback validation error=%v", err)
	}
}
