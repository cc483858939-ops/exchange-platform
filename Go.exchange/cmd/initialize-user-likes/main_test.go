package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRequiresExplicitDevelopmentAndQuiescenceConfirmations(t *testing.T) {
	var output bytes.Buffer
	err := run(nil, &output)
	if err == nil || !strings.Contains(err.Error(), "both explicit") {
		t.Fatalf("run error=%v want explicit confirmation refusal", err)
	}
}

func TestRunValidatesBoundedPageSizeBeforeOpeningDependencies(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{
		"--confirm-development-reset",
		"--confirm-api-worker-kafka-quiesced",
		"--page-size=1001",
	}, &output)
	if err == nil || !strings.Contains(err.Error(), "page-size must be between") {
		t.Fatalf("run error=%v want page-size validation", err)
	}
}
