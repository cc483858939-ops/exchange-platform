package main

import (
	"io"
	"strings"
	"testing"
)

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
