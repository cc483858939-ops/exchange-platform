package devdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestXClientUsesV2LookupAndTimelineContracts(t *testing.T) {
	falseValue := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/2/users/by":
			if request.URL.Query().Get("usernames") != "source" {
				t.Fatalf("usernames=%q", request.URL.Query().Get("usernames"))
			}
			if !strings.Contains(request.URL.Query().Get("user.fields"), "protected") {
				t.Fatalf("user.fields=%q", request.URL.Query().Get("user.fields"))
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"data": []XUser{{ID: "123", Username: "source", Name: "Source", Protected: &falseValue}},
			})
		case "/2/users/123/tweets":
			query := request.URL.Query()
			if query.Get("exclude") != "replies,retweets" {
				t.Fatalf("exclude=%q", query.Get("exclude"))
			}
			if query.Get("max_results") != "100" {
				t.Fatalf("max_results=%q", query.Get("max_results"))
			}
			if !strings.Contains(query.Get("tweet.fields"), "article") || !strings.Contains(query.Get("tweet.fields"), "note_tweet") || !strings.Contains(query.Get("tweet.fields"), "referenced_tweets") {
				t.Fatalf("tweet.fields=%q", query.Get("tweet.fields"))
			}
			if query.Get("expansions") != "attachments.media_keys" || query.Get("media.fields") != "media_key,type,url,width,height" {
				t.Fatalf("media query expansions=%q fields=%q", query.Get("expansions"), query.Get("media.fields"))
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{"id": "456", "author_id": "123", "text": "source post", "created_at": "2026-01-01T00:00:00Z", "note_tweet": map[string]string{"text": "long source post"}, "attachments": map[string]interface{}{"media_keys": []string{"m1", "v1", "m2", "blank", "m3", "m1"}}}},
				"includes": map[string]interface{}{"media": []map[string]interface{}{
					{"media_key": "m1", "type": "photo", "url": "https://pbs.twimg.com/media/one.jpg", "width": 100, "height": 200},
					{"media_key": "v1", "type": "video", "url": "https://pbs.twimg.com/media/video.mp4", "width": 300, "height": 400},
					{"media_key": "m2", "type": "photo", "url": "https://pbs.twimg.com/media/two.png", "width": 300, "height": 400},
					{"media_key": "blank", "type": "photo", "url": "", "width": 500, "height": 600},
					{"media_key": "m3", "type": "photo", "url": "https://pbs.twimg.com/media/three.webp", "width": 700, "height": 800},
				}},
				"meta": map[string]interface{}{"result_count": 1, "next_token": "next"},
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
	users, err := client.LookupUsers(context.Background(), []string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	if users["source"].ID != "123" {
		t.Fatalf("users=%#v", users)
	}
	page, err := client.GetUserPosts(context.Background(), "123", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Posts) != 1 || page.NextToken != "next" || page.Posts[0].NoteTweet == nil {
		t.Fatalf("page=%#v", page)
	}
	if len(page.Posts[0].Attachments.MediaKeys) != 6 || len(page.Posts[0].Media) != 3 {
		t.Fatalf("media mapping=%#v", page.Posts[0])
	}
	if page.Posts[0].Media[0].URL != "https://pbs.twimg.com/media/one.jpg" || page.Posts[0].Media[1].URL != "https://pbs.twimg.com/media/two.png" || page.Posts[0].Media[2].Width != 700 {
		t.Fatalf("media order=%#v", page.Posts[0].Media)
	}
}

func TestNewXClientRequiresBearerToken(t *testing.T) {
	if _, err := NewXClient("https://api.x.com", "", nil); err == nil || !strings.Contains(err.Error(), "X_BEARER_TOKEN") {
		t.Fatalf("error=%v", err)
	}
}

func TestNormalizeXTimelineMediaIgnoresUnknownUnsupportedAndCapsPhotos(t *testing.T) {
	posts := []XPost{{Attachments: XAttachments{MediaKeys: []string{"unknown", "video", "photo-1", "photo-2", "photo-3", "photo-4", "photo-5"}}}}
	includes := []XMedia{
		{MediaKey: "video", Type: "video", URL: "https://pbs.twimg.com/video.mp4"},
		{MediaKey: "photo-1", Type: "photo", URL: "https://pbs.twimg.com/1.jpg"},
		{MediaKey: "photo-2", Type: "photo", URL: "https://pbs.twimg.com/2.jpg"},
		{MediaKey: "photo-3", Type: "photo", URL: "https://pbs.twimg.com/3.jpg"},
		{MediaKey: "photo-4", Type: "photo", URL: "https://pbs.twimg.com/4.jpg"},
		{MediaKey: "photo-5", Type: "photo", URL: "https://pbs.twimg.com/5.jpg"},
	}
	normalizeXTimelineMedia(posts, includes)
	if len(posts[0].Attachments.MediaKeys) != 7 || len(posts[0].Media) != 4 {
		t.Fatalf("post=%#v", posts[0])
	}
	for index, media := range posts[0].Media {
		if media.Type != "image" || media.URL != "https://pbs.twimg.com/"+string(rune('1'+index))+".jpg" {
			t.Fatalf("media[%d]=%#v", index, media)
		}
	}
}

var _ SnapshotSourceClient = (*XClient)(nil)
