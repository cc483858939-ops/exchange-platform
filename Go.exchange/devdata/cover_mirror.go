package devdata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"Go.exchange/profilecover"
	"Go.exchange/profilecoverimage"
)

const (
	coverSourceHost     = "pbs.twimg.com"
	coverMaxRedirects   = 3
	coverRequestTimeout = 10 * time.Second
)

// CoverObjectStore intentionally reuses the generic Stat/Put contract already
// used by DevData avatar mirroring. The alias keeps MinIO and its tests shared
// without coupling cover processing to avatar image metadata.
type CoverObjectStore = AvatarObjectStore

// CoverResolution is the durable result of localizing one source banner.
type CoverResolution struct {
	RegistryKey string
	SourceURL   string
	ObjectKey   string
	LocalURL    string
	ContentHash string
}

// CoverMirrorReport counts best-effort cover preparation. Explicit source
// removal is a valid clear operation and is therefore not a failure.
type CoverMirrorReport struct {
	Attempted int
	Uploaded  int
	Reused    int
	Cleared   int
	Failed    int
}

// DownloadedCover contains the bounded, credential-free response body. Cover
// validation, optimization, and hashing remain owned by profilecoverimage.
type DownloadedCover struct {
	Body []byte
}

// CoverFetcher is intentionally narrow so mirror preparation can be tested
// with deterministic local fixtures and failure injectors.
type CoverFetcher interface {
	Download(ctx context.Context, sourceURL string) (DownloadedCover, error)
}

// CoverDownloader fetches only an explicitly allowlisted HTTPS banner host.
// It never reads or attaches application credentials.
type CoverDownloader struct {
	client      *http.Client
	allowedHost string
}

func NewCoverDownloader() *CoverDownloader {
	return &CoverDownloader{
		client:      &http.Client{Transport: http.DefaultTransport, Timeout: coverRequestTimeout},
		allowedHost: coverSourceHost,
	}
}

func (d *CoverDownloader) Download(ctx context.Context, sourceURL string) (DownloadedCover, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil {
		return DownloadedCover{}, errors.New("cover downloader is not initialized")
	}
	allowedHost := strings.TrimSpace(d.allowedHost)
	if allowedHost == "" {
		allowedHost = coverSourceHost
	}
	parsed, err := parseCoverURL(sourceURL, allowedHost)
	if err != nil {
		return DownloadedCover{}, err
	}

	client := d.client
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport}
	}
	requestClient := *client
	requestClient.Jar = nil
	if requestClient.Timeout <= 0 || requestClient.Timeout > coverRequestTimeout {
		requestClient.Timeout = coverRequestTimeout
	}
	requestClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > coverMaxRedirects {
			return errors.New("cover source redirects exceed the maximum")
		}
		if _, err := parseCoverURL(req.URL.String(), allowedHost); err != nil {
			return errors.New("cover redirect target is not allowed")
		}
		return nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return DownloadedCover{}, errors.New("create cover request failed")
	}
	response, err := requestClient.Do(request)
	if err != nil {
		return DownloadedCover{}, errors.New("request cover source failed")
	}
	defer response.Body.Close()
	if response.Request != nil {
		if _, err := parseCoverURL(response.Request.URL.String(), allowedHost); err != nil {
			return DownloadedCover{}, errors.New("cover response target is not allowed")
		}
	}
	if response.StatusCode != http.StatusOK {
		return DownloadedCover{}, errors.New("cover source returned a non-success status")
	}
	maxBytes := int64(profilecoverimage.MaxSourceBytes)
	if response.ContentLength > maxBytes {
		return DownloadedCover{}, errors.New("cover source exceeds the size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return DownloadedCover{}, errors.New("read cover source failed")
	}
	if len(body) == 0 {
		return DownloadedCover{}, errors.New("cover source returned an empty body")
	}
	if int64(len(body)) > maxBytes {
		return DownloadedCover{}, errors.New("cover source exceeds the size limit")
	}
	return DownloadedCover{Body: body}, nil
}

func parseCoverURL(rawURL, allowedHost string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return nil, errors.New("cover source URL is invalid")
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Opaque != "" {
		return nil, errors.New("cover source URL must be HTTPS without userinfo")
	}
	allowedHost = strings.ToLower(strings.TrimSpace(allowedHost))
	if allowedHost == "" {
		allowedHost = coverSourceHost
	}
	allowedHostName := allowedHost
	allowedPort := ""
	if host, port, splitErr := net.SplitHostPort(allowedHost); splitErr == nil {
		allowedHostName = host
		allowedPort = port
	}
	parsedPort := parsed.Port()
	if !strings.EqualFold(parsed.Hostname(), allowedHostName) ||
		(allowedPort == "" && parsedPort != "" && parsedPort != "443") ||
		(allowedPort != "" && parsedPort != allowedPort) {
		return nil, errors.New("cover source host is not allowlisted")
	}
	return parsed, nil
}

// BuildCoverObjectKey returns the versioned, content-addressed DevData cover
// namespace. The registry component is sanitized into one path segment and
// the hash/extension are validated before construction.
func BuildCoverObjectKey(registryKey, contentHash, extension string) (string, error) {
	safeKey, err := sanitizeCoverRegistryKey(registryKey)
	if err != nil {
		return "", err
	}
	if !isLowerHexHash(contentHash) {
		return "", errors.New("cover content hash must be lowercase SHA-256")
	}
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != ".jpg" && extension != ".png" {
		return "", errors.New("cover extension is not supported")
	}
	return profilecover.DevDataV1ObjectPrefix + safeKey + "/" + contentHash + extension, nil
}

func sanitizeCoverRegistryKey(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("registry key contains unsafe cover path characters")
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", errors.New("registry key cannot produce a safe cover path")
	}
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return "", errors.New("registry key contains unsafe cover path characters")
	}
	return raw, nil
}

func coverLocalURL(objectKey string) string {
	return profilecover.FilesURLPrefix + objectKey
}

func coverResolutionUsable(source SnapshotAccount, resolution CoverResolution) bool {
	if !source.ProfileBannerPresent || strings.TrimSpace(source.ProfileBannerURL) == "" {
		return false
	}
	if resolution.RegistryKey != source.RegistryKey || strings.TrimSpace(resolution.SourceURL) != strings.TrimSpace(source.ProfileBannerURL) {
		return false
	}
	if !isLowerHexHash(resolution.ContentHash) || !profilecover.IsDevDataPublicObjectKey(resolution.ObjectKey) {
		return false
	}
	expectedKey, err := BuildCoverObjectKey(source.RegistryKey, resolution.ContentHash, extensionFromCoverObjectKey(resolution.ObjectKey))
	if err != nil || resolution.ObjectKey != expectedKey || resolution.LocalURL != coverLocalURL(expectedKey) {
		return false
	}
	return true
}

func extensionFromCoverObjectKey(objectKey string) string {
	for _, extension := range []string{".jpg", ".png"} {
		if strings.HasSuffix(objectKey, extension) {
			return extension
		}
	}
	return ""
}

// PrepareCoverMirrors downloads, optimizes, and content-addresses each
// enabled account independently. Missing metadata and explicit removal are
// valid source states; a failed account does not block other accounts.
func PrepareCoverMirrors(ctx context.Context, registry SourceRegistry, snapshot Snapshot, fetcher CoverFetcher, store CoverObjectStore) (map[string]CoverResolution, CoverMirrorReport, error) {
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return nil, CoverMirrorReport{}, err
	}
	return prepareCoverMirrorsForAccounts(ctx, registry, snapshot, registry.EnabledAccounts(), fetcher, store)
}

// PrepareCoverMirrorsForKeys is the scoped counterpart used by incremental
// refresh. It never downloads covers for non-selected accounts.
func PrepareCoverMirrorsForKeys(ctx context.Context, registry SourceRegistry, snapshot Snapshot, keys []string, fetcher CoverFetcher, store CoverObjectStore) (map[string]CoverResolution, CoverMirrorReport, error) {
	if err := ValidateRegistry(registry); err != nil {
		return nil, CoverMirrorReport{}, err
	}
	selected, err := sourceAccountsForKeys(registry, keys)
	if err != nil {
		return nil, CoverMirrorReport{}, err
	}
	return prepareCoverMirrorsForAccounts(ctx, registry, snapshot, selected, fetcher, store)
}

func prepareCoverMirrorsForAccounts(ctx context.Context, registry SourceRegistry, snapshot Snapshot, configuredAccounts []SourceAccount, fetcher CoverFetcher, store CoverObjectStore) (map[string]CoverResolution, CoverMirrorReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	report := CoverMirrorReport{}
	resolutions := make(map[string]CoverResolution, len(configuredAccounts))
	accountsByKey := make(map[string]SnapshotAccount, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		accountsByKey[account.RegistryKey] = account
	}
	for _, configured := range configuredAccounts {
		source, exists := accountsByKey[configured.Key]
		if !exists {
			return nil, CoverMirrorReport{}, fmt.Errorf("snapshot is missing cover account %q", configured.Key)
		}
		if !source.ProfileBannerPresent {
			continue
		}
		sourceURL := strings.TrimSpace(source.ProfileBannerURL)
		if sourceURL == "" {
			report.Cleared++
			continue
		}
		report.Attempted++
		if fetcher == nil || store == nil {
			report.Failed++
			continue
		}
		downloaded, err := fetcher.Download(ctx, sourceURL)
		if err != nil {
			report.Failed++
			continue
		}
		derivative, err := profilecoverimage.Optimize(downloaded.Body)
		if err != nil {
			report.Failed++
			continue
		}
		objectKey, err := BuildCoverObjectKey(configured.Key, derivative.ContentHash, derivative.Extension)
		if err != nil {
			report.Failed++
			continue
		}
		info, exists, err := store.Stat(ctx, objectKey)
		if err != nil {
			report.Failed++
			continue
		}
		if exists && info.Size > 0 && info.Size == int64(len(derivative.Body)) && info.ContentType == derivative.ContentType {
			report.Reused++
		} else {
			if err := store.Put(ctx, objectKey, derivative.Body, derivative.ContentType); err != nil {
				report.Failed++
				continue
			}
			report.Uploaded++
		}
		resolutions[configured.Key] = CoverResolution{
			RegistryKey: configured.Key,
			SourceURL:   sourceURL,
			ObjectKey:   objectKey,
			LocalURL:    coverLocalURL(objectKey),
			ContentHash: derivative.ContentHash,
		}
	}
	return resolutions, report, nil
}
