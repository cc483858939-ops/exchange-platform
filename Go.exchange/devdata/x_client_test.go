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
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{"id": "456", "author_id": "123", "text": "source post", "created_at": "2026-01-01T00:00:00Z", "note_tweet": map[string]string{"text": "long source post"}}},
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
}

func TestNewXClientRequiresBearerToken(t *testing.T) {
	if _, err := NewXClient("https://api.x.com", "", nil); err == nil || !strings.Contains(err.Error(), "X_BEARER_TOKEN") {
		t.Fatalf("error=%v", err)
	}
}

var _ SnapshotSourceClient = (*XClient)(nil)
