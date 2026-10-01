package controllers

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestPostRelationPaginationLimitDefaultsClampsAndRejectsInvalidValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		query string
		want  int
		bad   bool
	}{
		{want: defaultPostRelationLimit},
		{query: "?limit=2", want: 2},
		{query: "?limit=50", want: 50},
		{query: "?limit=51", want: maxPostRelationLimit},
		{query: "?limit=0", bad: true},
		{query: "?limit=-1", bad: true},
		{query: "?limit=abc", bad: true},
	} {
		ctx, _ := newReplyUnitContext(http.MethodGet, "/api/posts/1/quotes"+test.query, "1", nil)
		got, err := parsePostRelationLimit(ctx)
		if test.bad {
			if err == nil {
				t.Errorf("query %q unexpectedly parsed as limit %d", test.query, got)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("query %q: limit=%d err=%v, want %d", test.query, got, err, test.want)
		}
	}
}

func TestPostRelationPaginationCursorRoundTripAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	want := postRelationCursor{
		CreatedAt: time.Date(2026, 9, 20, 11, 12, 13, 456789123, time.UTC),
		ID:        42,
	}
	encoded, err := encodePostRelationCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePostRelationCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("decoded=%#v want=%#v", got, want)
	}

	validCtx, _ := newReplyUnitContext(http.MethodGet, "/api/posts/1/quotes?cursor="+encoded, "1", nil)
	parsed, err := parsePostRelationCursor(validCtx)
	if err != nil || parsed == nil || parsed.ID != want.ID || !parsed.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("parsed cursor=%#v err=%v", parsed, err)
	}

	for _, query := range []string{"?cursor=", "?cursor=malformed"} {
		ctx, _ := newReplyUnitContext(http.MethodGet, "/api/posts/1/quotes"+query, "1", nil)
		if _, err := parsePostRelationCursor(ctx); err == nil {
			t.Errorf("query %q should be rejected", query)
		}
	}
	if cursor, err := parsePostRelationCursor(mustRelationContext(t, "/api/posts/1/quotes")); err != nil || cursor != nil {
		t.Fatalf("absent cursor=%#v err=%v, want nil cursor", cursor, err)
	}
}

func mustRelationContext(t *testing.T, target string) *gin.Context {
	t.Helper()
	ctx, _ := newReplyUnitContext(http.MethodGet, target, "1", nil)
	return ctx
}
