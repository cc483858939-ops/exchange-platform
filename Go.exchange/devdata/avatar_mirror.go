package devdata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

const (
	avatarSourceHost           = "pbs.twimg.com"
	avatarObjectPrefix         = "profile-avatars/devdata/"
	avatarLocalURLPrefix       = "/api/files/"
	avatarMaxBytes       int64 = 2 << 20
	avatarMaxRedirects         = 3
	avatarRequestTimeout       = 10 * time.Second
)

// AvatarObjectInfo is the small object metadata contract needed by the
// content-addressed avatar mirror.
type AvatarObjectInfo struct {
	Size        int64
	ContentType string
}

// AvatarObjectStore keeps avatar synchronization independent from MinIO so
// all source and object-storage behavior can be tested with local fixtures.
type AvatarObjectStore interface {
	Stat(ctx context.Context, objectKey string) (AvatarObjectInfo, bool, error)
	Put(ctx context.Context, objectKey string, body []byte, contentType string) error
}

// AvatarResolution is the durable result of successfully resolving one source
// avatar to a local content-addressed object.
type AvatarResolution struct {
	RegistryKey string
	SourceURL   string
	ObjectKey   string
	LocalURL    string
	ContentHash string
}

// AvatarMirrorReport counts best-effort per-account avatar preparation.
type AvatarMirrorReport struct {
	Attempted int
	Uploaded  int
	Reused    int
	Failed    int
}

// DownloadedAvatar contains validated bytes and canonical metadata derived
// from the bytes, never from a remote Content-Type header or file extension.
type DownloadedAvatar struct {
	Body        []byte
	ContentType string
	Extension   string
	ContentHash string
}

// AvatarFetcher is intentionally narrower than the HTTP implementation so
// unit tests can use local deterministic fixtures and failure injectors.
type AvatarFetcher interface {
	Download(ctx context.Context, sourceURL string) (DownloadedAvatar, error)
}

// AvatarDownloader fetches only an explicitly allowlisted HTTPS avatar host.
// It never reads or attaches application credentials.
type AvatarDownloader struct {
	client      *http.Client
	allowedHost string
}

func NewAvatarDownloader() *AvatarDownloader {
	return &AvatarDownloader{
		client:      &http.Client{Transport: http.DefaultTransport, Timeout: avatarRequestTimeout},
		allowedHost: avatarSourceHost,
	}
}

func (d *AvatarDownloader) Download(ctx context.Context, sourceURL string) (DownloadedAvatar, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil {
		return DownloadedAvatar{}, errors.New("avatar downloader is not initialized")
	}
	parsed, err := parseAvatarURL(sourceURL, d.allowedHost)
	if err != nil {
		return DownloadedAvatar{}, err
	}

	client := d.client
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport}
	}
	requestClient := *client
	requestClient.Jar = nil
	if requestClient.Timeout <= 0 || requestClient.Timeout > avatarRequestTimeout {
		requestClient.Timeout = avatarRequestTimeout
	}
	allowedHost := d.allowedHost
	if strings.TrimSpace(allowedHost) == "" {
		allowedHost = avatarSourceHost
	}
	requestClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= avatarMaxRedirects {
			return errors.New("avatar source redirects exceed the maximum")
		}
		if _, err := parseAvatarURL(req.URL.String(), allowedHost); err != nil {
			return errors.New("avatar redirect target is not allowed")
		}
		return nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return DownloadedAvatar{}, errors.New("create avatar request failed")
	}
	response, err := requestClient.Do(request)
	if err != nil {
		return DownloadedAvatar{}, errors.New("request avatar source failed")
	}
	defer response.Body.Close()
	if response.Request != nil {
		if _, err := parseAvatarURL(response.Request.URL.String(), allowedHost); err != nil {
			return DownloadedAvatar{}, errors.New("avatar response target is not allowed")
		}
	}
	if response.StatusCode != http.StatusOK {
		return DownloadedAvatar{}, errors.New("avatar source returned a non-success status")
	}
	if response.ContentLength > avatarMaxBytes {
		return DownloadedAvatar{}, errors.New("avatar source exceeds the size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, avatarMaxBytes+1))
	if err != nil {
		return DownloadedAvatar{}, errors.New("read avatar source failed")
	}
	if len(body) == 0 {
		return DownloadedAvatar{}, errors.New("avatar source returned an empty body")
	}
	if int64(len(body)) > avatarMaxBytes {
		return DownloadedAvatar{}, errors.New("avatar source exceeds the size limit")
	}
	contentType, extension, ok := detectAvatarImageType(body)
	if !ok {
		return DownloadedAvatar{}, errors.New("avatar source is not a valid JPEG, PNG, or WebP")
	}
	hash := sha256.Sum256(body)
	return DownloadedAvatar{
		Body:        body,
		ContentType: contentType,
		Extension:   extension,
		ContentHash: hex.EncodeToString(hash[:]),
	}, nil
}

func parseAvatarURL(rawURL, allowedHost string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return nil, errors.New("avatar source URL is invalid")
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Opaque != "" {
		return nil, errors.New("avatar source URL must be HTTPS without userinfo")
	}
	allowedHost = strings.ToLower(strings.TrimSpace(allowedHost))
	if allowedHost == "" {
		allowedHost = avatarSourceHost
	}
	allowedHostName := allowedHost
	allowedPort := ""
	if host, port, splitErr := net.SplitHostPort(allowedHost); splitErr == nil {
		allowedHostName = host
		allowedPort = port
	}
	if !strings.EqualFold(parsed.Hostname(), allowedHostName) || (allowedPort == "" && parsed.Port() != "") || (allowedPort != "" && parsed.Port() != allowedPort) {
		return nil, errors.New("avatar source host is not allowlisted")
	}
	return parsed, nil
}

func detectAvatarImageType(body []byte) (string, string, bool) {
	if len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff {
		if _, err := jpeg.DecodeConfig(bytes.NewReader(body)); err == nil {
			return "image/jpeg", ".jpg", true
		}
	}
	if len(body) >= 8 && bytes.Equal(body[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		if _, err := png.DecodeConfig(bytes.NewReader(body)); err == nil {
			return "image/png", ".png", true
		}
	}
	if isValidWebP(body) {
		return "image/webp", ".webp", true
	}
	return "", "", false
}

func isValidWebP(body []byte) bool {
	if len(body) < 20 || !bytes.Equal(body[:4], []byte("RIFF")) || !bytes.Equal(body[8:12], []byte("WEBP")) {
		return false
	}
	riffSize := uint64(binary.LittleEndian.Uint32(body[4:8]))
	if riffSize+8 != uint64(len(body)) {
		return false
	}
	foundImageChunk := false
	for offset := 12; offset < len(body); {
		if len(body)-offset < 8 {
			return false
		}
		chunkType := string(body[offset : offset+4])
		chunkSize := uint64(binary.LittleEndian.Uint32(body[offset+4 : offset+8]))
		chunkEnd := uint64(offset) + 8 + chunkSize
		if chunkEnd > uint64(len(body)) {
			return false
		}
		payload := body[offset+8 : int(chunkEnd)]
		switch chunkType {
		case "VP8 ":
			if len(payload) < 6 || !bytes.Equal(payload[3:6], []byte{0x9d, 0x01, 0x2a}) {
				return false
			}
			foundImageChunk = true
		case "VP8L":
			if len(payload) < 5 || payload[0] != 0x2f {
				return false
			}
			foundImageChunk = true
		case "VP8X":
			if len(payload) < 10 {
				return false
			}
			foundImageChunk = true
		}
		next := int(chunkEnd)
		if chunkSize%2 == 1 {
			next++
		}
		if next > len(body) {
			return false
		}
		offset = next
	}
	return foundImageChunk
}

// BuildAvatarObjectKey returns the frozen DevData avatar namespace. The
// registry component is sanitized into one path segment and the hash and
// extension are validated before they are placed in the key.
func BuildAvatarObjectKey(registryKey, contentHash, extension string) (string, error) {
	safeKey := sanitizeAvatarRegistryKey(registryKey)
	if safeKey == "" {
		return "", errors.New("registry key cannot produce a safe avatar path")
	}
	if !isLowerHexHash(contentHash) {
		return "", errors.New("avatar content hash must be lowercase SHA-256")
	}
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != ".jpg" && extension != ".png" && extension != ".webp" {
		return "", errors.New("avatar extension is not supported")
	}
	return avatarObjectPrefix + safeKey + "/" + contentHash + extension, nil
}

func sanitizeAvatarRegistryKey(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var builder strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteByte('-')
		default:
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-_")
}

func avatarLocalURL(objectKey string) string { return avatarLocalURLPrefix + objectKey }

func isLowerHexHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func avatarContentTypeForExtension(extension string) string {
	switch strings.ToLower(extension) {
	case ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func avatarResolutionUsable(source SnapshotAccount, resolution AvatarResolution) bool {
	if resolution.RegistryKey != source.RegistryKey {
		return false
	}
	if strings.TrimSpace(resolution.SourceURL) != "" && strings.TrimSpace(resolution.SourceURL) != strings.TrimSpace(source.ProfileImageURL) {
		return false
	}
	if !isLowerHexHash(resolution.ContentHash) {
		return false
	}
	objectKey, err := BuildAvatarObjectKey(source.RegistryKey, resolution.ContentHash, extensionFromAvatarObjectKey(resolution.ObjectKey))
	if err != nil || resolution.ObjectKey != objectKey || resolution.LocalURL != avatarLocalURL(objectKey) {
		return false
	}
	return true
}

func extensionFromAvatarObjectKey(objectKey string) string {
	for _, extension := range []string{".jpg", ".png", ".webp"} {
		if strings.HasSuffix(strings.ToLower(objectKey), extension) {
			return extension
		}
	}
	return ""
}

// PrepareAvatarMirrors downloads, validates, and content-addresses each
// enabled account independently. A failed account does not prevent other
// accounts or the later Post sync from proceeding.
func PrepareAvatarMirrors(ctx context.Context, registry SourceRegistry, snapshot Snapshot, downloader AvatarFetcher, store AvatarObjectStore) (map[string]AvatarResolution, AvatarMirrorReport, error) {
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return nil, AvatarMirrorReport{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	accounts := registry.EnabledAccounts()
	report := AvatarMirrorReport{Attempted: len(accounts)}
	resolutions := make(map[string]AvatarResolution, len(accounts))
	accountsByKey := make(map[string]SnapshotAccount, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		accountsByKey[account.RegistryKey] = account
	}
	for _, configured := range accounts {
		source := accountsByKey[configured.Key]
		if downloader == nil || store == nil || strings.TrimSpace(source.ProfileImageURL) == "" {
			report.Failed++
			continue
		}
		downloaded, err := downloader.Download(ctx, source.ProfileImageURL)
		if err != nil {
			report.Failed++
			continue
		}
		objectKey, err := BuildAvatarObjectKey(configured.Key, downloaded.ContentHash, downloaded.Extension)
		if err != nil {
			report.Failed++
			continue
		}
		info, exists, err := store.Stat(ctx, objectKey)
		if err != nil {
			report.Failed++
			continue
		}
		if exists && info.Size == int64(len(downloaded.Body)) && info.ContentType == downloaded.ContentType {
			report.Reused++
		} else {
			if err := store.Put(ctx, objectKey, downloaded.Body, downloaded.ContentType); err != nil {
				report.Failed++
				continue
			}
			report.Uploaded++
		}
		resolutions[configured.Key] = AvatarResolution{
			RegistryKey: configured.Key,
			SourceURL:   strings.TrimSpace(source.ProfileImageURL),
			ObjectKey:   objectKey,
			LocalURL:    avatarLocalURL(objectKey),
			ContentHash: downloaded.ContentHash,
		}
	}
	return resolutions, report, nil
}

type minioAvatarObjectStore struct {
	client *minio.Client
	bucket string
}

// NewMinioAvatarObjectStore adapts a configured MinIO client to the DevData
// avatar store contract. Bucket creation/checking belongs to config.NewStorageClient.
func NewMinioAvatarObjectStore(client *minio.Client) (AvatarObjectStore, error) {
	if client == nil {
		return nil, errors.New("MinIO client is not initialized")
	}
	return &minioAvatarObjectStore{client: client, bucket: config.StorageBucket()}, nil
}

func (s *minioAvatarObjectStore) Stat(ctx context.Context, objectKey string) (AvatarObjectInfo, bool, error) {
	if s == nil || s.client == nil {
		return AvatarObjectInfo{}, false, errors.New("avatar object store is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	info, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		if isMissingAvatarObject(err) {
			return AvatarObjectInfo{}, false, nil
		}
		return AvatarObjectInfo{}, false, errors.New("stat avatar object failed")
	}
	return AvatarObjectInfo{Size: info.Size, ContentType: info.ContentType}, true, nil
}

func (s *minioAvatarObjectStore) Put(ctx context.Context, objectKey string, body []byte, contentType string) error {
	if s == nil || s.client == nil {
		return errors.New("avatar object store is not initialized")
	}
	if len(body) == 0 || int64(len(body)) > avatarMaxBytes {
		return errors.New("avatar object body has an invalid size")
	}
	if avatarContentTypeForExtension(extensionFromAvatarObjectKey(objectKey)) != contentType {
		return errors.New("avatar object content type is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return errors.New("put avatar object failed")
	}
	return nil
}

func isMissingAvatarObject(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.StatusCode == http.StatusNotFound || response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound"
}

type AvatarVerificationReport struct {
	Enabled        int
	LocalURLs      int
	ObjectsPresent int
	Invalid        int
}

// VerifyAvatars performs a read-only local metadata/object audit. It does not
// contact X or RSSHub and does not mutate the database or object store.
func VerifyAvatars(ctx context.Context, db *gorm.DB, registry SourceRegistry, store AvatarObjectStore) (AvatarVerificationReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return AvatarVerificationReport{}, errors.New("database is not initialized")
	}
	if err := ValidateRegistry(registry); err != nil {
		return AvatarVerificationReport{}, err
	}
	if err := ValidateMetadataSchema(ctx, db); err != nil {
		return AvatarVerificationReport{}, err
	}
	report := AvatarVerificationReport{Enabled: len(registry.EnabledAccounts())}
	for _, configured := range registry.EnabledAccounts() {
		valid := true
		var account models.DevDataMirrorAccount
		if err := db.WithContext(ctx).Where("registry_key = ?", configured.Key).First(&account).Error; err != nil {
			valid = false
		}
		if !valid || !account.Enabled || account.LocalUserID == 0 || strings.TrimSpace(account.SourceAvatarURL) == "" || strings.TrimSpace(account.AvatarObjectKey) == "" || !isLowerHexHash(account.AvatarContentHash) {
			valid = false
		}

		var user models.User
		if account.LocalUserID != 0 {
			if err := db.WithContext(ctx).Unscoped().First(&user, account.LocalUserID).Error; err != nil || user.DeletedAt.Valid {
				valid = false
			}
		}

		expectedKey := ""
		if account.AvatarObjectKey != "" {
			expectedKey, _ = BuildAvatarObjectKey(configured.Key, account.AvatarContentHash, extensionFromAvatarObjectKey(account.AvatarObjectKey))
		}
		if expectedKey == "" || account.AvatarObjectKey != expectedKey {
			valid = false
		}
		if account.LocalUserID != 0 && user.AvatarURL == avatarLocalURL(account.AvatarObjectKey) {
			report.LocalURLs++
		} else {
			valid = false
		}

		if store == nil || account.AvatarObjectKey == "" {
			valid = false
		} else {
			info, exists, err := store.Stat(ctx, account.AvatarObjectKey)
			if exists {
				report.ObjectsPresent++
			}
			if err != nil || !exists || info.Size <= 0 || info.Size > avatarMaxBytes || avatarContentTypeForExtension(extensionFromAvatarObjectKey(account.AvatarObjectKey)) != info.ContentType {
				valid = false
			}
		}
		if !valid {
			report.Invalid++
		}
	}
	if report.Invalid > 0 {
		return report, fmt.Errorf("avatar verification failed: invalid=%d", report.Invalid)
	}
	return report, nil
}
