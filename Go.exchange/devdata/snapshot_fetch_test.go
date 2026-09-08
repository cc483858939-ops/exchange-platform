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

func TestSnapshotMediaValidationPreservesMarkerSemantics(t *testing.T) {
	validMedia := SnapshotMedia{Type: "image", SourceURL: "https://pbs.twimg.com/media/one.jpg", Width: 120, Height: 80}
	valid := testSnapshot("猫")
	valid.Posts[0].HasMedia = true
	valid.Posts[0].Media = []SnapshotMedia{validMedia}
	if err := ValidateSnapshot(valid, testRegistry()); err != nil {
		t.Fatalf("valid media snapshot rejected: %v", err)
	}
	markerOnlyShort := testSnapshot("猫")
	markerOnlyShort.Posts[0].HasMedia = true
	if err := ValidateSnapshot(markerOnlyShort, testRegistry()); err == nil {
		t.Fatal("short marker-only snapshot unexpectedly accepted")
	}
	markerOnly := testSnapshot(strings.Repeat("m", MinTextRunes))
	markerOnly.Posts[0].HasMedia = true
	if err := ValidateSnapshot(markerOnly, testRegistry()); err != nil {
		t.Fatalf("marker-only snapshot rejected: %v", err)
	}
	actualImageWithoutMarker := testSnapshot("猫")
	actualImageWithoutMarker.Posts[0].Media = []SnapshotMedia{validMedia}
	if err := ValidateSnapshot(actualImageWithoutMarker, testRegistry()); err == nil {
		t.Fatal("snapshot image without source media marker unexpectedly accepted")
	}
	for _, test := range []struct {
		name  string
		media []SnapshotMedia
		mark  bool
	}{
		{name: "media without marker", media: []SnapshotMedia{validMedia}, mark: false},
		{name: "unsupported type", media: []SnapshotMedia{{Type: "video", SourceURL: validMedia.SourceURL}}, mark: true},
		{name: "negative dimensions", media: []SnapshotMedia{{Type: "image", SourceURL: validMedia.SourceURL, Width: -1}}, mark: true},
		{name: "wrong host", media: []SnapshotMedia{{Type: "image", SourceURL: "https://cdn.example.test/image.jpg"}}, mark: true},
		{name: "userinfo", media: []SnapshotMedia{{Type: "image", SourceURL: "https://user:pass@pbs.twimg.com/image.jpg"}}, mark: true},
		{name: "custom port", media: []SnapshotMedia{{Type: "image", SourceURL: "https://pbs.twimg.com:443/image.jpg"}}, mark: true},
		{name: "duplicate URL", media: []SnapshotMedia{{Type: "image", SourceURL: validMedia.SourceURL}, {Type: "image", SourceURL: validMedia.SourceURL}}, mark: true},
		{name: "too many", media: []SnapshotMedia{{Type: "image", SourceURL: "https://pbs.twimg.com/1.jpg"}, {Type: "image", SourceURL: "https://pbs.twimg.com/2.jpg"}, {Type: "image", SourceURL: "https://pbs.twimg.com/3.jpg"}, {Type: "image", SourceURL: "https://pbs.twimg.com/4.jpg"}, {Type: "image", SourceURL: "https://pbs.twimg.com/5.jpg"}}, mark: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testSnapshot(strings.Repeat("m", MinTextRunes))
			snapshot.Posts[0].HasMedia = test.mark
			snapshot.Posts[0].Media = test.media
			if err := ValidateSnapshot(snapshot, testRegistry()); err == nil {
				t.Fatal("invalid media snapshot was accepted")
			}
		})
	}
}

func TestEligibleSourcePostFiltersRootContent(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := XPost{ID: "1", AuthorID: "123", CreatedAt: now, Text: "a valid root Post"}
	if ok, reason := EligibleSourcePost(base, "123"); !ok || reason != "" {
		t.Fatalf("base eligibility=%t reason=%q", ok, reason)
	}
	missingID := base
	missingID.ID = ""
	if ok, reason := EligibleSourcePost(missingID, "123"); ok || reason != "missing_id" {
		t.Fatalf("missing ID eligibility=%t reason=%q", ok, reason)
	}
	differentAuthor := base
	differentAuthor.AuthorID = "456"
	if ok, reason := EligibleSourcePost(differentAuthor, "123"); ok || reason != "different_author" {
		t.Fatalf("different author eligibility=%t reason=%q", ok, reason)
	}
	missingCreatedAt := base
	missingCreatedAt.CreatedAt = time.Time{}
	if ok, reason := EligibleSourcePost(missingCreatedAt, "123"); ok || reason != "missing_created_at" {
		t.Fatalf("missing created_at eligibility=%t reason=%q", ok, reason)
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
	tooLong := base
	tooLong.Text = strings.Repeat("x", MaxTextRunes+1)
	if ok, reason := EligibleSourcePost(tooLong, "123"); ok || reason != "text_too_long" {
		t.Fatalf("long eligibility=%t reason=%q", ok, reason)
	}
}

func TestEligibleSourcePostUsesSupportedImageForTextEligibility(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validImage := []SourceMedia{{Type: "image", URL: "https://pbs.twimg.com/media/test.jpg"}}
	tests := []struct {
		name      string
		text      string
		media     []SourceMedia
		mediaKeys []string
		want      bool
		reason    string
	}{
		{name: "image plus one rune", text: "猫", media: validImage, want: true},
		{name: "image plus emoji", text: "🌅", media: validImage, want: true},
		{name: "image plus empty text", media: validImage, reason: "empty_text"},
		{name: "image plus whitespace text", text: "   ", media: validImage, reason: "empty_text"},
		{name: "text only nine runes", text: strings.Repeat("a", MinTextRunes-1), reason: "short_text"},
		{name: "text only ten runes", text: strings.Repeat("a", MinTextRunes), want: true},
		{name: "unsupported video plus short text", text: "😂", mediaKeys: []string{"video-1"}, reason: "short_text"},
		{name: "supported image without source marker", text: "猫", media: validImage, want: true},
		{name: "image over maximum", text: strings.Repeat("x", MaxTextRunes+1), media: validImage, reason: "text_too_long"},
		{name: "text only over maximum", text: strings.Repeat("x", MaxTextRunes+1), reason: "text_too_long"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := XPost{
				ID: "1", AuthorID: "123", CreatedAt: now, Text: test.text,
				Attachments: XAttachments{MediaKeys: test.mediaKeys}, Media: test.media,
			}
			if ok, reason := EligibleSourcePost(post, "123"); ok != test.want || reason != test.reason {
				t.Fatalf("eligibility=%t reason=%q want %t/%q", ok, reason, test.want, test.reason)
			}
		})
	}
}

func TestBuildSnapshotPostPreservesSourceMediaMarkerSemantics(t *testing.T) {
	account := testRegistry().Accounts[0]
	imagePost := XPost{
		ID: "1", AuthorID: "123", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Text: "猫",
		Media: []SourceMedia{{Type: "image", URL: "https://pbs.twimg.com/media/test.jpg"}},
	}
	builtImage := BuildSnapshotPost(account, imagePost)
	if builtImage.HasMedia || len(builtImage.Media) != 1 {
		t.Fatalf("image without source marker=%#v", builtImage)
	}
	markerPost := imagePost
	markerPost.Media = nil
	markerPost.Attachments.MediaKeys = []string{"video-1"}
	builtMarker := BuildSnapshotPost(account, markerPost)
	if !builtMarker.HasMedia || len(builtMarker.Media) != 0 {
		t.Fatalf("source media marker=%#v", builtMarker)
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
	if len(snapshot.Accounts) != 20 || len(snapshot.Posts) != 20 {
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
