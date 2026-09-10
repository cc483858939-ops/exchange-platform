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

	"Go.exchange/postmedia"
	"Go.exchange/postmediaimage"
)

const (
	postMediaSourceHost           = "pbs.twimg.com"
	postMediaObjectPrefix         = postmedia.DevDataV1ObjectPrefix
	postMediaMaxBytes       int64 = postmediaimage.MaxSourceBytes
	postMediaMaxRedirects         = 3
	postMediaRequestTimeout       = 10 * time.Second
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

// BuildPostMediaObjectKey is kept as a small compatibility helper for local
// DevData callers. It now returns the V1 Medium object key; new code should
// use postmedia.BuildDevDataV1ObjectPaths when it also needs Original/Large.
func BuildPostMediaObjectKey(registryKey, sourcePostID, contentHash, extension string) (string, error) {
	derivativeExtension := strings.ToLower(strings.TrimSpace(extension))
	if derivativeExtension == ".webp" {
		derivativeExtension = ".jpg"
	}
	paths, err := postmedia.BuildDevDataV1ObjectPaths(registryKey, sourcePostID, contentHash, extension, derivativeExtension)
	if err != nil {
		return "", err
	}
	return paths.MediumObjectKey, nil
}

func postMediaLocalURL(objectKey string) string {
	return postmedia.PublicURL(objectKey)
}

type SourcePostKey struct {
	RegistryKey  string
	SourcePostID string
}

type PostMediaResolution struct {
	RegistryKey    string
	SourcePostID   string
	Position       int
	SourceURL      string
	ObjectKey      string
	LocalURL       string
	LargeObjectKey string
	LargeLocalURL  string
	Width          int
	Height         int
	ContentHash    string
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
			processed, err := postmediaimage.Process(downloaded.Body)
			if err != nil {
				report.Failed++
				continue
			}
			hash := sha256.Sum256(downloaded.Body)
			contentHash := hex.EncodeToString(hash[:])
			paths, err := postmedia.BuildDevDataV1ObjectPaths(post.RegistryKey, post.SourcePostID, contentHash, processed.OriginalExtension, processed.Medium.Extension)
			if err != nil {
				report.Failed++
				continue
			}
			objects := []struct {
				key         string
				body        []byte
				contentType string
			}{
				{key: paths.OriginalObjectKey, body: downloaded.Body, contentType: processed.OriginalContentType},
				{key: paths.MediumObjectKey, body: processed.Medium.Body, contentType: processed.Medium.ContentType},
				{key: paths.LargeObjectKey, body: processed.Large.Body, contentType: processed.Large.ContentType},
			}
			allReused := true
			objectFailed := false
			for _, object := range objects {
				info, exists, err := store.Stat(ctx, object.key)
				if err != nil {
					report.Failed++
					objectFailed = true
					allReused = false
					break
				}
				if exists && info.Size == int64(len(object.body)) && info.ContentType == object.contentType {
					continue
				}
				allReused = false
				if err := store.Put(ctx, object.key, object.body, object.contentType); err != nil {
					report.Failed++
					objectFailed = true
					break
				}
			}
			if objectFailed {
				continue
			}
			if !allReused {
				report.Uploaded++
			} else {
				report.Reused++
			}
			resolutions[key] = append(resolutions[key], PostMediaResolution{
				RegistryKey: post.RegistryKey, SourcePostID: post.SourcePostID,
				Position: position, SourceURL: strings.TrimSpace(media.SourceURL),
				ObjectKey: paths.MediumObjectKey, LocalURL: postMediaLocalURL(paths.MediumObjectKey),
				LargeObjectKey: paths.LargeObjectKey, LargeLocalURL: postMediaLocalURL(paths.LargeObjectKey),
				Width: processed.Medium.Width, Height: processed.Medium.Height, ContentHash: contentHash,
			})
		}
	}
	return resolutions, report, nil
}

var _ PostMediaFetcher = (*PostMediaDownloader)(nil)
