package recommendation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecommendationCursorCodecBindsOwnerPageSizeAndSignature(t *testing.T) {
	codec, err := NewCursorCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	viewer := Viewer{Kind: ViewerAuthenticated, UserID: 42}
	cursor, err := codec.Encode("snapshot-1", 3, 20, viewer, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(cursor, viewer, 20, now)
	if err != nil || decoded.SnapshotID != "snapshot-1" || decoded.PageIndex != 3 {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	if _, err := codec.Decode(cursor, Viewer{Kind: ViewerAuthenticated, UserID: 43}, 20, now); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("other user cursor error=%v want invalid cursor", err)
	}
	if _, err := codec.Decode(cursor, viewer, 21, now); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed page size error=%v want invalid cursor", err)
	}
	separator := strings.IndexByte(cursor, '.')
	changed := []byte(cursor)
	if changed[separator-1] == 'A' {
		changed[separator-1] = 'B'
	} else {
		changed[separator-1] = 'A'
	}
	if _, err := codec.Decode(string(changed), viewer, 20, now); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error=%v want invalid cursor", err)
	}
	expired, err := codec.Encode("snapshot-2", 0, 20, viewer, now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(expired, viewer, 20, now); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired cursor error=%v want cursor expired", err)
	}
}

func TestRedisSnapshotStoreCreateLoadCASAndExpiryIntegration(t *testing.T) {
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisSnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := CandidatePoolSnapshot{
		Version: candidatePoolSnapshotVersion, ID: uuid.NewString(), OwnerDigest: "owner",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(3 * time.Second), PageSize: 20,
		Revision: 1, Candidates: []CachedCandidate{{PostID: 9, Candidate: Candidate{PostID: 9}}},
	}
	key := snapshotRedisKey(snapshot.ID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete recommendation snapshot integration key: %v", err)
		}
	})
	if err := store.Create(context.Background(), snapshot, 3*time.Second, defaultSnapshotMaxBytes); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), snapshot.ID)
	if err != nil || loaded.Revision != 1 || len(loaded.Candidates) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	updated := cloneSnapshot(loaded)
	updated.Revision = 2
	updated.UsedPostIDs = []uint{9}
	if committed, err := store.CompareAndSwap(context.Background(), snapshot.ID, 1, updated, defaultSnapshotMaxBytes); err != nil || !committed {
		t.Fatalf("CAS committed=%v err=%v", committed, err)
	}
	if committed, err := store.CompareAndSwap(context.Background(), snapshot.ID, 1, updated, defaultSnapshotMaxBytes); err != nil || committed {
		t.Fatalf("stale CAS committed=%v err=%v", committed, err)
	}
	loaded, err = store.Load(context.Background(), snapshot.ID)
	if err != nil || loaded.Revision != 2 || len(loaded.UsedPostIDs) != 1 {
		t.Fatalf("updated snapshot=%#v err=%v", loaded, err)
	}
	if err := client.Del(key).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), snapshot.ID); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("missing snapshot error=%v want cursor expired", err)
	}
}
