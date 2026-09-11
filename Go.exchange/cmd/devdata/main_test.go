package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseCommandFlagsDefaultsToRSSHub(t *testing.T) {
	for _, name := range []string{"DEVDATA_FETCH_BATCH_SIZE", "DEVDATA_FETCH_BATCH_DELAY"} {
		t.Setenv(name, "")
	}
	options, err := parseCommandFlags("fetch", nil, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if options.source != "rsshub" {
		t.Fatalf("default source=%q", options.source)
	}
	if options.batchSize != DefaultCommandBatchSize || options.batchDelay != DefaultCommandBatchDelay {
		t.Fatalf("defaults batch_size=%d batch_delay=%s", options.batchSize, options.batchDelay)
	}
}

func TestParseCommandFlagsEnvironmentAndCLIPrecedence(t *testing.T) {
	t.Setenv("DEVDATA_FETCH_BATCH_SIZE", "7")
	t.Setenv("DEVDATA_FETCH_BATCH_DELAY", "13s")
	fromEnv, err := parseCommandFlags("fetch", nil, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromEnv.batchSize != 7 || fromEnv.batchDelay != 13*time.Second {
		t.Fatalf("environment options=%#v", fromEnv)
	}
	fromCLI, err := parseCommandFlags("fetch", []string{"--batch-size=2", "--batch-delay=0s", "--checkpoint=custom.json"}, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromCLI.batchSize != 2 || fromCLI.batchDelay != 0 || fromCLI.checkpoint != "custom.json" {
		t.Fatalf("CLI options=%#v", fromCLI)
	}
}

func TestParseCommandFlagsRejectsInvalidFetchPacing(t *testing.T) {
	t.Setenv("DEVDATA_FETCH_BATCH_SIZE", "")
	t.Setenv("DEVDATA_FETCH_BATCH_DELAY", "")
	if _, err := parseCommandFlags("fetch", []string{"--batch-size=0"}, io.Discard, false); err == nil {
		t.Fatal("zero batch size unexpectedly accepted")
	}
	if _, err := parseCommandFlags("fetch", []string{"--batch-delay=-1s"}, io.Discard, false); err == nil {
		t.Fatal("negative batch delay unexpectedly accepted")
	}
}

func TestParseCommandFlagsRejectsInvalidFetchEnvironment(t *testing.T) {
	t.Setenv("DEVDATA_FETCH_BATCH_SIZE", "not-an-int")
	if _, err := parseCommandFlags("fetch", nil, io.Discard, false); err == nil || !strings.Contains(err.Error(), "DEVDATA_FETCH_BATCH_SIZE") {
		t.Fatalf("invalid batch environment error=%v", err)
	}
	t.Setenv("DEVDATA_FETCH_BATCH_SIZE", "")
	t.Setenv("DEVDATA_FETCH_BATCH_DELAY", "not-a-duration")
	if _, err := parseCommandFlags("fetch", nil, io.Discard, false); err == nil || !strings.Contains(err.Error(), "DEVDATA_FETCH_BATCH_DELAY") {
		t.Fatalf("invalid delay environment error=%v", err)
	}
}

func TestParseCommandFlagsSupportsRSSHubAndOfficialXFallback(t *testing.T) {
	rsshub, err := parseCommandFlags("fetch", []string{"--source=rsshub"}, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if rsshub.source != "rsshub" {
		t.Fatalf("rsshub source=%q", rsshub.source)
	}

	official, err := parseCommandFlags("fetch", []string{"--source=x"}, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if official.source != "x" {
		t.Fatalf("official source=%q", official.source)
	}
}

func TestParseCommandFlagsRejectsUnknownSourceAdapter(t *testing.T) {
	if _, err := parseCommandFlags("fetch", []string{"--source=unknown"}, io.Discard, false); err == nil || !strings.Contains(err.Error(), "source must be x or rsshub") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunNoArgsUsageOmitsPreflight(t *testing.T) {
	err := run(nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("no-argument invocation unexpectedly succeeded")
	}
	message := err.Error()
	for _, command := range []string{"fetch", "refresh", "rebuild", "verify", "verify-avatars"} {
		if !strings.Contains(message, command) {
			t.Fatalf("usage missing %q: %s", command, message)
		}
	}
	if strings.Contains(message, "preflight") {
		t.Fatalf("usage still advertises removed preflight command: %s", message)
	}
}

func TestRunRejectsRemovedPreflightCommandWithoutNetwork(t *testing.T) {
	err := run([]string{"preflight"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `unknown devdata command "preflight"`) {
		t.Fatalf("removed command error=%v", err)
	}
}

func TestWriteMediaLocalizationWarningOnlyReportsFailures(t *testing.T) {
	var output strings.Builder
	writeMediaLocalizationWarning(&output, 1, 2)
	if got := output.String(); got != "WARN: media localization completed with failures: avatars=1 post_media=2\n" {
		t.Fatalf("warning=%q", got)
	}

	output.Reset()
	writeMediaLocalizationWarning(&output, 0, 0)
	if output.Len() != 0 {
		t.Fatalf("unexpected warning for successful localization: %q", output.String())
	}
}
