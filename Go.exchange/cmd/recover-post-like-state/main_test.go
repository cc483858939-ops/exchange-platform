package main

import (
	"strings"
	"testing"
)

func TestRecoveryCommandDefaultsToDryRun(t *testing.T) {
	options, err := parseRecoveryOptions([]string{"--post-id=42"})
	if err != nil {
		t.Fatal(err)
	}
	if options.PostID != 42 || options.Apply {
		t.Fatalf("options=%+v, want PostID 42 and dry-run", options)
	}
}

func TestRecoveryCommandApplyRequiresAllMaintenanceConfirmations(t *testing.T) {
	_, err := parseRecoveryOptions([]string{"--post-id=42", "--apply"})
	if err == nil || !strings.Contains(err.Error(), "refusing mutation") {
		t.Fatalf("error=%v, want explicit maintenance confirmation refusal", err)
	}
	options, err := parseRecoveryOptions([]string{
		"--post-id=42", "--apply", "--confirm-development-reset",
		"--confirm-like-writes-paused", "--confirm-snapshot-behavior-kafka-drained",
	})
	if err != nil || !options.Apply {
		t.Fatalf("options=%+v err=%v, want explicitly confirmed apply", options, err)
	}
}

func TestRecoveryCommandRequiresExplicitPostID(t *testing.T) {
	if _, err := parseRecoveryOptions(nil); err == nil || !strings.Contains(err.Error(), "--post-id") {
		t.Fatalf("error=%v, want required PostID", err)
	}
}
