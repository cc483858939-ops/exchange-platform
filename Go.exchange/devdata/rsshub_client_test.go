package devdata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const rssHubTestFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>Twitter @Marques Brownlee</title>
    <description><![CDATA[Technology &amp; hardware feed - Powered by RSSHub]]></description>
    <link>https://twitter.com/MKBHD</link>
    <image><url>https://cdn.example.test/mkbhd.png</url></image>
    <item>
      <title><![CDATA[This is a valid RSSHub source post]]></title>
      <description><![CDATA[<p>This is a valid RSSHub source post</p>]]></description>
      <guid isPermaLink="false">https://twitter.com/MKBHD/status/1001</guid>
      <link>https://x.com/MKBHD/status/1001</link>
      <pubDate>Tue, 01 Sep 2026 10:56:56 GMT</pubDate>
      <author>MKBHD</author>
    </item>
    <item>
      <title>tiny</title>
      <guid isPermaLink="false">https://twitter.com/MKBHD/status/1002</guid>
      <link>https://x.com/MKBHD/status/1002</link>
      <pubDate>Tue, 01 Sep 2026 10:55:56 GMT</pubDate>
      <author>MKBHD</author>
    </item>
    <item>
      <title><![CDATA[This root quote has enough standalone text to be rejected]]></title>
      <description><![CDATA[<p>This root quote has enough standalone text to be rejected</p><div class="rsshub-quote"><a href="https://x.com/Other/status/9001">Quoted source text</a></div>]]></description>
      <guid isPermaLink="false">https://twitter.com/MKBHD/status/1004</guid>
      <link>https://x.com/MKBHD/status/1004</link>
      <pubDate>Tue, 01 Sep 2026 10:54:30 GMT</pubDate>
      <author>MKBHD</author>
    </item>
    <item>
      <title><![CDATA[Another valid RSSHub source post with enough standalone text]]></title>
      <description><![CDATA[<p>Another valid RSSHub source post with enough standalone text</p><img src="https://cdn.example.test/post.png" />]]></description>
      <guid isPermaLink="false">https://twitter.com/MKBHD/status/1003</guid>
      <link>https://x.com/MKBHD/status/1003</link>
      <pubDate>Tue, 01 Sep 2026 10:54:56 GMT</pubDate>
      <author>MKBHD</author>
      <media:content url="https://cdn.example.test/post.png" type="image/png" />
    </item>
    <item>
      <title></title>
      <description><![CDATA[<p>Line one &amp; detail</p><br />Line two has enough standalone text]]></description>
      <content:encoded><![CDATA[<p>Line one &amp; detail</p><br />Line two has enough standalone text]]></content:encoded>
      <guid isPermaLink="false">https://twitter.com/MKBHD/status/1005</guid>
      <link>https://x.com/MKBHD/status/1005</link>
      <pubDate>Tue, 01 Sep 2026 10:53:56 GMT</pubDate>
      <author>MKBHD</author>
    </item>
    <item>
      <title>short image</title>
      <description><![CDATA[<p>short image</p><img src="https://cdn.example.test/short.png" />]]></description>
      <guid isPermaLink="false">https://twitter.com/MKBHD/status/1006</guid>
      <link>https://x.com/MKBHD/status/1006</link>
      <pubDate>Tue, 01 Sep 2026 10:52:56 GMT</pubDate>
      <author>MKBHD</author>
    </item>
  </channel>
</rss>`

func TestRSSHubClientMapsFeedToExistingSourceContract(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		if request.URL.Path != "/twitter/user/MKBHD/count=60&includeReplies=false&includeRts=false&strict=true" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "" {
			t.Fatalf("RSSHub request unexpectedly carried authorization: %q", authorization)
		}
		writer.Header().Set("Content-Type", "application/rss+xml")
		_, _ = io.WriteString(writer, rssHubTestFeed)
	}))
	defer server.Close()

	client, err := NewRSSHubClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	users, err := client.LookupUsers(context.Background(), []string{"MKBHD"})
	if err != nil {
		t.Fatal(err)
	}
	user, ok := users["mkbhd"]
	if !ok {
		t.Fatalf("users=%#v", users)
	}
	if user.ID != "rsshub:mkbhd" || user.Username != "MKBHD" || user.Name != "Marques Brownlee" {
		t.Fatalf("user=%#v", user)
	}
	if user.Description != "Technology & hardware feed" || user.ProfileImageURL != "https://cdn.example.test/mkbhd.png" {
		t.Fatalf("profile=%#v", user)
	}
	if user.Protected == nil || *user.Protected {
		t.Fatalf("protected=%v", user.Protected)
	}

	page, err := client.GetUserPosts(context.Background(), user.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || client.RequestCount() != 1 {
		t.Fatalf("requestCount=%d clientCount=%d", requestCount, client.RequestCount())
	}
	if len(page.Posts) != 6 || page.ResultCount != 6 || page.NextToken != "" {
		t.Fatalf("page=%#v", page)
	}
	if page.Posts[0].ID != "1001" || page.Posts[0].Text != "This is a valid RSSHub source post" {
		t.Fatalf("first post=%#v", page.Posts[0])
	}
	if page.Posts[2].ID != "1004" || len(page.Posts[2].ReferencedTweets) != 1 || page.Posts[2].ReferencedTweets[0].Type != "quote" || page.Posts[2].ReferencedTweets[0].ID != "9001" {
		t.Fatalf("quote post=%#v", page.Posts[2])
	}
	if ok, reason := EligibleSourcePost(page.Posts[2], user.ID); ok || reason != "referenced_post" {
		t.Fatalf("quote eligibility=%t reason=%q", ok, reason)
	}
	if strings.Contains(page.Posts[2].Text, "Quoted source text") {
		t.Fatalf("quote card text leaked into root text=%q", page.Posts[2].Text)
	}
	if !page.Posts[3].CreatedAt.Equal(time.Date(2026, 9, 1, 10, 54, 56, 0, time.UTC)) || len(page.Posts[3].Attachments.MediaKeys) != 1 {
		t.Fatalf("media post=%#v", page.Posts[3])
	}
	if !strings.Contains(page.Posts[4].Text, "Line one & detail") || !strings.Contains(page.Posts[4].Text, "Line two") {
		t.Fatalf("multiline post=%#v", page.Posts[4])
	}
	if len(page.Posts[5].Attachments.MediaKeys) != 1 {
		t.Fatalf("short media markers=%#v", page.Posts[5])
	}
	if ok, reason := EligibleSourcePost(page.Posts[5], user.ID); ok || reason != "media_dependent_text" {
		t.Fatalf("short media eligibility=%t reason=%q", ok, reason)
	}
}

func TestRSSHubItemTextPrefersDescriptionOverTruncatedTitle(t *testing.T) {
	item := rssHubItem{
		Title:       "EVAN brings it on stage 🔥 He reflects on how performing solo differs from performing in a group. Watch more in his conversation with Billboard News...",
		Description: "<p>EVAN brings it on stage 🔥 He reflects on how performing solo differs from performing in a group.</p><p>Watch more in his conversation with Billboard News.</p>",
	}

	if got, want := rssHubItemText(item), "EVAN brings it on stage 🔥 He reflects on how performing solo differs from performing in a group.\nWatch more in his conversation with Billboard News."; got != want {
		t.Fatalf("item text=%q want %q", got, want)
	}
}

func TestRSSHubItemTextFallbackOrderAndQuoteProtection(t *testing.T) {
	tests := []struct {
		name string
		item rssHubItem
		want string
	}{
		{
			name: "encoded description when description is unavailable",
			item: rssHubItem{EncodedDescription: "<p>encoded</p>", Title: "truncated..."},
			want: "encoded",
		},
		{
			name: "title when both body fields are unavailable",
			item: rssHubItem{Title: "fallback"},
			want: "fallback",
		},
		{
			name: "title for quote post",
			item: rssHubItem{
				Title:       "root fallback",
				Description: `<p>root body</p><div class="rsshub-quote"><a href="https://x.com/Other/status/9001">quoted body</a></div>`,
			},
			want: "root fallback",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rssHubItemText(test.item); got != test.want {
				t.Fatalf("item text=%q want %q", got, test.want)
			}
		})
	}
}

func TestRSSHubContentTextNormalizesLineWhitespaceWithoutCollapsingInlineSpaces(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "paragraph tags", raw: "<p>first</p><p>second</p>", want: "first\nsecond"},
		{name: "br", raw: "first<br>second", want: "first\nsecond"},
		{name: "whitespace around block break", raw: "<p>first </p> <p> second</p>", want: "first\nsecond"},
		{name: "tabs around break", raw: "first\t<br>\tsecond", want: "first\nsecond"},
		{name: "inline spaces preserved", raw: "hello   world", want: "hello   world"},
		{name: "HTML entities", raw: "Tom &amp; Jerry", want: "Tom & Jerry"},
		{name: "NBSP", raw: "hello&nbsp;world", want: "hello world"},
		{name: "empty HTML", raw: "<p></p>", want: ""},
		{name: "CRLF normalized", raw: "first\r\nsecond", want: "first\nsecond"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rssHubContentText(test.raw); got != test.want {
				t.Fatalf("content text=%q want %q", got, test.want)
			}
		})
	}
}

func TestRSSHubFeedUsesExistingFetchFiltersAndSnapshotValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/rss+xml")
		_, _ = io.WriteString(writer, rssHubTestFeed)
	}))
	defer server.Close()

	registry := SourceRegistry{
		Version:         SourceRegistryVersion,
		DefaultMaxPosts: 2,
		Accounts: []SourceAccount{{
			Key: "MKBHD", Platform: "x", Handle: "MKBHD", Category: "test", MaxPosts: 2, Enabled: true,
		}},
	}
	client, err := NewRSSHubClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, report, err := FetchSnapshot(context.Background(), client, registry, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.APIRequests != 1 || report.SourcePostsScanned != 4 || report.EligibleSelected != 2 {
		t.Fatalf("report=%#v", report)
	}
	if len(snapshot.Posts) != 2 || snapshot.Accounts[0].SourceUserID != "rsshub:mkbhd" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Posts[0].SourcePostID != "1001" || snapshot.Posts[0].SourceURL != "https://x.com/MKBHD/status/1001" {
		t.Fatalf("posts=%#v", snapshot.Posts)
	}
	if snapshot.Posts[1].SourcePostID != "1003" || !snapshot.Posts[1].HasMedia {
		t.Fatalf("posts=%#v", snapshot.Posts)
	}
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		t.Fatal(err)
	}
}

func TestRSSHubSourceIdentityValidation(t *testing.T) {
	for _, value := range []string{"123", "rsshub:thsottiaux", "RSSHUB:MKBHD"} {
		if !isValidSourceUserID(value) {
			t.Fatalf("source ID %q should be valid", value)
		}
	}
	for _, value := range []string{"", "rsshub:bad-handle", "rsshub:too/many", strings.Repeat("1", 20)} {
		if isValidSourceUserID(value) {
			t.Fatalf("source ID %q should be invalid", value)
		}
	}
}

func TestRSSHubClientRejectsPagination(t *testing.T) {
	client, err := NewRSSHubClient("http://127.0.0.1:1200", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetUserPosts(context.Background(), "rsshub:mkbhd", "next", 100); err == nil || !strings.Contains(err.Error(), "does not support pagination") {
		t.Fatalf("error=%v", err)
	}
}

func TestNewRSSHubClientFromEnvTimeout(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "unset", raw: "", want: 60 * time.Second},
		{name: "override", raw: "45s", want: 45 * time.Second},
		{name: "invalid", raw: "invalid", want: 60 * time.Second},
		{name: "zero", raw: "0s", want: 60 * time.Second},
		{name: "negative", raw: "-5s", want: 60 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RSSHUB_TIMEOUT", test.raw)
			client, err := NewRSSHubClientFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			if client.httpClient.Timeout != test.want {
				t.Fatalf("timeout=%s want %s", client.httpClient.Timeout, test.want)
			}
		})
	}
}

func TestRSSHubClientReportsHTTPAndMalformedXML(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantError  string
	}{
		{name: "http error", statusCode: http.StatusBadGateway, body: "upstream unavailable", wantError: "HTTP 502"},
		{name: "malformed xml", statusCode: http.StatusOK, body: "<rss><channel>", wantError: "decode RSSHub response"},
		{name: "empty feed", statusCode: http.StatusOK, body: `<rss version="2.0"><channel><title>Twitter @MKBHD</title></channel></rss>`, wantError: "RSSHub feed returned no items"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(test.statusCode)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()

			client, err := NewRSSHubClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.LookupUsers(context.Background(), []string{"MKBHD"})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v want substring %q", err, test.wantError)
			}
		})
	}
}

func TestRSSHubClientInvalidStatusIDFailsEligibility(t *testing.T) {
	feed := strings.Replace(rssHubTestFeed, "status/1001", "status/not-a-number", 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/rss+xml")
		_, _ = io.WriteString(writer, feed)
	}))
	defer server.Close()

	client, err := NewRSSHubClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	users, err := client.LookupUsers(context.Background(), []string{"MKBHD"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.GetUserPosts(context.Background(), users["mkbhd"].ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.Posts[0].ID != "" {
		t.Fatalf("invalid status ID=%q", page.Posts[0].ID)
	}
	if ok, reason := EligibleSourcePost(page.Posts[0], users["mkbhd"].ID); ok || reason != "missing_id" {
		t.Fatalf("invalid ID eligibility=%t reason=%q", ok, reason)
	}
}

var _ SnapshotSourceClient = (*RSSHubClient)(nil)
