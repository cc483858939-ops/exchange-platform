package devdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultXRequestTimeout = 20 * time.Second
	maxXResponseBytes      = 16 << 20
)

var xUserFields = strings.Join([]string{
	"description", "name", "profile_image_url", "protected", "username",
}, ",")

var xTweetFields = strings.Join([]string{
	"article", "attachments", "author_id", "created_at", "in_reply_to_user_id", "lang",
	"note_tweet", "possibly_sensitive", "public_metrics", "referenced_tweets", "text",
}, ",")

// XClient is a small app-only X API v2 client. The bearer token is retained
// only in memory and is never included in errors, snapshots, or logs.
type XClient struct {
	baseURL     *url.URL
	bearerToken string
	httpClient  *http.Client
}

func NewXClient(baseURL, bearerToken string, httpClient *http.Client) (*XClient, error) {
	bearerToken = strings.TrimSpace(bearerToken)
	if bearerToken == "" {
		return nil, errors.New("X_BEARER_TOKEN unavailable")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultXAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "" {
		return nil, errors.New("invalid X API base URL")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultXRequestTimeout}
	}
	return &XClient{baseURL: parsed, bearerToken: bearerToken, httpClient: httpClient}, nil
}

func NewXClientFromEnv() (*XClient, error) {
	baseURL := strings.TrimSpace(os.Getenv("X_API_BASE_URL"))
	if baseURL == "" {
		baseURL = DefaultXAPIBaseURL
	}
	timeout := defaultXRequestTimeout
	if raw := strings.TrimSpace(os.Getenv("X_API_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	return NewXClient(baseURL, os.Getenv("X_BEARER_TOKEN"), &http.Client{Timeout: timeout})
}

type XAPIError struct {
	Title        string `json:"title"`
	Detail       string `json:"detail"`
	Type         string `json:"type"`
	ResourceType string `json:"resource_type"`
	Parameter    string `json:"parameter"`
	Value        string `json:"value"`
	Status       int    `json:"status"`
}

type XAPIResponseError struct {
	Status int
	Errors []XAPIError
}

func (e *XAPIResponseError) Error() string {
	if e == nil {
		return "X API request failed"
	}
	parts := make([]string, 0, len(e.Errors))
	for _, item := range e.Errors {
		detail := strings.TrimSpace(item.Detail)
		if detail == "" {
			detail = strings.TrimSpace(item.Title)
		}
		if detail != "" {
			parts = append(parts, detail)
		}
	}
	message := fmt.Sprintf("X API request failed with HTTP %d", e.Status)
	if len(parts) > 0 {
		message += ": " + strings.Join(parts, "; ")
	}
	return message
}

type XUser struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Username        string `json:"username"`
	Description     string `json:"description"`
	ProfileImageURL string `json:"profile_image_url"`
	Protected       *bool  `json:"protected"`
}

type XPublicMetrics struct {
	LikeCount    int64 `json:"like_count"`
	ReplyCount   int64 `json:"reply_count"`
	RetweetCount int64 `json:"retweet_count"`
	QuoteCount   int64 `json:"quote_count"`
}

type XReferencedTweet struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type XAttachments struct {
	MediaKeys []string `json:"media_keys"`
}

type XMedia struct {
	MediaKey string `json:"media_key"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type XNoteTweet struct {
	Text string `json:"text"`
}

// XArticle accommodates the current long-form field when an account/API
// response exposes it. Older v2 responses use note_tweet instead.
type XArticle struct {
	Text      string `json:"text"`
	PlainText string `json:"plain_text"`
}

type XPost struct {
	ID                string             `json:"id"`
	AuthorID          string             `json:"author_id"`
	Text              string             `json:"text"`
	CreatedAt         time.Time          `json:"created_at"`
	Lang              string             `json:"lang"`
	PossiblySensitive bool               `json:"possibly_sensitive"`
	InReplyToUserID   *string            `json:"in_reply_to_user_id"`
	Attachments       XAttachments       `json:"attachments"`
	Media             []SourceMedia      `json:"-"`
	ReferencedTweets  []XReferencedTweet `json:"referenced_tweets"`
	PublicMetrics     XPublicMetrics     `json:"public_metrics"`
	NoteTweet         *XNoteTweet        `json:"note_tweet"`
	Article           *XArticle          `json:"article"`
}

type XTimelinePage struct {
	Posts       []XPost
	NextToken   string
	ResultCount int
}

type xUserEnvelope struct {
	Data   []XUser     `json:"data"`
	Errors []XAPIError `json:"errors"`
}

type xTimelineEnvelope struct {
	Data []XPost `json:"data"`
	Includes struct {
		Media []XMedia `json:"media"`
	} `json:"includes"`
	Meta struct {
		NextToken   string `json:"next_token"`
		ResultCount int    `json:"result_count"`
	} `json:"meta"`
	Errors []XAPIError `json:"errors"`
}

func (c *XClient) LookupUsers(ctx context.Context, handles []string) (map[string]XUser, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return nil, errors.New("X API client is not initialized")
	}
	if len(handles) == 0 {
		return map[string]XUser{}, errors.New("X user lookup requires at least one handle")
	}
	query := url.Values{}
	query.Set("usernames", strings.Join(handles, ","))
	query.Set("user.fields", xUserFields)
	var envelope xUserEnvelope
	err := c.getJSON(ctx, "/2/users/by", query, &envelope)
	users := make(map[string]XUser, len(envelope.Data))
	for _, user := range envelope.Data {
		if strings.TrimSpace(user.Username) != "" {
			users[strings.ToLower(strings.TrimSpace(user.Username))] = user
		}
	}
	if err != nil {
		return users, err
	}
	if len(envelope.Errors) > 0 {
		return users, &XAPIResponseError{Status: http.StatusOK, Errors: envelope.Errors}
	}
	return users, nil
}

func (c *XClient) GetUserPosts(ctx context.Context, sourceUserID, paginationToken string, maxResults int) (XTimelinePage, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return XTimelinePage{}, errors.New("X API client is not initialized")
	}
	if !isNumericSourceID(sourceUserID) {
		return XTimelinePage{}, errors.New("invalid X source user ID")
	}
	if maxResults < 5 {
		maxResults = 5
	}
	if maxResults > 100 {
		maxResults = 100
	}
	query := url.Values{}
	query.Set("exclude", "replies,retweets")
	query.Set("max_results", strconv.Itoa(maxResults))
	query.Set("tweet.fields", xTweetFields)
	query.Set("expansions", "attachments.media_keys")
	query.Set("media.fields", "media_key,type,url,width,height")
	if strings.TrimSpace(paginationToken) != "" {
		query.Set("pagination_token", paginationToken)
	}
	var envelope xTimelineEnvelope
	err := c.getJSON(ctx, "/2/users/"+url.PathEscape(sourceUserID)+"/tweets", query, &envelope)
	if err != nil {
		return XTimelinePage{}, err
	}
	if len(envelope.Errors) > 0 {
		return XTimelinePage{}, &XAPIResponseError{Status: http.StatusOK, Errors: envelope.Errors}
	}
	normalizeXTimelineMedia(envelope.Data, envelope.Includes.Media)
	resultCount := envelope.Meta.ResultCount
	if resultCount == 0 && len(envelope.Data) > 0 {
		resultCount = len(envelope.Data)
	}
	return XTimelinePage{Posts: envelope.Data, NextToken: envelope.Meta.NextToken, ResultCount: resultCount}, nil
}

func normalizeXTimelineMedia(posts []XPost, includes []XMedia) {
	byKey := make(map[string]XMedia, len(includes))
	for _, media := range includes {
		key := strings.TrimSpace(media.MediaKey)
		if key != "" {
			byKey[key] = media
		}
	}
	for index := range posts {
		posts[index].Media = nil
		seenURLs := make(map[string]struct{}, len(posts[index].Attachments.MediaKeys))
		for _, key := range posts[index].Attachments.MediaKeys {
			media, ok := byKey[strings.TrimSpace(key)]
			if !ok || strings.TrimSpace(media.URL) == "" || !strings.EqualFold(strings.TrimSpace(media.Type), "photo") {
				continue
			}
			mediaURL := strings.TrimSpace(media.URL)
			if _, exists := seenURLs[mediaURL]; exists {
				continue
			}
			seenURLs[mediaURL] = struct{}{}
			posts[index].Media = append(posts[index].Media, SourceMedia{
				Type: "image", URL: mediaURL, Width: media.Width, Height: media.Height,
			})
			if len(posts[index].Media) == 4 {
				break
			}
		}
	}
}

func (c *XClient) getJSON(ctx context.Context, path string, query url.Values, target interface{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build X API request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	req.Header.Set("User-Agent", "NexusFeed-DevData/1")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("X API request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxXResponseBytes))
	if err != nil {
		return fmt.Errorf("read X API response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var envelope struct {
			Errors []XAPIError `json:"errors"`
		}
		_ = json.Unmarshal(body, &envelope)
		return &XAPIResponseError{Status: resp.StatusCode, Errors: envelope.Errors}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode X API response: %w", err)
	}
	return nil
}

func isNumericSourceID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 19 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
