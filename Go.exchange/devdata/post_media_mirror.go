package devdata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	postMediaSourceHost                 = "pbs.twimg.com"
	postMediaObjectPrefix               = "post-media/devdata/"
	postMediaMaxBytes             int64 = 5 << 20
	postMediaMaxRedirects                = 3
	postMediaRequestTimeout              = 10 * time.Second
)

type SourceMedia struct {
	Type   string
	URL    string
	Width  int
	Height int
}

type DownloadedPostMedia struct {
	Body        []byte
	ContentType string
	Extension   string
	ContentHash string
}

type PostMediaFetcher interface {
	Download(ctx context.Context, sourceURL string) (DownloadedPostMedia, error)
}

type PostMediaDownloader struct {
	client      *http.Client
	allowedHost string
}

func NewPostMediaDownloader() *PostMediaDownloader {
	return &PostMediaDownloader{
		client:      &http.Client{Transport: http.DefaultTransport, Timeout: postMediaRequestTimeout},
		allowedHost: postMediaSourceHost,
	}
}

func (d *PostMediaDownloader) Download(ctx context.Context, sourceURL string) (DownloadedPostMedia, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil {
		return DownloadedPostMedia{}, errors.New("post media downloader is not initialized")
	}
	allowedHost := strings.TrimSpace(d.allowedHost)
	if allowedHost == "" {
		allowedHost = postMediaSourceHost
	}
	parsed, err := parsePostMediaSourceURL(sourceURL, allowedHost)
	if err != nil {
		return DownloadedPostMedia{}, err
	}
	client := d.client
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport}
	}
	requestClient := *client
	requestClient.Jar = nil
	if requestClient.Timeout <= 0 || requestClient.Timeout > postMediaRequestTimeout {
		requestClient.Timeout = postMediaRequestTimeout
	}
	requestClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= postMediaMaxRedirects {
			return errors.New("post media source redirects exceed the maximum")
		}
		if _, err := parsePostMediaSourceURL(req.URL.String(), allowedHost); err != nil {
			return errors.New("post media redirect target is not allowed")
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return DownloadedPostMedia{}, errors.New("create post media request failed")
	}
	request.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	response, err := requestClient.Do(request)
	if err != nil {
		return DownloadedPostMedia{}, errors.New("request post media source failed")
	}
	defer response.Body.Close()
	if response.Request != nil {
		if _, err := parsePostMediaSourceURL(response.Request.URL.String(), allowedHost); err != nil {
			return DownloadedPostMedia{}, errors.New("post media response target is not allowed")
		}
	}
	if response.StatusCode != http.StatusOK {
		return DownloadedPostMedia{}, errors.New("post media source returned a non-success status")
	}
	if response.ContentLength > postMediaMaxBytes {
		return DownloadedPostMedia{}, errors.New("post media source exceeds the size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, postMediaMaxBytes+1))
	if err != nil {
		return DownloadedPostMedia{}, errors.New("read post media source failed")
	}
	if len(body) == 0 {
		return DownloadedPostMedia{}, errors.New("post media source returned an empty body")
	}
	if int64(len(body)) > postMediaMaxBytes {
		return DownloadedPostMedia{}, errors.New("post media source exceeds the size limit")
	}
	contentType, extension, ok := detectMirrorImageType(body)
	if !ok {
		return DownloadedPostMedia{}, errors.New("post media source is not a valid JPEG, PNG, or WebP")
	}
	hash := sha256.Sum256(body)
	return DownloadedPostMedia{
		Body:        body,
		ContentType: contentType,
		Extension:   extension,
		ContentHash: hex.EncodeToString(hash[:]),
	}, nil
}

func parsePostMediaSourceURL(rawURL, allowedHost string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil || parsed.Host == "" {
		return nil, errors.New("post media source URL is invalid")
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Opaque != "" {
		return nil, errors.New("post media source URL must be HTTPS without userinfo")
	}
	allowedHost = strings.ToLower(strings.TrimSpace(allowedHost))
	if allowedHost == "" {
		allowedHost = postMediaSourceHost
	}
	allowedHostName := allowedHost
	allowedPort := ""
	if host, port, splitErr := net.SplitHostPort(allowedHost); splitErr == nil {
		allowedHostName = host
		allowedPort = port
	}
	if !strings.EqualFold(parsed.Hostname(), allowedHostName) || (allowedPort == "" && parsed.Port() != "") || (allowedPort != "" && parsed.Port() != allowedPort) {
		return nil, errors.New("post media source host is not allowlisted")
	}
	return parsed, nil
}

func BuildPostMediaObjectKey(registryKey, sourcePostID, contentHash, extension string) (string, error) {
	safeRegistryKey, err := sanitizePostMediaRegistryKey(registryKey)
	if err != nil {
		return "", err
	}
	if !isNumericSourceID(strings.TrimSpace(sourcePostID)) {
		return "", errors.New("source Post ID must be numeric")
	}
	if !isLowerHexHash(contentHash) {
		return "", errors.New("post media content hash must be lowercase SHA-256")
	}
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != ".jpg" && extension != ".png" && extension != ".webp" {
		return "", errors.New("post media extension is not supported")
	}
	return postMediaObjectPrefix + safeRegistryKey + "/" + strings.TrimSpace(sourcePostID) + "/" + contentHash + extension, nil
}

func sanitizePostMediaRegistryKey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "..") || strings.ContainsAny(raw, `/\\`) {
		return "", errors.New("registry key is unsafe for a post media object key")
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", errors.New("registry key contains control characters")
		}
	}
	safe := sanitizeAvatarRegistryKey(raw)
	if safe == "" {
		return "", errors.New("registry key cannot produce a safe post media path")
	}
	return safe, nil
}

func postMediaLocalURL(objectKey string) string {
	return avatarLocalURLPrefix + objectKey
}

type SourcePostKey struct {
	RegistryKey  string
	SourcePostID string
}

type PostMediaResolution struct {
	RegistryKey  string
	SourcePostID string
	Position     int
	SourceURL    string
	ObjectKey    string
	LocalURL     string
	ContentHash  string
}

type PostMediaMirrorReport struct {
	PostsWithMedia int
	Attempted      int
	Uploaded       int
	Reused         int
	Failed         int
}

func PreparePostMediaMirrors(ctx context.Context, registry SourceRegistry, snapshot Snapshot, downloader PostMediaFetcher, store AvatarObjectStore) (map[SourcePostKey][]PostMediaResolution, PostMediaMirrorReport, error) {
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return nil, PostMediaMirrorReport{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resolutions := make(map[SourcePostKey][]PostMediaResolution)
	report := PostMediaMirrorReport{}
	for _, post := range snapshot.Posts {
		if len(post.Media) == 0 {
			continue
		}
		report.PostsWithMedia++
		key := SourcePostKey{RegistryKey: post.RegistryKey, SourcePostID: post.SourcePostID}
		resolutions[key] = make([]PostMediaResolution, 0, len(post.Media))
		for position, media := range post.Media {
			report.Attempted++
			if downloader == nil || store == nil {
				report.Failed++
				continue
			}
			downloaded, err := downloader.Download(ctx, media.SourceURL)
			if err != nil || len(downloaded.Body) == 0 || int64(len(downloaded.Body)) > postMediaMaxBytes {
				report.Failed++
				continue
			}
			contentType, extension, ok := detectMirrorImageType(downloaded.Body)
			if !ok {
				report.Failed++
				continue
			}
			hash := sha256.Sum256(downloaded.Body)
			contentHash := hex.EncodeToString(hash[:])
			objectKey, err := BuildPostMediaObjectKey(post.RegistryKey, post.SourcePostID, contentHash, extension)
			if err != nil {
				report.Failed++
				continue
			}
			info, exists, err := store.Stat(ctx, objectKey)
			if err != nil {
				report.Failed++
				continue
			}
			if exists && info.Size == int64(len(downloaded.Body)) && info.ContentType == contentType {
				report.Reused++
			} else {
				if err := store.Put(ctx, objectKey, downloaded.Body, contentType); err != nil {
					report.Failed++
					continue
				}
				report.Uploaded++
			}
			resolutions[key] = append(resolutions[key], PostMediaResolution{
				RegistryKey: post.RegistryKey, SourcePostID: post.SourcePostID,
				Position: position, SourceURL: strings.TrimSpace(media.SourceURL),
				ObjectKey: objectKey, LocalURL: postMediaLocalURL(objectKey), ContentHash: contentHash,
			})
		}
	}
	return resolutions, report, nil
}

var _ PostMediaFetcher = (*PostMediaDownloader)(nil)
