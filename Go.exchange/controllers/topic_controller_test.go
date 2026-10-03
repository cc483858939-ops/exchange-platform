package controllers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newTopicControllerContext(path, slug string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Params = gin.Params{{Key: "slug", Value: slug}}
	return ctx, recorder
}

func topicTestCatalog() config.CuratedTopicsConfig {
	return config.CuratedTopicsConfig{
		Version: config.CuratedTopicsVersion,
		Topics: []config.CuratedTopic{
			{Slug: "topic-one", Label: "Topic One", Description: "A test topic", SourceKeys: []string{"included", "disabled"}, Enabled: true},
			{Slug: "hidden", Label: "Hidden", Description: "Disabled topic", SourceKeys: []string{"other"}, Enabled: false},
		},
	}
}

func TestGetTopicsReturnsEnabledSummariesInConfigOrder(t *testing.T) {
	originalLoader := loadTopicConfiguration
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) { return topicTestCatalog(), nil }
	t.Cleanup(func() { loadTopicConfiguration = originalLoader })

	ctx, recorder := newTopicControllerContext("/api/topics", "")
	GetTopics(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "source_keys") {
		t.Fatalf("public topic list leaked source keys: %s", recorder.Body.String())
	}
	var response topicListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0] != (topicSummaryResponse{Slug: "topic-one", Label: "Topic One", Description: "A test topic"}) {
		t.Fatalf("items=%+v", response.Items)
	}
}

func TestGetTopicsHidesConfigurationErrors(t *testing.T) {
	originalLoader := loadTopicConfiguration
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) {
		return config.CuratedTopicsConfig{}, errors.New("private config path")
	}
	t.Cleanup(func() { loadTopicConfiguration = originalLoader })

	ctx, recorder := newTopicControllerContext("/api/topics", "")
	GetTopics(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "private config path") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetTopicPostsParsesLimitsAndReturnsEmptyArray(t *testing.T) {
	originalConfigLoader, originalPageLoader := loadTopicConfiguration, loadTopicPostsPage
	originalDB := global.APIDb
	global.APIDb = &gorm.DB{}
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) { return topicTestCatalog(), nil }
	var gotLimits []int
	loadTopicPostsPage = func(_ context.Context, _ config.CuratedTopic, limit int, cursor *topicPostCursorV2) (postPageResponse, error) {
		if cursor != nil {
			t.Fatalf("unexpected cursor: %+v", cursor)
		}
		gotLimits = append(gotLimits, limit)
		return postPageResponse{Items: make([]postResponse, 0)}, nil
	}
	t.Cleanup(func() {
		loadTopicConfiguration, loadTopicPostsPage, global.APIDb = originalConfigLoader, originalPageLoader, originalDB
	})

	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/api/topics/topic-one/posts", want: defaultTopicPostLimit},
		{path: "/api/topics/topic-one/posts?limit=100", want: maxTopicPostLimit},
	} {
		ctx, recorder := newTopicControllerContext(test.path, "topic-one")
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"items":[]`) || !strings.Contains(recorder.Body.String(), `"next_cursor":null`) {
			t.Fatalf("path=%q status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
		if got := gotLimits[len(gotLimits)-1]; got != test.want {
			t.Fatalf("path=%q limit=%d, want %d", test.path, got, test.want)
		}
	}
}

func TestGetTopicPostsRejectsBadQueriesAndUnknownTopics(t *testing.T) {
	originalConfigLoader, originalPageLoader := loadTopicConfiguration, loadTopicPostsPage
	originalDB := global.APIDb
	global.APIDb = &gorm.DB{}
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) { return topicTestCatalog(), nil }
	loadTopicPostsPage = func(context.Context, config.CuratedTopic, int, *topicPostCursorV2) (postPageResponse, error) {
		t.Fatal("page loader should not run for invalid input")
		return postPageResponse{}, nil
	}
	t.Cleanup(func() {
		loadTopicConfiguration, loadTopicPostsPage, global.APIDb = originalConfigLoader, originalPageLoader, originalDB
	})

	for _, rawLimit := range []string{"0", "-1", "bad"} {
		ctx, recorder := newTopicControllerContext("/api/topics/topic-one/posts?limit="+rawLimit, "topic-one")
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q status=%d body=%s", rawLimit, recorder.Code, recorder.Body.String())
		}
	}
	for _, rawCursor := range []string{"bad", base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-01-01T00:00:00Z"}`))} {
		ctx, recorder := newTopicControllerContext("/api/topics/topic-one/posts?cursor="+rawCursor, "topic-one")
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("cursor=%q status=%d body=%s", rawCursor, recorder.Code, recorder.Body.String())
		}
	}
	for _, slug := range []string{"unknown", "hidden"} {
		ctx, recorder := newTopicControllerContext("/api/topics/"+slug+"/posts", slug)
		GetTopicPosts(ctx)
		if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "topic not found") {
			t.Fatalf("slug=%q status=%d body=%s", slug, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTopicCursorV2RoundTripAndValidation(t *testing.T) {
	topic := topicTestCatalog().Topics[0]
	topicHash, err := topicCursorCriteriaHash(topic)
	if err != nil {
		t.Fatal(err)
	}
	want := topicPostCursorV2{
		Version: topicPostCursorVersion, TopicHash: topicHash,
		AnchorAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
		Seed:     0, Bucket: 2, ShuffleKey: 0, PostID: 42,
	}
	raw, err := encodeTopicPostCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeTopicPostCursor(raw, topicHash)
	if err != nil || got != want {
		t.Fatalf("decoded cursor=%+v err=%v, want %+v", got, err, want)
	}

	invalid := []struct {
		name   string
		mutate func(*topicPostCursorV2)
	}{
		{name: "unsupported version", mutate: func(c *topicPostCursorV2) { c.Version++ }},
		{name: "malformed topic hash", mutate: func(c *topicPostCursorV2) { c.TopicHash = "not-a-hash" }},
		{name: "zero anchor", mutate: func(c *topicPostCursorV2) { c.AnchorAt = time.Time{} }},
		{name: "future anchor", mutate: func(c *topicPostCursorV2) { c.AnchorAt = time.Now().UTC().Add(24 * time.Hour) }},
		{name: "negative bucket", mutate: func(c *topicPostCursorV2) { c.Bucket = -1 }},
		{name: "out of range bucket", mutate: func(c *topicPostCursorV2) { c.Bucket = maxTopicBucketIndex(c.AnchorAt) + 1 }},
		{name: "zero post id", mutate: func(c *topicPostCursorV2) { c.PostID = 0 }},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			cursor := want
			test.mutate(&cursor)
			encoded, err := json.Marshal(cursor)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeTopicPostCursor(base64.RawURLEncoding.EncodeToString(encoded), topicHash); err == nil {
				t.Fatalf("invalid cursor was accepted: %+v", cursor)
			}
		})
	}
	for _, test := range []struct {
		name string
		json string
	}{
		{name: "invalid JSON", json: `{"v":`},
		{name: "unknown field", json: `{"v":2,"topic_hash":"` + topicHash + `","anchor_at":"2026-09-20T12:00:00Z","seed":0,"bucket":0,"shuffle_key":0,"post_id":42,"extra":true}`},
		{name: "trailing JSON", json: `{"v":2,"topic_hash":"` + topicHash + `","anchor_at":"2026-09-20T12:00:00Z","seed":0,"bucket":0,"shuffle_key":0,"post_id":42}{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := base64.RawURLEncoding.EncodeToString([]byte(test.json))
			if _, err := decodeTopicPostCursor(raw, topicHash); err == nil {
				t.Fatalf("invalid cursor JSON was accepted: %s", test.json)
			}
		})
	}
	for _, raw := range []string{"%%%", "", "a"} {
		if _, err := decodeTopicPostCursor(raw, topicHash); err == nil {
			t.Fatalf("malformed Base64URL cursor %q was accepted", raw)
		}
	}
	if _, err := decodeTopicPostCursor(raw, strings.Repeat("0", 64)); err == nil {
		t.Fatal("cursor for a different topic configuration was accepted")
	}
}

func TestTopicCursorCriteriaHashBindsStableConfiguration(t *testing.T) {
	topic := config.CuratedTopic{Slug: "topic-one", SourceKeys: []string{"source-b", "source-a"}}
	reordered := config.CuratedTopic{Slug: "topic-one", SourceKeys: []string{"source-a", "source-b"}}
	want, err := topicCursorCriteriaHash(topic)
	if err != nil {
		t.Fatal(err)
	}
	got, err := topicCursorCriteriaHash(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("reordered source keys changed topic hash: %s != %s", got, want)
	}
	if !reflect.DeepEqual(topic.SourceKeys, []string{"source-b", "source-a"}) {
		t.Fatalf("hash computation mutated config source keys: %v", topic.SourceKeys)
	}
	changedSlug := reordered
	changedSlug.Slug = "topic-two"
	changedSource := reordered
	changedSource.SourceKeys = []string{"source-a", "source-c"}
	changedDuration, err := topicCursorCriteriaHashForBucketDuration(topic, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for name, variant := range map[string]config.CuratedTopic{"slug": changedSlug, "sources": changedSource} {
		variantHash, err := topicCursorCriteriaHash(variant)
		if err != nil {
			t.Fatal(err)
		}
		if variantHash == want {
			t.Errorf("changing %s did not change topic hash", name)
		}
	}
	if changedDuration == want {
		t.Fatal("changing bucket duration did not change topic hash")
	}
}

func TestTopicBucketRangesAndBoundaries(t *testing.T) {
	anchor := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for bucket, expected := range []struct{ start, end time.Time }{
		{start: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), end: anchor},
		{start: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), end: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)},
		{start: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), end: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)},
	} {
		start, end, err := topicBucketRange(anchor, int64(bucket))
		if err != nil || !start.Equal(expected.start) || !end.Equal(expected.end) {
			t.Fatalf("bucket %d range=(%s,%s] err=%v, want (%s,%s]", bucket, start, end, err, expected.start, expected.end)
		}
		startBucket, err := topicBucketIndex(anchor, start)
		if err != nil || startBucket != int64(bucket+1) {
			t.Fatalf("bucket %d exclusive start maps to bucket=%d err=%v", bucket, startBucket, err)
		}
		endBucket, err := topicBucketIndex(anchor, end)
		if err != nil || endBucket != int64(bucket) {
			t.Fatalf("bucket %d inclusive end maps to bucket=%d err=%v", bucket, endBucket, err)
		}
	}
	for _, test := range []struct {
		createdAt time.Time
		want      int64
	}{
		{createdAt: anchor, want: 0},
		{createdAt: anchor.Add(-topicShuffleBucketDuration + time.Nanosecond), want: 0},
		{createdAt: anchor.Add(-topicShuffleBucketDuration), want: 1},
		{createdAt: anchor.Add(-2 * topicShuffleBucketDuration), want: 2},
	} {
		got, err := topicBucketIndex(anchor, test.createdAt)
		if err != nil || got != test.want {
			t.Fatalf("timestamp %s bucket=%d err=%v, want %d", test.createdAt, got, err, test.want)
		}
	}
}

func TestTopicBucketCandidateQueryUsesParameterizedShuffleKeyset(t *testing.T) {
	topic := config.CuratedTopic{Slug: "topic-one", SourceKeys: []string{"source-a", "source-b"}}
	anchor := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	after := &topicPostCursorV2{ShuffleKey: -17, PostID: 42}
	query, args, err := topicBucketCandidatesQuery(topic, anchor, -9, 2, after, 21)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"hashint8extended(mirror_posts.local_post_id::bigint, ?::bigint)",
		"mirror_posts.imported_at <= ?",
		"mirror_posts.source_created_at > ?",
		"mirror_posts.source_created_at <= ?",
		"shuffle_key < ? OR (shuffle_key = ? AND id < ?)",
		"ORDER BY shuffle_key DESC, id DESC LIMIT ?",
	} {
		if !strings.Contains(query, fragment) {
			t.Errorf("candidate query missing %q: %s", fragment, query)
		}
	}
	if strings.Contains(strings.ToLower(query), "order by random()") {
		t.Fatal("candidate query uses ORDER BY random()")
	}
	if len(args) != 10 || args[0] != int64(-9) || args[6] != int64(-17) || args[7] != int64(-17) || args[8] != uint(42) || args[9] != 21 {
		t.Fatalf("candidate query args=%#v", args)
	}
	start, end, err := topicBucketRange(anchor, 2)
	if err != nil || !args[4].(time.Time).Equal(start) || !args[5].(time.Time).Equal(end) {
		t.Fatalf("bucket range args=(%v,%v] err=%v, want (%v,%v]", args[4], args[5], err, start, end)
	}
}

func TestResolveTopicTraversalGeneratesSeedOnceAndReusesCursor(t *testing.T) {
	originalGenerator := generateTopicTraversalSeed
	calls := 0
	generateTopicTraversalSeed = func() (int64, error) {
		calls++
		return 0, nil
	}
	t.Cleanup(func() { generateTopicTraversalSeed = originalGenerator })

	topic := topicTestCatalog().Topics[0]
	first, err := resolveTopicTraversal(topic, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || first.Seed != 0 || first.Bucket != 0 || first.AnchorAt.IsZero() {
		t.Fatalf("initial traversal=%+v seed calls=%d", first, calls)
	}
	cursor := topicPostCursorV2{
		Version: topicPostCursorVersion, TopicHash: first.TopicHash, AnchorAt: first.AnchorAt,
		Seed: first.Seed, Bucket: 3, ShuffleKey: 0, PostID: 42,
	}
	continued, err := resolveTopicTraversal(topic, &cursor)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || continued.Seed != first.Seed || !continued.AnchorAt.Equal(first.AnchorAt) || continued.After != &cursor || continued.Bucket != cursor.Bucket {
		t.Fatalf("continuation=%+v seed calls=%d", continued, calls)
	}
}
