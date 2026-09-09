package devdata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildSnapshotPostResolvesSourceLanguage(t *testing.T) {
	account := testRegistry().Accounts[0]
	for _, test := range []struct {
		name string
		lang string
		text string
		want string
	}{
		{name: "official X supported language", lang: "en", text: "这个推荐系统现在越来越稳定了", want: "en"},
		{name: "official X unsupported language", lang: "fr", text: "The recommendation system is improving steadily today.", want: "und"},
		{name: "RSSHub fallback", text: "今日は新しい推薦システムを試しています", want: "ja"},
		{name: "explicit und fallback", lang: "und", text: "今日は新しい推薦システムを試しています", want: "ja"},
	} {
		t.Run(test.name, func(t *testing.T) {
			post := BuildSnapshotPost(account, XPost{
				ID:        "456",
				AuthorID:  "123",
				CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Text:      test.text,
				Lang:      test.lang,
			})
			if post.Language != test.want {
				t.Fatalf("BuildSnapshotPost language=%q want %q", post.Language, test.want)
			}
		})
	}
}

func TestValidateSnapshotRequiresCanonicalLanguage(t *testing.T) {
	for _, language := range []string{"", "fr", "english", "jp", "ZH-cn"} {
		snapshot := testSnapshot("a valid source Post")
		snapshot.Posts[0].Language = language
		if err := ValidateSnapshot(snapshot, testRegistry()); err == nil {
			t.Fatalf("ValidateSnapshot accepted non-canonical language %q", language)
		}
	}
}

func TestReadSnapshotNormalizesLegacyBlankLanguage(t *testing.T) {
	snapshot := testSnapshot("今日は新しい推薦システムを試しています")
	snapshot.Posts[0].Language = ""
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy-snapshot.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := ReadSnapshot(path, testRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Posts[0].Language != "ja" {
		t.Fatalf("legacy disk snapshot language=%q want ja", loaded.Posts[0].Language)
	}
}

func TestNormalizePersistedPostLanguagePreservesUndAndNormalizesSource(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		text string
		want string
	}{
		{name: "blank detects legacy Japanese", text: "今日は新しい推薦システムを試しています", want: "ja"},
		{name: "canonical und remains und", raw: "und", text: "The recommendation system is improving steadily today.", want: "und"},
		{name: "source alias normalizes without detection", raw: "EN-us", text: "这个推荐系统现在越来越稳定了", want: "en"},
		{name: "unsupported source remains und", raw: "fr", text: "The recommendation system is improving steadily today.", want: "und"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizePersistedPostLanguage(test.raw, test.text); got != test.want {
				t.Fatalf("normalizePersistedPostLanguage(%q, %q)=%q want %q", test.raw, test.text, got, test.want)
			}
		})
	}
}

func TestReadSnapshotPreservesCanonicalUndAcrossDiskRoundTrip(t *testing.T) {
	registry := testRegistry()
	account := registry.Accounts[0]
	post := BuildSnapshotPost(account, XPost{
		ID:        "456",
		AuthorID:  "123",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Text:      "The recommendation system is improving steadily today.",
		Lang:      "fr",
	})
	if post.Language != "und" {
		t.Fatalf("BuildSnapshotPost language=%q want und before disk round trip", post.Language)
	}
	snapshot := testSnapshot(post.Text)
	snapshot.Posts[0] = post
	path := filepath.Join(t.TempDir(), "canonical-und-snapshot.json")
	if err := WriteSnapshotAtomic(path, snapshot, registry); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadSnapshot(path, registry)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Posts[0].Language != "und" {
		t.Fatalf("canonical und disk snapshot language=%q want und", loaded.Posts[0].Language)
	}
}

func TestAssembleSnapshotFromCheckpointNormalizesLegacyBlankLanguage(t *testing.T) {
	registry := testRegistry()
	post := testSnapshot("今日は新しい推薦システムを試しています").Posts[0]
	post.Language = ""
	checkpoint := FetchCheckpoint{
		Version:             FetchCheckpointVersion,
		Source:              FetchCheckpointSource,
		RegistryFingerprint: RegistryFingerprint(registry),
		StartedAt:           time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Completed: map[string]FetchAccountData{
			"source": {
				Account: testSnapshot(post.Text).Accounts[0],
				Posts:   []SnapshotPost{post},
				Report:  FetchAccountReport{RegistryKey: "source"},
			},
		},
	}

	assembled, err := AssembleSnapshotFromCheckpoint(checkpoint, registry, checkpoint.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(assembled.Posts) != 1 || assembled.Posts[0].Language != "ja" {
		t.Fatalf("assembled legacy checkpoint posts=%#v", assembled.Posts)
	}
}

func TestAssembleSnapshotFromCheckpointPreservesCanonicalUnd(t *testing.T) {
	registry := testRegistry()
	post := testSnapshot("The recommendation system is improving steadily today.").Posts[0]
	post.Language = "und"
	checkpoint := FetchCheckpoint{
		Version:             FetchCheckpointVersion,
		Source:              FetchCheckpointSource,
		RegistryFingerprint: RegistryFingerprint(registry),
		StartedAt:           time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Completed: map[string]FetchAccountData{
			"source": {
				Account: testSnapshot(post.Text).Accounts[0],
				Posts:   []SnapshotPost{post},
				Report:  FetchAccountReport{RegistryKey: "source"},
			},
		},
	}

	assembled, err := AssembleSnapshotFromCheckpoint(checkpoint, registry, checkpoint.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(assembled.Posts) != 1 || assembled.Posts[0].Language != "und" {
		t.Fatalf("assembled canonical und checkpoint posts=%#v", assembled.Posts)
	}
}
