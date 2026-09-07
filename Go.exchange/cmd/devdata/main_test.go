package main

import (
	"io"
	"strings"
	"testing"
)

func TestParseCommandFlagsDefaultsToRSSHub(t *testing.T) {
	options, err := parseCommandFlags("fetch", nil, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if options.source != "rsshub" {
		t.Fatalf("default source=%q", options.source)
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
