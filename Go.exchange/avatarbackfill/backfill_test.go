package avatarbackfill

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	"Go.exchange/avatarimage"
	"Go.exchange/devdata"
	"Go.exchange/profileavatar"
)

func TestRegularDryRunOptimizesWithoutPutOrUpdate(t *testing.T) {
	legacyKey := "profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.jpg"
	store := newFakeBackfillStore()
	legacyBody := backfillJPEG(t, 800, 400)
	store.objects[legacyKey] = backfillObject{body: legacyBody, info: AvatarObjectInfo{Size: int64(len(legacyBody)), ContentType: "image/jpeg"}}
	report := Report{}
	processRegularUser(context.Background(), nil, store, Options{BatchSize: DefaultBatchSize}, capturedRegularUser{ID: 42, AvatarURL: profileavatar.FilesURLPrefix + legacyKey}, &report)
	if report.Eligible != 1 || report.Optimized != 1 || report.WouldUpload != 1 || report.Uploaded != 0 || report.DBUpdated != 0 || len(store.puts) != 0 {
		t.Fatalf("dry-run report=%#v puts=%v", report, store.puts)
	}
	if _, ok := store.objects[legacyKey]; !ok {
		t.Fatal("dry-run removed legacy object")
	}
}

func TestRegularBackfillClassificationAndMissingOrCorrupt(t *testing.T) {
	store := newFakeBackfillStore()
	report := Report{}
	for _, captured := range []capturedRegularUser{
		{ID: 1, AvatarURL: ""},
		{ID: 2, AvatarURL: profileavatar.FilesURLPrefix + profileavatar.UserV1ObjectPrefix + "2/" + strings.Repeat("a", 64) + ".jpg"},
		{ID: 3, AvatarURL: "https://example.invalid/avatar.jpg"},
		{ID: 4, AvatarURL: profileavatar.FilesURLPrefix + "profile-avatars/5/550e8400-e29b-41d4-a716-446655440000.jpg"},
		{ID: 5, AvatarURL: profileavatar.FilesURLPrefix + "profile-avatars/5/not-a-uuid.jpg"},
	} {
		processRegularUser(context.Background(), nil, store, Options{}, captured, &report)
	}
	missing := capturedRegularUser{ID: 6, AvatarURL: profileavatar.FilesURLPrefix + "profile-avatars/6/550e8400-e29b-41d4-a716-446655440000.jpg"}
	processRegularUser(context.Background(), nil, store, Options{}, missing, &report)
	corruptKey := "profile-avatars/7/550e8400-e29b-41d4-a716-446655440000.jpg"
	store.objects[corruptKey] = backfillObject{body: []byte("not an image"), info: AvatarObjectInfo{Size: 12, ContentType: "image/jpeg"}}
	processRegularUser(context.Background(), nil, store, Options{}, capturedRegularUser{ID: 7, AvatarURL: profileavatar.FilesURLPrefix + corruptKey}, &report)
	if report.Empty != 1 || report.AlreadyV1 != 1 || report.UnsupportedURL != 3 || report.MissingObject != 1 || report.Failed != 2 || len(store.puts) != 0 {
		t.Fatalf("classification report=%#v puts=%v", report, store.puts)
	}
}

func TestDevDataDryRunBuildsV1DerivativeAndRetainsLegacy(t *testing.T) {
	legacyBody := backfillJPEG(t, 400, 200)
	legacyHash := strings.Repeat("a", 64)
	legacyKey, err := devdata.BuildAvatarObjectKey("MKBHD", legacyHash, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeBackfillStore()
	store.objects[legacyKey] = backfillObject{body: legacyBody, info: AvatarObjectInfo{Size: int64(len(legacyBody)), ContentType: "image/jpeg"}}
	report := Report{}
	captured := capturedDevDataAccount{ID: 1, RegistryKey: "MKBHD", LocalUserID: 42, AvatarObjectKey: legacyKey, AvatarContentHash: legacyHash, UserAvatarURL: profileavatar.FilesURLPrefix + legacyKey}
	processDevDataAccount(context.Background(), nil, store, Options{}, captured, &report)
	if report.Eligible != 1 || report.Optimized != 1 || report.WouldUpload != 1 || report.Uploaded != 0 || report.DBUpdated != 0 || len(store.puts) != 0 {
		t.Fatalf("DevData dry-run report=%#v puts=%v", report, store.puts)
	}
	if _, ok := store.objects[legacyKey]; !ok {
		t.Fatal("DevData dry-run removed legacy object")
	}
	derivative, err := avatarimage.Optimize(legacyBody)
	if err != nil {
		t.Fatal(err)
	}
	v1Key, err := devdata.BuildAvatarObjectKeyV1("MKBHD", derivative.ContentHash, derivative.Extension)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v1Key, profileavatar.DevDataV1ObjectPrefix) {
		t.Fatalf("unexpected V1 key=%q", v1Key)
	}
}

func TestDevDataAlreadyV1IsIdempotentSkip(t *testing.T) {
	derivative, err := avatarimage.Optimize(backfillJPEG(t, 32, 32))
	if err != nil {
		t.Fatal(err)
	}
	key, err := devdata.BuildAvatarObjectKeyV1("MKBHD", derivative.ContentHash, derivative.Extension)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeBackfillStore()
	report := Report{}
	processDevDataAccount(context.Background(), nil, store, Options{}, capturedDevDataAccount{
		ID: 1, RegistryKey: "MKBHD", LocalUserID: 42, AvatarObjectKey: key, AvatarContentHash: derivative.ContentHash, UserAvatarURL: profileavatar.FilesURLPrefix + key,
	}, &report)
	if report.AlreadyV1 != 1 || report.Eligible != 0 || len(store.gets) != 0 || len(store.puts) != 0 {
		t.Fatalf("already V1 report=%#v gets=%v puts=%v", report, store.gets, store.puts)
	}
}

func TestReportWriteReportIncludesAllCounters(t *testing.T) {
	report := Report{RegularScanned: 1, DevDataScanned: 2, Eligible: 3, AlreadyV1: 4, Empty: 5, UnsupportedURL: 6, MissingObject: 7, Optimized: 8, WouldUpload: 9, Uploaded: 10, Reused: 11, DBUpdated: 12, ConcurrentChangeSkipped: 13, Failed: 14}
	var output bytes.Buffer
	report.WriteReport(&output)
	for _, field := range []string{"regular_scanned=1", "devdata_scanned=2", "eligible=3", "already_v1=4", "empty=5", "unsupported_url=6", "missing_object=7", "optimized=8", "would_upload=9", "uploaded=10", "reused=11", "db_updated=12", "concurrent_change_skipped=13", "failed=14"} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("report output missing %q: %s", field, output.String())
		}
	}
}

type backfillObject struct {
	body []byte
	info AvatarObjectInfo
}

type fakeBackfillStore struct {
	objects map[string]backfillObject
	gets    []string
	puts    []string
}

func newFakeBackfillStore() *fakeBackfillStore {
	return &fakeBackfillStore{objects: make(map[string]backfillObject)}
}

func (s *fakeBackfillStore) Get(_ context.Context, key string) ([]byte, AvatarObjectInfo, error) {
	s.gets = append(s.gets, key)
	object, ok := s.objects[key]
	if !ok {
		return nil, AvatarObjectInfo{}, ErrObjectNotFound
	}
	return append([]byte(nil), object.body...), object.info, nil
}

func (s *fakeBackfillStore) Stat(_ context.Context, key string) (AvatarObjectInfo, bool, error) {
	object, ok := s.objects[key]
	if !ok {
		return AvatarObjectInfo{}, false, nil
	}
	return object.info, true, nil
}

func (s *fakeBackfillStore) Put(_ context.Context, key string, body []byte, contentType string) error {
	s.puts = append(s.puts, key)
	s.objects[key] = backfillObject{body: append([]byte(nil), body...), info: AvatarObjectInfo{Size: int64(len(body)), ContentType: contentType}}
	return nil
}

func backfillJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	data := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			data.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: 120, B: 210, A: 255})
		}
	}
	var body bytes.Buffer
	if err := jpeg.Encode(&body, data, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
