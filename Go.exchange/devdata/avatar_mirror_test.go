package devdata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAvatarDownloaderAcceptsJPEGPNGAndWebPFromBytes(t *testing.T) {
	fixtures := map[string]struct {
		body        []byte
		contentType string
		extension   string
	}{
		"jpeg": {body: avatarJPEGFixture(t), contentType: "image/jpeg", extension: ".jpg"},
		"png":  {body: avatarPNGFixture(t), contentType: "image/png", extension: ".png"},
		"webp": {body: avatarWebPFixture(), contentType: "image/webp", extension: ".webp"},
	}

	server, downloader := newLocalAvatarDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		fixture, ok := fixtures[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(fixture.body)
	})
	defer server.Close()

	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			got, err := downloader.Download(context.Background(), server.URL+"/"+name)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if !bytes.Equal(got.Body, fixture.body) || got.ContentType != fixture.contentType || got.Extension != fixture.extension {
				t.Fatalf("downloaded=%#v", got)
			}
			hash := sha256.Sum256(fixture.body)
			if got.ContentHash != hexHash(hash) {
				t.Fatalf("unexpected content hash=%q", got.ContentHash)
			}
		})
	}
}

func TestAvatarDownloaderRejectsBadResponsesAndUnsafeURLs(t *testing.T) {
	server, downloader := newLocalAvatarDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/500":
			w.WriteHeader(http.StatusInternalServerError)
		case "/empty":
			w.WriteHeader(http.StatusOK)
		case "/html":
			_, _ = io.WriteString(w, "<html>not an avatar</html>")
		case "/unsupported":
			_, _ = w.Write([]byte("GIF89a"))
		case "/content-length-over":
			w.Header().Set("Content-Length", strconv.FormatInt(avatarMaxBytes+1, 10))
			w.WriteHeader(http.StatusOK)
		case "/streaming-over":
			flusher, _ := w.(http.Flusher)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, int(avatarMaxBytes)+1))
			if flusher != nil {
				flusher.Flush()
			}
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	for _, path := range []string{"404", "500", "empty", "html", "unsupported", "content-length-over", "streaming-over"} {
		t.Run(path, func(t *testing.T) {
			if _, err := downloader.Download(context.Background(), server.URL+"/"+path); err == nil {
				t.Fatal("Download unexpectedly succeeded")
			}
		})
	}

	defaultDownloader := NewAvatarDownloader()
	for name, rawURL := range map[string]string{
		"http":     "http://pbs.twimg.com/avatar.jpg",
		"userinfo": "https://user:password@pbs.twimg.com/avatar.jpg",
		"host":     "https://avatars.example.test/avatar.jpg",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := defaultDownloader.Download(context.Background(), rawURL); err == nil {
				t.Fatal("unsafe URL was accepted")
			}
		})
	}
}

func TestAvatarDownloaderRevalidatesRedirects(t *testing.T) {
	server, downloader := newLocalAvatarDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = w.Write(avatarPNGFixture(t))
		case "/too-many":
			http.Redirect(w, r, "/redirect-1", http.StatusFound)
		case "/redirect-1":
			http.Redirect(w, r, "/redirect-2", http.StatusFound)
		case "/redirect-2":
			http.Redirect(w, r, "/redirect-3", http.StatusFound)
		case "/redirect-3":
			http.Redirect(w, r, "/redirect-4", http.StatusFound)
		case "/redirect-4":
			_, _ = w.Write(avatarPNGFixture(t))
		case "/rejected-host":
			http.Redirect(w, r, "https://example.invalid/avatar.png", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	if _, err := downloader.Download(context.Background(), server.URL+"/start"); err != nil {
		t.Fatalf("valid redirect rejected: %v", err)
	}
	if _, err := downloader.Download(context.Background(), server.URL+"/too-many"); err == nil {
		t.Fatal("too many redirects were accepted")
	}
	if _, err := downloader.Download(context.Background(), server.URL+"/rejected-host"); err == nil {
		t.Fatal("rejected redirect host was accepted")
	}
}

func TestAvatarDownloaderDoesNotAttachCredentialHeaders(t *testing.T) {
	t.Setenv("X_BEARER_TOKEN", "unit-test-x-token")
	t.Setenv("TWITTER_AUTH_TOKEN", "unit-test-rsshub-token")
	seen := make(chan [2]string, 1)
	server, downloader := newLocalAvatarDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		seen <- [2]string{r.Header.Get("Authorization"), r.Header.Get("Cookie")}
		_, _ = w.Write(avatarJPEGFixture(t))
	})
	defer server.Close()

	if _, err := downloader.Download(context.Background(), server.URL+"/avatar"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	headers := <-seen
	if headers[0] != "" || headers[1] != "" {
		t.Fatalf("credential headers were attached: authorization=%q cookie=%q", headers[0], headers[1])
	}
}

func TestPrepareAvatarMirrorsContentAddressesAndContinuesPerAccount(t *testing.T) {
	registry, snapshot := avatarTestRegistrySnapshot()
	aBody := avatarJPEGFixture(t)
	bBody := avatarPNGFixture(t)
	cBody := avatarWebPFixture()
	fetcher := fakeAvatarFetcher{items: map[string]fakeAvatarFetch{
		snapshot.Accounts[0].ProfileImageURL: {avatar: downloadedAvatar(aBody)},
		snapshot.Accounts[1].ProfileImageURL: {err: errors.New("fixture failure")},
		snapshot.Accounts[2].ProfileImageURL: {avatar: downloadedAvatar(cBody)},
	}}
	store := newFakeAvatarStore()
	bDownloaded := downloadedAvatar(bBody)
	bKey, err := BuildAvatarObjectKey(registry.Accounts[1].Key, bDownloaded.ContentHash, bDownloaded.Extension)
	if err != nil {
		t.Fatal(err)
	}
	store.objects[bKey] = fakeAvatarObject{size: int64(len(bBody)), contentType: bDownloaded.ContentType}

	resolutions, report, err := PrepareAvatarMirrors(context.Background(), registry, snapshot, fetcher, store)
	if err != nil {
		t.Fatalf("PrepareAvatarMirrors: %v", err)
	}
	if report.Attempted != 3 || report.Uploaded != 2 || report.Reused != 0 || report.Failed != 1 {
		t.Fatalf("report=%#v", report)
	}
	if len(resolutions) != 2 || resolutions[registry.Accounts[0].Key].ObjectKey == resolutions[registry.Accounts[2].Key].ObjectKey {
		t.Fatalf("resolutions=%#v", resolutions)
	}
	if len(store.puts) != 2 {
		t.Fatalf("put count=%d", len(store.puts))
	}

	fetcher.items[snapshot.Accounts[1].ProfileImageURL] = fakeAvatarFetch{avatar: bDownloaded}
	resolutions, report, err = PrepareAvatarMirrors(context.Background(), registry, snapshot, fetcher, store)
	if err != nil {
		t.Fatalf("idempotent PrepareAvatarMirrors: %v", err)
	}
	if report.Attempted != 3 || report.Uploaded != 0 || report.Reused != 3 || report.Failed != 0 || len(resolutions) != 3 {
		t.Fatalf("idempotent report=%#v resolutions=%d", report, len(resolutions))
	}
	if len(store.puts) != 2 {
		t.Fatalf("idempotent put count=%d", len(store.puts))
	}
}

func TestBuildAvatarObjectKeySanitizesRegistryAndKeepsHashNamespace(t *testing.T) {
	hash := strings.Repeat("a", sha256.Size*2)
	key, err := BuildAvatarObjectKey("MKBHD", hash, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	if key != "profile-avatars/devdata/mkbhd/"+hash+".jpg" {
		t.Fatalf("key=%q", key)
	}
	for _, badHash := range []string{strings.Repeat("A", sha256.Size*2), "../" + strings.Repeat("a", 62)} {
		if _, err := BuildAvatarObjectKey("MKBHD", badHash, ".jpg"); err == nil {
			t.Fatalf("bad hash accepted: %q", badHash)
		}
	}
}

func newLocalAvatarDownloader(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *AvatarDownloader) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return server, &AvatarDownloader{client: server.Client(), allowedHost: parsed.Host}
}

func avatarJPEGFixture(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	imageData.Set(1, 1, color.RGBA{B: 255, A: 255})
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, imageData, nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func avatarPNGFixture(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.RGBA{G: 255, A: 255})
	imageData.Set(1, 1, color.RGBA{R: 255, B: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageData); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func avatarWebPFixture() []byte {
	chunkPayload := []byte{0x2f, 0, 0, 0, 0}
	body := make([]byte, 12+8+len(chunkPayload)+1)
	copy(body[0:4], "RIFF")
	binary.LittleEndian.PutUint32(body[4:8], uint32(len(body)-8))
	copy(body[8:12], "WEBP")
	copy(body[12:16], "VP8L")
	binary.LittleEndian.PutUint32(body[16:20], uint32(len(chunkPayload)))
	copy(body[20:], chunkPayload)
	return body
}

func downloadedAvatar(body []byte) DownloadedAvatar {
	contentType, extension, ok := detectAvatarImageType(body)
	if !ok {
		panic("test avatar fixture is invalid")
	}
	hash := sha256.Sum256(body)
	return DownloadedAvatar{Body: body, ContentType: contentType, Extension: extension, ContentHash: hexHash(hash)}
}

func hexHash(hash [sha256.Size]byte) string {
	const hexDigits = "0123456789abcdef"
	result := make([]byte, len(hash)*2)
	for index, value := range hash {
		result[index*2] = hexDigits[value>>4]
		result[index*2+1] = hexDigits[value&0x0f]
	}
	return string(result)
}

func avatarTestRegistrySnapshot() (SourceRegistry, Snapshot) {
	accounts := []SourceAccount{
		{Key: "MKBHD", Platform: "x", Handle: "MKBHD", Category: "fixture", MaxPosts: 3, Enabled: true},
		{Key: "F1", Platform: "x", Handle: "F1", Category: "fixture", MaxPosts: 3, Enabled: true},
		{Key: "Reuters", Platform: "x", Handle: "Reuters", Category: "fixture", MaxPosts: 3, Enabled: true},
	}
	registry := SourceRegistry{Version: SourceRegistryVersion, DefaultMaxPosts: 3, Accounts: accounts}
	snapshotAccounts := make([]SnapshotAccount, 0, len(accounts))
	for index, account := range accounts {
		snapshotAccounts = append(snapshotAccounts, SnapshotAccount{
			RegistryKey:     account.Key,
			SourceUserID:    strconv.Itoa(1000 + index),
			Handle:          account.Handle,
			Name:            account.Key,
			Description:     "fixture",
			ProfileImageURL: "https://pbs.twimg.com/profile_images/" + strings.ToLower(account.Key) + ".jpg",
			Category:        account.Category,
		})
	}
	return registry, Snapshot{Version: DefaultSnapshotVersion, FetchedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Accounts: snapshotAccounts}
}

type fakeAvatarFetch struct {
	avatar DownloadedAvatar
	err    error
}

type fakeAvatarFetcher struct {
	items map[string]fakeAvatarFetch
}

func (f fakeAvatarFetcher) Download(_ context.Context, sourceURL string) (DownloadedAvatar, error) {
	item, ok := f.items[sourceURL]
	if !ok {
		return DownloadedAvatar{}, errors.New("missing fixture avatar")
	}
	return item.avatar, item.err
}

type fakeAvatarObject struct {
	body        []byte
	size        int64
	contentType string
}

type fakeAvatarObjectStore struct {
	mu      sync.Mutex
	objects map[string]fakeAvatarObject
	puts    []string
}

func newFakeAvatarStore() *fakeAvatarObjectStore {
	return &fakeAvatarObjectStore{objects: make(map[string]fakeAvatarObject)}
}

func (s *fakeAvatarObjectStore) Stat(_ context.Context, objectKey string) (AvatarObjectInfo, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[objectKey]
	if !ok {
		return AvatarObjectInfo{}, false, nil
	}
	return AvatarObjectInfo{Size: object.size, ContentType: object.contentType}, true, nil
}

func (s *fakeAvatarObjectStore) Put(_ context.Context, objectKey string, body []byte, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[objectKey] = fakeAvatarObject{body: append([]byte(nil), body...), size: int64(len(body)), contentType: contentType}
	s.puts = append(s.puts, objectKey)
	return nil
}
