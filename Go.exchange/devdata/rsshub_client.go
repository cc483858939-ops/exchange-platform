package devdata

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRSSHubBaseURL        = "http://127.0.0.1:1200"
	defaultRSSHubRequestTimeout = 20 * time.Second
	maxRSSHubResponseBytes      = 16 << 20
	rssHubSourceUserPrefix      = "rsshub:"
	rssHubUserRouteParams       = "/count=60&includeReplies=false&includeRts=false&strict=true"
)

var (
	rssHubStatusIDPattern  = regexp.MustCompile(`(?i)/status/([0-9]{1,19})(?:[/?#\s"'&<>]|$)`)
	rssHubHTMLBreakPattern = regexp.MustCompile(`(?is)<\s*(?:br|/p|/div|/li|/blockquote)\s*/?\s*>`)
	rssHubHTMLTagPattern   = regexp.MustCompile(`(?is)<[^>]+>`)
	rssHubQuotePattern     = regexp.MustCompile(`(?is)<div\b[^>]*\bclass\s*=\s*["'][^"']*\brsshub-quote\b[^"']*["'][^>]*>`)
)

// RSSHubClient adapts RSSHub's X user feeds to the existing source client
// contract. RSSHub returns a bounded recent feed, so it intentionally has no
// pagination token of its own.
type RSSHubClient struct {
	baseURL    *url.URL
	httpClient *http.Client

	mu       sync.Mutex
	feeds    map[string]rssHubFeed
	requests int
}

type rssHubFeed struct {
	user  XUser
	posts []XPost
}

type rssHubDocument struct {
	Channel rssHubChannel `xml:"channel"`
}

type rssHubChannel struct {
	Title       string       `xml:"title"`
	Description string       `xml:"description"`
	Link        string       `xml:"link"`
	Image       rssHubImage  `xml:"image"`
	Items       []rssHubItem `xml:"item"`
}

type rssHubImage struct {
	URL string `xml:"url"`
}

type rssHubItem struct {
	Title              string             `xml:"title"`
	Description        string             `xml:"description"`
	EncodedDescription string             `xml:"encoded"`
	Link               string             `xml:"link"`
	GUID               string             `xml:"guid"`
	PubDate            string             `xml:"pubDate"`
	Author             string             `xml:"author"`
	Creator            string             `xml:"creator"`
	Enclosure          *rssHubEnclosure   `xml:"enclosure"`
	MediaContent       []rssHubMediaEntry `xml:"content"`
}

type rssHubEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

type rssHubMediaEntry struct {
	URL string `xml:"url,attr"`
}

func NewRSSHubClient(baseURL string, httpClient *http.Client) (*RSSHubClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultRSSHubBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid RSSHub base URL")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultRSSHubRequestTimeout}
	}
	return &RSSHubClient{baseURL: parsed, httpClient: httpClient, feeds: make(map[string]rssHubFeed)}, nil
}

func NewRSSHubClientFromEnv() (*RSSHubClient, error) {
	baseURL := strings.TrimSpace(os.Getenv("RSSHUB_BASE_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("RSSHUB_URL"))
	}
	timeout := defaultRSSHubRequestTimeout
	if raw := strings.TrimSpace(os.Getenv("RSSHUB_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	return NewRSSHubClient(baseURL, &http.Client{Timeout: timeout})
}

func (c *RSSHubClient) RequestCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func (c *RSSHubClient) LookupUsers(ctx context.Context, handles []string) (map[string]XUser, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return nil, errors.New("RSSHub client is not initialized")
	}
	if len(handles) == 0 {
		return map[string]XUser{}, errors.New("RSSHub user lookup requires at least one handle")
	}
	users := make(map[string]XUser, len(handles))
	var firstErr error
	for _, rawHandle := range handles {
		handle := strings.TrimSpace(rawHandle)
		if !xHandlePattern.MatchString(handle) {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid X handle %q", handle)
			}
			continue
		}
		feed, err := c.fetchFeed(ctx, handle, true)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("fetch RSSHub feed for %q: %w", handle, err)
			}
			continue
		}
		users[strings.ToLower(handle)] = feed.user
	}
	return users, firstErr
}

func (c *RSSHubClient) GetUserPosts(ctx context.Context, sourceUserID, paginationToken string, maxResults int) (XTimelinePage, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return XTimelinePage{}, errors.New("RSSHub client is not initialized")
	}
	if strings.TrimSpace(paginationToken) != "" {
		return XTimelinePage{}, errors.New("RSSHub source does not support pagination")
	}
	handle, ok := rssHubHandleFromSourceUserID(sourceUserID)
	if !ok {
		return XTimelinePage{}, errors.New("invalid RSSHub source user ID")
	}
	feed, err := c.fetchFeed(ctx, handle, false)
	if err != nil {
		return XTimelinePage{}, fmt.Errorf("fetch RSSHub feed for %q: %w", handle, err)
	}
	posts := append([]XPost(nil), feed.posts...)
	if maxResults > 0 && len(posts) > maxResults {
		posts = posts[:maxResults]
	}
	return XTimelinePage{Posts: posts, ResultCount: len(posts)}, nil
}

func (c *RSSHubClient) fetchFeed(ctx context.Context, handle string, forceRefresh bool) (rssHubFeed, error) {
	key := strings.ToLower(strings.TrimSpace(handle))
	if !forceRefresh {
		c.mu.Lock()
		feed, ok := c.feeds[key]
		c.mu.Unlock()
		if ok {
			return feed, nil
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/twitter/user/" + url.PathEscape(handle) + rssHubUserRouteParams
	endpoint.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return rssHubFeed{}, fmt.Errorf("build RSSHub request: %w", err)
	}
	request.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	request.Header.Set("User-Agent", "NexusFeed-DevData/1")
	c.incrementRequests()
	response, err := c.httpClient.Do(request)
	if err != nil {
		return rssHubFeed{}, fmt.Errorf("RSSHub request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return rssHubFeed{}, fmt.Errorf("RSSHub request failed with HTTP %d", response.StatusCode)
	}
	var document rssHubDocument
	decoder := xml.NewDecoder(io.LimitReader(response.Body, maxRSSHubResponseBytes))
	if err := decoder.Decode(&document); err != nil {
		return rssHubFeed{}, fmt.Errorf("decode RSSHub response: %w", err)
	}
	if len(document.Channel.Items) == 0 {
		return rssHubFeed{}, errors.New("RSSHub feed returned no items")
	}
	feed, err := parseRSSHubFeed(handle, document)
	if err != nil {
		return rssHubFeed{}, err
	}
	c.mu.Lock()
	c.feeds[key] = feed
	c.mu.Unlock()
	return feed, nil
}

func (c *RSSHubClient) incrementRequests() {
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
}

func parseRSSHubFeed(handle string, document rssHubDocument) (rssHubFeed, error) {
	handle = strings.TrimSpace(handle)
	if !xHandlePattern.MatchString(handle) {
		return rssHubFeed{}, fmt.Errorf("invalid X handle %q", handle)
	}
	name := rssHubDisplayName(document.Channel.Title, handle)
	protected := false
	feed := rssHubFeed{user: XUser{
		ID:              rssHubSourceUserID(handle),
		Name:            name,
		Username:        handle,
		Description:     rssHubContentText(document.Channel.Description),
		ProfileImageURL: strings.TrimSpace(document.Channel.Image.URL),
		Protected:       &protected,
	}, posts: make([]XPost, 0, len(document.Channel.Items))}
	for _, item := range document.Channel.Items {
		feed.posts = append(feed.posts, parseRSSHubItem(handle, item))
	}
	return feed, nil
}

func parseRSSHubItem(handle string, item rssHubItem) XPost {
	text := rssHubItemText(item)
	post := XPost{
		ID:        rssHubPostID(item),
		AuthorID:  rssHubSourceUserID(handle),
		Text:      text,
		CreatedAt: rssHubDate(item.PubDate),
		Attachments: XAttachments{
			MediaKeys: nil,
		},
	}
	if item.Enclosure != nil || len(item.MediaContent) > 0 || rssHubContainsMedia(item.Description) || rssHubContainsMedia(item.EncodedDescription) {
		post.Attachments.MediaKeys = []string{"rsshub-media"}
	}
	if rssHubContainsQuote(item) {
		post.ReferencedTweets = []XReferencedTweet{{Type: "quote", ID: rssHubQuoteReferenceID(item, post.ID)}}
	}
	return post
}

func rssHubItemText(item rssHubItem) string {
	if title := rssHubContentText(item.Title); title != "" {
		return title
	}
	if encoded := rssHubContentText(item.EncodedDescription); encoded != "" {
		return encoded
	}
	return rssHubContentText(item.Description)
}

func rssHubDisplayName(raw, handle string) string {
	name := rssHubContentText(raw)
	const prefix = "twitter @"
	if len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) {
		name = strings.TrimSpace(name[len(prefix):])
	}
	if name == "" {
		return handle
	}
	return name
}

func rssHubContainsQuote(item rssHubItem) bool {
	for _, raw := range []string{item.Description, item.EncodedDescription} {
		if rssHubQuotePattern.MatchString(raw) || rssHubQuotePattern.MatchString(html.UnescapeString(raw)) {
			return true
		}
	}
	return false
}

func rssHubQuoteReferenceID(item rssHubItem, rootID string) string {
	for _, raw := range []string{item.Description, item.EncodedDescription} {
		for _, match := range rssHubStatusIDPattern.FindAllStringSubmatch(html.UnescapeString(raw), -1) {
			if len(match) == 2 && match[1] != rootID {
				return match[1]
			}
		}
	}
	return ""
}

func rssHubPostID(item rssHubItem) string {
	for _, candidate := range []string{item.GUID, item.Link} {
		candidate = strings.TrimSpace(candidate)
		if match := rssHubStatusIDPattern.FindStringSubmatch(candidate); len(match) == 2 {
			return match[1]
		}
		if isNumericSourceID(candidate) {
			return candidate
		}
	}
	return ""
}

func rssHubDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 02 Jan 2006 15:04:05 MST",
	} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func rssHubContentText(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = rssHubHTMLBreakPattern.ReplaceAllString(raw, "\n")
	raw = rssHubHTMLTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	raw = strings.ReplaceAll(raw, "\u00a0", " ")
	return NormalizeSourceText(raw)
}

func rssHubContainsMedia(raw string) bool {
	raw = strings.ToLower(html.UnescapeString(raw))
	for _, marker := range []string{"<img", "<video", "<audio", "<source", "<iframe", "media:content"} {
		if strings.Contains(raw, marker) {
			return true
		}
	}
	return false
}

func rssHubSourceUserID(handle string) string {
	return rssHubSourceUserPrefix + strings.ToLower(strings.TrimSpace(handle))
}

func isRSSHubSourceUserID(value string) bool {
	_, ok := rssHubHandleFromSourceUserID(value)
	return ok
}

func isValidSourceUserID(value string) bool {
	return isNumericSourceID(value) || isRSSHubSourceUserID(value)
}

func rssHubHandleFromSourceUserID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) <= len(rssHubSourceUserPrefix) || !strings.EqualFold(value[:len(rssHubSourceUserPrefix)], rssHubSourceUserPrefix) {
		return "", false
	}
	handle := value[len(rssHubSourceUserPrefix):]
	if !xHandlePattern.MatchString(handle) {
		return "", false
	}
	return handle, true
}
