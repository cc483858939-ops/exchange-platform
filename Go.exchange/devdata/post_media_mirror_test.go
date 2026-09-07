package devdata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPostMediaDownloaderAcceptsValidatedImageBytes(t *testing.T) {
	fixtures := map[string]struct {
		body        []byte
		contentType string
		extension   string
	}{
		"jpeg": {body: avatarJPEGFixture(t), contentType: "image/jpeg", extension: ".jpg"},
		"png":  {body: avatarPNGFixture(t), contentType: "image/png", extension: ".png"},
		"webp": {body: avatarWebPFixture(), contentType: "image/webp", extension: ".webp"},
	}
	server, downloader := newLocalPostMediaDownloader(t, func(writer http.ResponseWriter, request *http.Request) {
		fixture, ok := fixtures[strings.TrimPrefix(request.URL.Path, "/")]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write(fixture.body)
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
				t.Fatalf("hash=%q", got.ContentHash)
			}
		})
	}
}

func TestPostMediaDownloaderRejectsBadResponsesAndUnsafeURLs(t *testing.T) {
	server, downloader := newLocalPostMediaDownloader(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/empty":
			return
		case "/fake-jpeg":
			_, _ = io.WriteString(writer, "\xff\xd8\xffnot-a-jpeg")
		case "/invalid":
			_, _ = io.WriteString(writer, "not an image")
		case "/failure":
			writer.WriteHeader(http.StatusBadGateway)
		case "/oversized":
			writer.Header().Set("Content-Length", "5242881")
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	})
	defer server.Close()
	for _, path := range []string{"empty", "fake-jpeg", "invalid", "failure", "oversized"} {
		t.Run(path, func(t *testing.T) {
			if _, err := downloader.Download(context.Background(), server.URL+"/"+path); err == nil {
				t.Fatal("Download unexpectedly succeeded")
			}
		})
	}

	production := NewPostMediaDownloader()
	for name, rawURL := range map[string]string{
		"http":        "http://pbs.twimg.com/media/a.jpg",
		"userinfo":    "https://user:password@pbs.twimg.com/media/a.jpg",
		"custom-port": "https://pbs.twimg.com:443/media/a.jpg",
		"wrong-host":  "https://avatars.example.test/media/a.jpg",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := production.Download(context.Background(), rawURL); err == nil {
				t.Fatal("unsafe URL was accepted")
			}
		})
	}
}

func TestPostMediaDownloaderRevalidatesRedirects(t *testing.T) {
	server, downloader := newLocalPostMediaDownloader(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/start":
			http.Redirect(writer, request, "/final", http.StatusFound)
		case "/final":
			_, _ = writer.Write(avatarPNGFixture(t))
		case "/too-many":
			http.Redirect(writer, request, "/redirect-1", http.StatusFound)
		case "/redirect-1":
			http.Redirect(writer, request, "/redirect-2", http.StatusFound)
		case "/redirect-2":
			http.Redirect(writer, request, "/redirect-3", http.StatusFound)
		case "/redirect-3":
			http.Redirect(writer, request, "/redirect-4", http.StatusFound)
		case "/redirect-4":
			_, _ = writer.Write(avatarPNGFixture(t))
		default:
			http.NotFound(writer, request)
		}
	})
	defer server.Close()
	if _, err := downloader.Download(context.Background(), server.URL+"/start"); err != nil {
		t.Fatalf("valid redirect rejected: %v", err)
	}
	if _, err := downloader.Download(context.Background(), server.URL+"/too-many"); err == nil {
		t.Fatal("redirect overflow was accepted")
	}

	otherServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(avatarPNGFixture(t))
	}))
	defer otherServer.Close()
	redirecting := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, otherServer.URL+"/image", http.StatusFound)
	}))
	defer redirecting.Close()
	redirectURL, _ := url.Parse(redirecting.URL)
	unsafeDownloader := &PostMediaDownloader{client: redirecting.Client(), allowedHost: redirectURL.Host}
	if _, err := unsafeDownloader.Download(context.Background(), redirecting.URL+"/image"); err == nil {
		t.Fatal("redirect to a different host was accepted")
	}
}

func TestBuildPostMediaObjectKey(t *testing.T) {
	hash := strings.Repeat("a", sha256.Size*2)
	key, err := BuildPostMediaObjectKey("dotey", "123456", hash, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	if want := "post-media/devdata/dotey/123456/" + hash + ".jpg"; key != want {
		t.Fatalf("key=%q want %q", key, want)
	}
	for _, test := range []struct {
		registry string
		postID   string
		hash     string
		ext      string
	}{
		{registry: "../dotey", postID: "123456", hash: hash, ext: ".jpg"},
		{registry: "dotey", postID: "not-numeric", hash: hash, ext: ".jpg"},
		{registry: "dotey", postID: "123456", hash: strings.Repeat("A", 64), ext: ".jpg"},
		{registry: "dotey", postID: "123456", hash: hash, ext: ".gif"},
	} {
		if _, err := BuildPostMediaObjectKey(test.registry, test.postID, test.hash, test.ext); err == nil {
			t.Fatalf("unsafe key inputs accepted: %#v", test)
		}
	}
}

func TestPreparePostMediaMirrorsIsolatesFailuresAndReusesObjects(t *testing.T) {
	registry := SourceRegistry{
		Version: SourceRegistryVersion, DefaultMaxPosts: 3,
		Accounts: []SourceAccount{{Key: "source", Platform: "x", Handle: "source", Category: "test", MaxPosts: 3, Enabled: true}},
	}
	firstURL := "https://pbs.twimg.com/media/first.jpg"
	secondURL := "https://pbs.twimg.com/media/second.png"
	thirdURL := "https://pbs.twimg.com/media/third.webp"
	snapshot := Snapshot{
		Version: DefaultSnapshotVersion, FetchedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Accounts: []SnapshotAccount{{RegistryKey: "source", SourceUserID: "123", Handle: "source", Name: "Source", Category: "test"}},
		Posts: []SnapshotPost{
			{RegistryKey: "source", SourcePostID: "100", SourceURL: "https://x.com/source/status/100", Text: strings.Repeat("a", 40), CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), HasMedia: true, Media: []SnapshotMedia{{Type: "image", SourceURL: firstURL}}},
			{RegistryKey: "source", SourcePostID: "101", SourceURL: "https://x.com/source/status/101", Text: strings.Repeat("b", 40), CreatedAt: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), HasMedia: true, Media: []SnapshotMedia{{Type: "image", SourceURL: secondURL}, {Type: "image", SourceURL: thirdURL}}},
		},
	}
	firstBody := avatarJPEGFixture(t)
	secondBody := avatarPNGFixture(t)
	fetcher := fakePostMediaFetcher{items: map[string]fakePostMediaFetch{
		firstURL:  {media: downloadedPostMedia(firstBody)},
		secondURL: {media: downloadedPostMedia(secondBody)},
		thirdURL:  {err: errors.New("fixture failure")},
	}}
	store := newFakeAvatarStore()
	secondDownloaded := downloadedPostMedia(secondBody)
	secondKey, err := BuildPostMediaObjectKey("source", "101", secondDownloaded.ContentHash, secondDownloaded.Extension)
	if err != nil {
		t.Fatal(err)
	}
	store.objects[secondKey] = fakeAvatarObject{size: int64(len(secondBody)), contentType: secondDownloaded.ContentType}

	resolutions, report, err := PreparePostMediaMirrors(context.Background(), registry, snapshot, fetcher, store)
	if err != nil {
		t.Fatalf("PreparePostMediaMirrors: %v", err)
	}
	if report.PostsWithMedia != 2 || report.Attempted != 3 || report.Uploaded != 1 || report.Reused != 1 || report.Failed != 1 {
		t.Fatalf("report=%#v", report)
	}
	key100 := SourcePostKey{RegistryKey: "source", SourcePostID: "100"}
	key101 := SourcePostKey{RegistryKey: "source", SourcePostID: "101"}
	if len(resolutions[key100]) != 1 || len(resolutions[key101]) != 1 {
		t.Fatalf("resolutions=%#v", resolutions)
	}
	if _, complete := postMediaResolutionsForSync(snapshot.Posts[1], SyncOptions{PostMediaResolutions: resolutions}); complete {
		t.Fatal("partial post media resolutions were considered complete")
	}
}

type fakePostMediaFetch struct {
	media DownloadedPostMedia
	err   error
}

type fakePostMediaFetcher struct {
	items map[string]fakePostMediaFetch
}

func (f fakePostMediaFetcher) Download(_ context.Context, sourceURL string) (DownloadedPostMedia, error) {
	item, ok := f.items[sourceURL]
	if !ok {
		return DownloadedPostMedia{}, errors.New("missing fixture media")
	}
	return item.media, item.err
}

func downloadedPostMedia(body []byte) DownloadedPostMedia {
	contentType, extension, ok := detectMirrorImageType(body)
	if !ok {
		panic("test post media fixture is invalid")
	}
	hash := sha256.Sum256(body)
	return DownloadedPostMedia{Body: body, ContentType: contentType, Extension: extension, ContentHash: hexHash(hash)}
}

func newLocalPostMediaDownloader(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *PostMediaDownloader) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return server, &PostMediaDownloader{client: server.Client(), allowedHost: parsed.Host}
}
