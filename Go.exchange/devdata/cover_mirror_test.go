package devdata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"Go.exchange/profilecover"
	"Go.exchange/profilecoverimage"
)

func TestCoverDownloaderAcceptsBoundedBodiesAndDefersImageValidationToOptimizer(t *testing.T) {
	fixtures := map[string][]byte{
		"jpeg": avatarJPEGFixture(t),
		"png":  avatarPNGFixture(t),
		"webp": avatarWebPFixture(),
	}
	server, downloader := newLocalCoverDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := fixtures[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	})
	defer server.Close()

	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			downloaded, err := downloader.Download(context.Background(), server.URL+"/"+name)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if string(downloaded.Body) != string(body) {
				t.Fatalf("downloaded body does not match fixture")
			}
			if _, err := profilecoverimage.Optimize(downloaded.Body); err != nil {
				t.Fatalf("optimizer rejected downloaded %s fixture: %v", name, err)
			}
		})
	}
}

func TestCoverDownloaderRejectsUnsafeResponsesAndURLs(t *testing.T) {
	if _, err := parseCoverURL("https://pbs.twimg.com:443/profile_banners/a.jpg", coverSourceHost); err != nil {
		t.Fatalf("standard HTTPS port rejected: %v", err)
	}
	if _, err := parseCoverURL("https://pbs.twimg.com:444/profile_banners/a.jpg", coverSourceHost); err == nil {
		t.Fatal("nonstandard HTTPS port accepted")
	}

	server, downloader := newLocalCoverDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty":
			return
		case "/oversized":
			_, _ = w.Write(make([]byte, profilecoverimage.MaxSourceBytes+1))
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/redirect":
			http.Redirect(w, r, "https://example.invalid/cover.jpg", http.StatusFound)
		case "/too-many":
			http.Redirect(w, r, "/one", http.StatusFound)
		case "/one":
			http.Redirect(w, r, "/two", http.StatusFound)
		case "/two":
			http.Redirect(w, r, "/three", http.StatusFound)
		case "/three":
			http.Redirect(w, r, "/four", http.StatusFound)
		case "/four":
			_, _ = w.Write(avatarJPEGFixture(t))
		default:
			_, _ = w.Write([]byte("not an image"))
		}
	})
	defer server.Close()

	for _, path := range []string{"empty", "oversized", "404", "redirect", "too-many"} {
		t.Run(path, func(t *testing.T) {
			if _, err := downloader.Download(context.Background(), server.URL+"/"+path); err == nil {
				t.Fatal("unsafe cover response was accepted")
			}
		})
	}

	for name, rawURL := range map[string]string{
		"http":      "http://pbs.twimg.com/profile_banners/a.jpg",
		"userinfo":  "https://user:password@pbs.twimg.com/profile_banners/a.jpg",
		"wronghost": "https://avatars.example.test/profile_banners/a.jpg",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCoverDownloader().Download(context.Background(), rawURL); err == nil {
				t.Fatal("unsafe cover URL was accepted")
			}
		})
	}
}

func TestPrepareCoverMirrorsPreservesThreeBannerStatesAndReusesObjects(t *testing.T) {
	registry, snapshot := avatarTestRegistrySnapshot()
	for index := range snapshot.Accounts {
		snapshot.Accounts[index].ProfileBannerPresent = true
		snapshot.Accounts[index].ProfileBannerURL = "https://pbs.twimg.com/profile_banners/" + strings.ToLower(snapshot.Accounts[index].RegistryKey) + ".jpg"
	}
	(snapshot.Accounts[1]).ProfileBannerURL = ""
	snapshot.Accounts[2].ProfileBannerPresent = false
	snapshot.Accounts[2].ProfileBannerURL = ""

	body := avatarJPEGFixture(t)
	fetcher := fakeCoverFetcher{items: map[string]fakeCoverFetch{
		snapshot.Accounts[0].ProfileBannerURL: {cover: DownloadedCover{Body: body}},
	}}
	store := newFakeAvatarStore()

	resolutions, report, err := PrepareCoverMirrors(context.Background(), registry, snapshot, fetcher, store)
	if err != nil {
		t.Fatalf("PrepareCoverMirrors: %v", err)
	}
	if report.Attempted != 1 || report.Uploaded != 1 || report.Reused != 0 || report.Cleared != 1 || report.Failed != 0 {
		t.Fatalf("report=%#v", report)
	}
	resolution, ok := resolutions[registry.Accounts[0].Key]
	if !ok || resolution.LocalURL != profilecover.FilesURLPrefix+resolution.ObjectKey || !profilecover.IsDevDataPublicObjectKey(resolution.ObjectKey) {
		t.Fatalf("resolution=%#v", resolution)
	}
	if len(store.puts) != 1 {
		t.Fatalf("put count=%d", len(store.puts))
	}

	resolutions, report, err = PrepareCoverMirrors(context.Background(), registry, snapshot, fetcher, store)
	if err != nil {
		t.Fatalf("idempotent PrepareCoverMirrors: %v", err)
	}
	if report.Attempted != 1 || report.Uploaded != 0 || report.Reused != 1 || report.Cleared != 1 || report.Failed != 0 || len(resolutions) != 1 {
		t.Fatalf("idempotent report=%#v resolutions=%d", report, len(resolutions))
	}
}

func TestPrepareCoverMirrorsContinuesAfterOneFailure(t *testing.T) {
	registry, snapshot := avatarTestRegistrySnapshot()
	for index := range snapshot.Accounts {
		snapshot.Accounts[index].ProfileBannerPresent = true
		snapshot.Accounts[index].ProfileBannerURL = "https://pbs.twimg.com/profile_banners/" + strings.ToLower(snapshot.Accounts[index].RegistryKey) + ".jpg"
	}
	fetcher := fakeCoverFetcher{items: map[string]fakeCoverFetch{
		snapshot.Accounts[0].ProfileBannerURL: {cover: DownloadedCover{Body: avatarJPEGFixture(t)}},
		snapshot.Accounts[1].ProfileBannerURL: {err: errors.New("fixture failure")},
		snapshot.Accounts[2].ProfileBannerURL: {cover: DownloadedCover{Body: []byte("not an image")}},
	}}
	resolutions, report, err := PrepareCoverMirrors(context.Background(), registry, snapshot, fetcher, newFakeAvatarStore())
	if err != nil {
		t.Fatalf("PrepareCoverMirrors: %v", err)
	}
	if report.Attempted != 3 || report.Uploaded != 1 || report.Reused != 0 || report.Cleared != 0 || report.Failed != 2 || len(resolutions) != 1 {
		t.Fatalf("report=%#v resolutions=%d", report, len(resolutions))
	}
}

func TestBuildCoverObjectKeyAndResolutionValidation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	key, err := BuildCoverObjectKey("MKBHD", hash, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	want := profilecover.DevDataV1ObjectPrefix + "mkbhd/" + hash + ".jpg"
	if key != want || !profilecover.IsDevDataPublicObjectKey(key) {
		t.Fatalf("key=%q want=%q public=%v", key, want, profilecover.IsDevDataPublicObjectKey(key))
	}
	for _, testCase := range []struct {
		registryKey string
		hash        string
		extension   string
	}{
		{"", hash, ".jpg"},
		{"bad/key", hash, ".jpg"},
		{"bad..key", hash, ".jpg"},
		{"bad\r\nkey", hash, ".jpg"},
		{"MKBHD", strings.Repeat("A", 64), ".jpg"},
		{"MKBHD", hash[:63], ".jpg"},
		{"MKBHD", hash, ".webp"},
	} {
		if _, err := BuildCoverObjectKey(testCase.registryKey, testCase.hash, testCase.extension); err == nil {
			t.Fatalf("invalid cover object key input accepted: %#v", testCase)
		}
	}

	source := SnapshotAccount{RegistryKey: "MKBHD", ProfileBannerPresent: true, ProfileBannerURL: "https://pbs.twimg.com/profile_banners/new.jpg"}
	resolution := CoverResolution{RegistryKey: source.RegistryKey, SourceURL: source.ProfileBannerURL, ObjectKey: key, LocalURL: coverLocalURL(key), ContentHash: hash}
	if !coverResolutionUsable(source, resolution) {
		t.Fatalf("valid resolution rejected: %#v", resolution)
	}
	resolution.SourceURL = "https://pbs.twimg.com/profile_banners/old.jpg"
	if coverResolutionUsable(source, resolution) {
		t.Fatal("stale cover resolution accepted")
	}
}

func newLocalCoverDownloader(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *CoverDownloader) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return server, &CoverDownloader{client: server.Client(), allowedHost: parsed.Host}
}

type fakeCoverFetch struct {
	cover DownloadedCover
	err   error
}

type fakeCoverFetcher struct {
	items map[string]fakeCoverFetch
}

func (f fakeCoverFetcher) Download(_ context.Context, sourceURL string) (DownloadedCover, error) {
	item, ok := f.items[sourceURL]
	if !ok {
		return DownloadedCover{}, errors.New("missing fixture cover")
	}
	return item.cover, item.err
}
