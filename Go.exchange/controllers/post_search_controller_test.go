package controllers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func parsePostSearchTestCriteria(t *testing.T, rawQuery string) (postSearchCriteria, error) {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/api/posts/search?"+rawQuery, nil)
	return parsePostSearchCriteria(ctx)
}

func TestParsePostSearchCriteriaValidatesNormalizedQuery(t *testing.T) {
	for _, query := range []string{"q=%E4%B8%80", "q=" + strings.Repeat("a", 201)} {
		if _, err := parsePostSearchTestCriteria(t, query); err == nil {
			t.Fatalf("parsePostSearchCriteria(%q) unexpectedly succeeded", query)
		}
	}
	criteria, err := parsePostSearchTestCriteria(t, "q=%20%40%23%20")
	if err != nil {
		t.Fatal(err)
	}
	if criteria.Query != "@#" || criteria.Sort != "latest" || criteria.Limit != postSearchDefaultLimit {
		t.Fatalf("parsed criteria=%+v", criteria)
	}
	if _, err := parsePostSearchTestCriteria(t, "q=%E6%97%A5%E5%85%83"); err != nil {
		t.Fatalf("two-rune query rejected: %v", err)
	}
}

func TestParsePostSearchCriteriaValidatesFilters(t *testing.T) {
	tests := []string{
		"q=JPY&author_id=0",
		"q=JPY&author_id=9007199254740992",
		"q=JPY&author_id=abc",
		"q=JPY&from=not-a-time",
		"q=JPY&to=not-a-time",
		"q=JPY&from=2026-10-02T00%3A00%3A00Z&to=2026-10-01T00%3A00%3A00Z",
		"q=JPY&sort=relevance",
		"q=JPY&limit=51",
		"q=JPY&limit=0",
	}
	for _, query := range tests {
		if _, err := parsePostSearchTestCriteria(t, query); err == nil {
			t.Errorf("parsePostSearchCriteria(%q) unexpectedly succeeded", query)
		}
	}
	criteria, err := parsePostSearchTestCriteria(t, "q=JPY&author_id=42&from=2026-09-01T08%3A00%3A00%2B08%3A00&to=2026-10-01T00%3A00%3A00Z&limit=50&sort=latest")
	if err != nil {
		t.Fatal(err)
	}
	if criteria.AuthorID == nil || *criteria.AuthorID != 42 || criteria.Limit != 50 || criteria.From.Location() != time.UTC {
		t.Fatalf("parsed criteria=%+v", criteria)
	}
	if got := criteria.From.Format(time.RFC3339); got != "2026-09-01T00:00:00Z" {
		t.Fatalf("from normalized to %q", got)
	}
}

func TestPostSearchLikePatternEscapesLiteralMetacharacters(t *testing.T) {
	if got, want := postSearchLikePattern(`a\%_`), `%a\\\%\_%`; got != want {
		t.Fatalf("postSearchLikePattern()=%q, want %q", got, want)
	}
}
