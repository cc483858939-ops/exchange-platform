package controllers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validPostSearchCursor() postSearchCursorV1 {
	return postSearchCursorV1{
		Version: 1, CriteriaHash: strings.Repeat("a", 64),
		AnchorAt:  time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC), PostID: 42,
	}
}

func TestPostSearchCursorRoundTrip(t *testing.T) {
	want := validPostSearchCursor()
	encoded, err := encodePostSearchCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePostSearchCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded cursor=%+v, want %+v", got, want)
	}
}

func TestPostSearchCursorRejectsMalformedShapes(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "unknown field", json: `{"v":1,"criteria_hash":"` + strings.Repeat("a", 64) + `","anchor_at":"2026-10-01T12:00:00Z","created_at":"2026-10-01T11:00:00Z","id":42,"extra":true}`},
		{name: "trailing JSON", json: `{"v":1,"criteria_hash":"` + strings.Repeat("a", 64) + `","anchor_at":"2026-10-01T12:00:00Z","created_at":"2026-10-01T11:00:00Z","id":42}{}`},
		{name: "unknown version", json: `{"v":2,"criteria_hash":"` + strings.Repeat("a", 64) + `","anchor_at":"2026-10-01T12:00:00Z","created_at":"2026-10-01T11:00:00Z","id":42}`},
		{name: "zero ID", json: `{"v":1,"criteria_hash":"` + strings.Repeat("a", 64) + `","anchor_at":"2026-10-01T12:00:00Z","created_at":"2026-10-01T11:00:00Z","id":0}`},
		{name: "zero timestamp", json: `{"v":1,"criteria_hash":"` + strings.Repeat("a", 64) + `","anchor_at":"0001-01-01T00:00:00Z","created_at":"2026-10-01T11:00:00Z","id":42}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodePostSearchCursor(base64.RawURLEncoding.EncodeToString([]byte(test.json))); err == nil {
				t.Fatal("decodePostSearchCursor unexpectedly succeeded")
			}
		})
	}
	for _, encoded := range []string{"", "%%%", "e30="} {
		if _, err := decodePostSearchCursor(encoded); err == nil {
			t.Fatalf("decodePostSearchCursor(%q) unexpectedly succeeded", encoded)
		}
	}
}

func TestEncodePostSearchCursorRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*postSearchCursorV1)
	}{
		{name: "version", change: func(cursor *postSearchCursorV1) { cursor.Version = 2 }},
		{name: "criteria hash", change: func(cursor *postSearchCursorV1) { cursor.CriteriaHash = "bad" }},
		{name: "anchor", change: func(cursor *postSearchCursorV1) { cursor.AnchorAt = time.Time{} }},
		{name: "created at", change: func(cursor *postSearchCursorV1) { cursor.CreatedAt = time.Time{} }},
		{name: "post ID", change: func(cursor *postSearchCursorV1) { cursor.PostID = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cursor := validPostSearchCursor()
			test.change(&cursor)
			if _, err := encodePostSearchCursor(cursor); err == nil {
				t.Fatal("encodePostSearchCursor unexpectedly succeeded")
			}
		})
	}
}

func TestPostSearchCriteriaHashIsCanonicalAndBound(t *testing.T) {
	author := uint(42)
	from := "2026-09-01T00:00:00Z"
	one, err := postSearchCriteriaHash(postSearchCursorCriteria{
		Query: "JPY", AuthorID: &author, From: &from, Sort: "latest",
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := postSearchCriteriaHash(postSearchCursorCriteria{
		Query: "USD", AuthorID: &author, From: &from, Sort: "latest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if one == other {
		t.Fatal("different queries unexpectedly produced the same criteria hash")
	}
	encoded, err := json.Marshal(postSearchCursorCriteria{Query: "JPY", AuthorID: &author, From: &from, Sort: "latest"})
	if err != nil || len(encoded) == 0 {
		t.Fatalf("marshal canonical criteria: %q %v", encoded, err)
	}
}
