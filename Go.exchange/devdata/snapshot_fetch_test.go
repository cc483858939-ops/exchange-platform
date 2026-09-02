package devdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRegistry() SourceRegistry {
	return SourceRegistry{
		Version:         SourceRegistryVersion,
		DefaultMaxPosts: 2,
		Accounts: []SourceAccount{{
			Key: "source", Platform: "x", Handle: "source", Category: "test", MaxPosts: 2, Enabled: true,
		}},
	}
}

func testSnapshot(text string) Snapshot {
	return Snapshot{
		Version:   DefaultSnapshotVersion,
		FetchedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Accounts: []SnapshotAccount{{
			RegistryKey: "source", SourceUserID: "123", Handle: "source", Name: "Source", Category: "test",
		}},
		Posts: []SnapshotPost{{
			RegistryKey: "source", SourcePostID: "456", SourceURL: "https://x.com/source/status/456",
			Text: text, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}},
	}
}

func TestNormalizeSourceTextAndLongFormSelection(t *testing.T) {
	if got, want := NormalizeSourceText("\u200b hello\r\nworld\uFEFF "), "hello\nworld"; got != want {
		t.Fatalf("normalized=%q want %q", got, want)
	}
	post := XPost{Text: "short fallback", NoteTweet: &XNoteTweet{Text: "a complete long-form source Post"}}
	if got := SourceText(post); got != "a complete long-form source Post" {
		t.Fatalf("source text=%q", got)
	}
	article := XPost{Text: "short fallback", Article: &XArticle{PlainText: "a complete article source Post"}}
	if got := SourceText(article); got != "a complete article source Post" {
		t.Fatalf("article source text=%q", got)
	}
}

func TestEligibleSourcePostFiltersRootContent(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := XPost{ID: "1", AuthorID: "123", CreatedAt: now, Text: "a valid root Post"}
	if ok, reason := EligibleSourcePost(base, "123"); !ok || reason != "" {
		t.Fatalf("base eligibility=%t reason=%q", ok, reason)
	}
	reply := base
	reply.InReplyToUserID = stringPointer("123")
	if ok, reason := EligibleSourcePost(reply, "123"); ok || reason != "reply" {
		t.Fatalf("reply eligibility=%t reason=%q", ok, reason)
	}
	referenced := base
	referenced.ReferencedTweets = []XReferencedTweet{{Type: "quoted", ID: "2"}}
	if ok, reason := EligibleSourcePost(referenced, "123"); ok || reason != "referenced_post" {
		t.Fatalf("referenced eligibility=%t reason=%q", ok, reason)
	}
	missingAuthor := base
	missingAuthor.AuthorID = ""
	if ok, reason := EligibleSourcePost(missingAuthor, "123"); ok || reason != "missing_author" {
		t.Fatalf("missing author eligibility=%t reason=%q", ok, reason)
	}
	sensitive := base
	sensitive.PossiblySensitive = true
	if ok, reason := EligibleSourcePost(sensitive, "123"); ok || reason != "possibly_sensitive" {
		t.Fatalf("sensitive eligibility=%t reason=%q", ok, reason)
	}
	media := base
	media.Attachments.MediaKeys = []string{"3"}
	if ok, reason := EligibleSourcePost(media, "123"); ok || reason != "media_dependent_text" {
		t.Fatalf("media eligibility=%t reason=%q", ok, reason)
	}
	media.Text = strings.Repeat("m", MinTextRunesWithMedia)
	if ok, reason := EligibleSourcePost(media, "123"); !ok || reason != "" {
		t.Fatalf("media long eligibility=%t reason=%q", ok, reason)
	}
	tooLong := base
	tooLong.Text = strings.Repeat("x", MaxTextRunes+1)
	if ok, reason := EligibleSourcePost(tooLong, "123"); ok || reason != "text_too_long" {
		t.Fatalf("long eligibility=%t reason=%q", ok, reason)
	}
}

func TestFetchSnapshotUsesBoundedPaginationAndDesiredFilters(t *testing.T) {
	registry := testRegistry()
	falseValue := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/2/users/by":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"data": []XUser{{ID: "123", Username: "source", Name: "Source", Protected: &falseValue}},
			})
		case "/2/users/123/tweets":
			if request.URL.Query().Get("pagination_token") == "next" {
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"data": []XPost{{ID: "3", AuthorID: "123", Text: "second valid source Post", CreatedAt: time.Date(2025, 12, 30, 0, 0, 0, 0, time.UTC)}},
				})
				return
			}
			reply := "123"
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"data": []interface{}{
					XPost{ID: "1", AuthorID: "123", Text: "first valid source Post", CreatedAt: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)},
					XPost{ID: "2", AuthorID: "123", Text: "tiny", CreatedAt: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)},
					XPost{ID: "4", AuthorID: "123", Text: "reply content that should be excluded", CreatedAt: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), InReplyToUserID: &reply},
				},
				"meta": map[string]interface{}{"result_count": 3, "next_token": "next"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := NewXClient(server.URL, "test-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, report, err := FetchSnapshot(context.Background(), client, registry, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Posts) != 2 || report.APIRequests != 3 || report.SourcePostsScanned != 4 || report.EligibleSelected != 2 {
		t.Fatalf("posts=%d report=%#v", len(snapshot.Posts), report)
	}
	if snapshot.Posts[0].SourcePostID != "1" || snapshot.Posts[1].SourcePostID != "3" {
		t.Fatalf("posts=%#v", snapshot.Posts)
	}
}

func TestSnapshotAtomicWriteReplacesOnlyAfterValidation(t *testing.T) {
	registry := testRegistry()
	directory := t.TempDir()
	path := filepath.Join(directory, "x_latest.json")
	first := testSnapshot("first valid source Post")
	if err := WriteSnapshotAtomic(path, first, registry); err != nil {
		t.Fatal(err)
	}
	second := testSnapshot("second valid source Post")
	second.Posts[0].SourcePostID = "457"
	second.Posts[0].SourceURL = "https://x.com/source/status/457"
	if err := WriteSnapshotAtomic(path, second, registry); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadSnapshot(path, registry)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Posts[0].SourcePostID != "457" {
		t.Fatalf("loaded=%#v", loaded.Posts)
	}
	invalid := second
	invalid.Posts[0].Text = "short"
	if err := WriteSnapshotAtomic(path, invalid, registry); err == nil {
		t.Fatal("invalid snapshot unexpectedly wrote")
	}
	unchanged, err := ReadSnapshot(path, registry)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Posts[0].SourcePostID != "457" {
		t.Fatalf("target changed after invalid write: %#v", unchanged.Posts)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestShippedSnapshotFixtureIsValidTestData(t *testing.T) {
	registry, err := LoadCuratedRegistry(filepath.Join("testdata", "x_sources_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(filepath.Join("testdata", "x_snapshot_fixture_test.json"), registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Accounts) != 15 || len(snapshot.Posts) != 15 {
		t.Fatalf("fixture accounts=%d posts=%d", len(snapshot.Accounts), len(snapshot.Posts))
	}
}

func TestSourceIDsAreBoundedNumericIdentifiers(t *testing.T) {
	if !isNumericSourceID("1234567890123456789") {
		t.Fatal("19-digit source ID should be accepted")
	}
	if isNumericSourceID(strings.Repeat("1", 20)) {
		t.Fatal("20-digit source ID should be rejected")
	}
}

func stringPointer(value string) *string { return &value }
