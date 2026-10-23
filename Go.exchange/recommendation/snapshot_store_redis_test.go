package recommendation

import (
	"context"
	"errors"
	"fmt"
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

func TestRedisSnapshotStoreDeepPaginationUsesBoundedHashFieldsIntegration(t *testing.T) {
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisSnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := CandidatePoolSnapshot{
		ID: uuid.NewString(), OwnerDigest: "deep-pagination-owner", CreatedAt: now,
		ExpiresAt: now.Add(10 * time.Minute), PageSize: defaultSnapshotMaxPageSize,
		RankerConfigHash: "ranker-v1", EmbeddingVersion: "embedding-v1", StrategyVersion: "strategy-v3",
	}
	key := snapshotRedisKey(snapshot.ID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete deep-pagination snapshot: %v", err)
		}
	})
	if err := store.Create(context.Background(), snapshot, nil, nil, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	createdTTL, err := client.PTTL(key).Result()
	if err != nil || createdTTL <= 0 {
		t.Fatalf("initial snapshot TTL=%s err=%v", createdTTL, err)
	}
	var contextEntries []SnapshotContextEntry
	const pageCount = 100
	const pageSize = defaultSnapshotMaxPageSize
	for pageIndex := 0; pageIndex < pageCount; pageIndex++ {
		state, err := store.Load(context.Background(), snapshot.ID)
		if err != nil {
			t.Fatalf("load before page %d: %v", pageIndex, err)
		}
		page := CachedPage{Index: pageIndex, RequestID: fmt.Sprintf("deep-page-%03d", pageIndex), ServedAt: now.Add(time.Duration(pageIndex) * time.Second), Items: make([]CachedCandidate, 0, pageSize)}
		postStates := make([]SnapshotPostStateUpdate, 0, pageSize)
		for offset := 0; offset < pageSize; offset++ {
			id := uint(pageIndex*pageSize + offset + 1)
			item := CachedCandidate{
				PostID: id, AuthorID: id%97 + 1, CreatedAt: now.Add(-time.Duration(id) * time.Second),
				Candidate: Candidate{PostID: id, RecallTier: RecallTierHistoricalUnseen, RecentRank: offset + 1, FromHistorical: true},
				Breakdown: ScoreBreakdown{FinalScore: float64(pageSize - offset)}, Phase: SelectionPhaseFresh,
			}
			page.Items = append(page.Items, item)
			postStates = append(postStates, SnapshotPostStateUpdate{PostID: id, Next: snapshotPostState("reserved", pageIndex)})
		}
		contextEntries = appendSnapshotContext(contextEntries, page, defaultSemanticHistoryWindowSize)
		state.PageCount = pageIndex + 1
		state.Revision++
		applied, err := store.Apply(context.Background(), snapshot.ID, state.Revision-1, SnapshotMutation{
			Snapshot: state, Page: &page, PostStates: postStates, Context: &contextEntries,
		})
		if err != nil || !applied {
			t.Fatalf("apply page %d: applied=%t err=%v", pageIndex, applied, err)
		}
		firstRead, found, err := store.LoadPage(context.Background(), snapshot.ID, pageIndex)
		if err != nil || !found || len(firstRead.Items) != pageSize {
			t.Fatalf("page %d read: found=%t items=%d err=%v", pageIndex, found, len(firstRead.Items), err)
		}
		replayed, found, err := store.LoadPage(context.Background(), snapshot.ID, pageIndex)
		if err != nil || !found || replayed.RequestID != firstRead.RequestID || !equalUintSlices(cachedPagePostIDs(replayed), cachedPagePostIDs(firstRead)) {
			t.Fatalf("page %d replay changed stable record: request=%q want=%q err=%v", pageIndex, replayed.RequestID, firstRead.RequestID, err)
		}
	}
	loaded, err := store.Load(context.Background(), snapshot.ID)
	if err != nil || loaded.PageCount != pageCount {
		t.Fatalf("deep snapshot state=%#v err=%v", loaded, err)
	}
	if entries, err := store.LoadContext(context.Background(), snapshot.ID, pageCount); err != nil || len(entries) != defaultSemanticHistoryWindowSize {
		t.Fatalf("deep context entries=%d err=%v want=%d", len(entries), err, defaultSemanticHistoryWindowSize)
	}
	knownCount := 0
	for start := uint(1); start <= pageCount*pageSize; {
		end := start + uint(maxSnapshotKnownPostIDsPerOperation)
		if end > pageCount*pageSize+1 {
			end = pageCount*pageSize + 1
		}
		ids := make([]uint, 0, end-start)
		for id := start; id < end; id++ {
			ids = append(ids, id)
		}
		known, err := store.KnownPostIDs(context.Background(), snapshot.ID, ids)
		if err != nil {
			t.Fatalf("load exact known IDs %d..%d: %v", start, end-1, err)
		}
		knownCount += len(known)
		start = end
	}
	if knownCount != pageCount*pageSize {
		t.Fatalf("exact known post IDs=%d want %d", knownCount, pageCount*pageSize)
	}
	bytesUsed, err := client.HGet(key, "_bytes").Int()
	if err != nil {
		t.Fatal(err)
	}
	if bytesUsed <= 1<<20 || bytesUsed > defaultSnapshotMaxStorageBytes {
		t.Fatalf("100-page split snapshot bytes=%d want >1 MiB and <=%d", bytesUsed, defaultSnapshotMaxStorageBytes)
	}
	remainingTTL, err := client.PTTL(key).Result()
	if err != nil || remainingTTL <= 0 || remainingTTL >= createdTTL {
		t.Fatalf("snapshot TTL refreshed or missing: initial=%s remaining=%s err=%v", createdTTL, remainingTTL, err)
	}
	fieldCount, err := client.HLen(key).Result()
	if err != nil || fieldCount > maxSnapshotHashFields {
		t.Fatalf("snapshot hash fields=%d err=%v max=%d", fieldCount, err, maxSnapshotHashFields)
	}
	tooLarge := loaded
	tooLarge.PageCount = defaultSnapshotMaxPages + 1
	tooLarge.Revision++
	if _, err := store.Apply(context.Background(), snapshot.ID, loaded.Revision, SnapshotMutation{Snapshot: tooLarge}); !errors.Is(err, ErrSnapshotCapacity) {
		t.Fatalf("over-budget page count error=%v want ErrSnapshotCapacity", err)
	}
	t.Logf("100-page Redis hash bytes=%d fields=%d exact IDs=%d context=%d TTL remaining=%s", bytesUsed, fieldCount, knownCount, defaultSemanticHistoryWindowSize, remainingTTL)
}

func TestRedisSnapshotStoreInitialEightPagePreselectionIntegration(t *testing.T) {
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisSnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	_, result, snapshot, contextEntries := createInitialPreselectionSnapshotWithStore(
		t, 500, 50, 8, 128, 8, nil, nil, store,
	)
	key := snapshotRedisKey(result.PoolID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete initial preselection snapshot: %v", err)
		}
	})
	if snapshot.PageCount != 8 || len(contextEntries) != 128 {
		t.Fatalf("initial Redis snapshot pages=%d context=%d, want 8 pages and 128 context entries", snapshot.PageCount, len(contextEntries))
	}
	initialTTL, err := client.PTTL(key).Result()
	if err != nil || initialTTL <= 0 {
		t.Fatalf("initial Redis snapshot TTL=%s err=%v", initialTTL, err)
	}
	allIDs := make([]uint, 0, 400)
	for pageIndex := 0; pageIndex < 8; pageIndex++ {
		page, exists, err := store.LoadPage(context.Background(), result.PoolID, pageIndex)
		if err != nil || !exists || len(page.Items) != 50 {
			t.Fatalf("load preselected page %d: exists=%t items=%d err=%v", pageIndex, exists, len(page.Items), err)
		}
		allIDs = append(allIDs, cachedPagePostIDs(page)...)
	}
	seen := make(map[uint]struct{}, len(allIDs))
	for _, id := range allIDs {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("Post %d appears in more than one Redis preselection page", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != 400 {
		t.Fatalf("Redis preselection unique IDs=%d want 400", len(seen))
	}
	known, err := store.KnownPostIDs(context.Background(), result.PoolID, allIDs)
	if err != nil || len(known) != 400 {
		t.Fatalf("Redis preselection known IDs=%d err=%v want 400", len(known), err)
	}
	remainingTTL, err := client.PTTL(key).Result()
	if err != nil || remainingTTL <= 0 || remainingTTL > initialTTL {
		t.Fatalf("Redis preselection reads changed TTL: initial=%s remaining=%s err=%v", initialTTL, remainingTTL, err)
	}
	if exists, err := client.Exists(key).Result(); err != nil || exists != 1 {
		t.Fatalf("Redis preselection hash exists=%d err=%v", exists, err)
	}
}

func TestRedisSnapshotStoreCreateRejectsDuplicatePostAcrossPagesIntegration(t *testing.T) {
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisSnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := CandidatePoolSnapshot{
		ID: uuid.NewString(), OwnerDigest: "duplicate-preselection-owner", CreatedAt: now,
		ExpiresAt: now.Add(time.Minute), PageSize: 20, EmbeddingVersion: "embedding-v1",
		RankerConfigHash: "ranker-v1", StrategyVersion: "strategy-v3",
	}
	key := snapshotRedisKey(snapshot.ID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete rejected duplicate snapshot key: %v", err)
		}
	})
	item := CachedCandidate{PostID: 44, Candidate: Candidate{PostID: 44}}
	pages := []CachedPage{
		{Index: 0, RequestID: "duplicate-page-0", ServedAt: now, Items: []CachedCandidate{item}},
		{Index: 1, RequestID: "duplicate-page-1", ServedAt: now, Items: []CachedCandidate{item}},
	}
	if err := store.Create(context.Background(), snapshot, pages, nil, time.Minute); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("duplicate preselection create error=%v, want ErrSnapshotUnavailable", err)
	}
	if exists, err := client.Exists(key).Result(); err != nil || exists != 0 {
		t.Fatalf("rejected duplicate snapshot left a Redis key: exists=%d err=%v", exists, err)
	}
}

func TestRedisSnapshotStoreCreateLoadCASAndExpiryIntegration(t *testing.T) {
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisSnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := CandidatePoolSnapshot{
		Version: candidatePoolSnapshotVersion, ID: uuid.NewString(), OwnerDigest: "owner",
		CreatedAt: now, ExpiresAt: now.Add(2 * time.Second), PageSize: 20,
		Candidates: []CachedCandidate{{PostID: 9, Candidate: Candidate{PostID: 9}}},
	}
	page := CachedPage{Index: 0, RequestID: "request-0", ServedAt: now, Items: []CachedCandidate{{PostID: 9, Candidate: Candidate{PostID: 9}}}}
	selectionContext := []SnapshotContextEntry{{PageIndex: 0, Candidate: page.Items[0]}}
	key := snapshotRedisKey(snapshot.ID)
	t.Cleanup(func() {
		if err := client.Del(key).Err(); err != nil {
			t.Errorf("delete recommendation snapshot integration key: %v", err)
		}
	})
	if err := store.Create(context.Background(), snapshot, []CachedPage{page}, selectionContext, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if kind, err := client.Type(key).Result(); err != nil || kind != "hash" {
		t.Fatalf("snapshot Redis type=%q err=%v, want hash", kind, err)
	}
	for _, field := range []string{"meta", "pool", "context", "pending_history", "page:0", "post:9", "_bytes"} {
		if exists, err := client.HExists(key, field).Result(); err != nil || !exists {
			t.Fatalf("snapshot field %q exists=%v err=%v", field, exists, err)
		}
	}
	loaded, err := store.Load(context.Background(), snapshot.ID)
	if err != nil || loaded.Revision != 1 || loaded.PageCount != 1 || len(loaded.Candidates) != 1 || len(loaded.Pages) != 0 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	loadedPage, exists, err := store.LoadPage(context.Background(), snapshot.ID, 0)
	if err != nil || !exists || loadedPage.RequestID != page.RequestID {
		t.Fatalf("page=%#v exists=%t err=%v", loadedPage, exists, err)
	}
	updatedPage := loadedPage
	updatedPage.Committed = true
	updatedPage.ReturnedPostIDs = []uint{9}
	updated := cloneSnapshot(loaded)
	updated.Revision = 2
	mutation := SnapshotMutation{
		Snapshot: updated, Page: &updatedPage,
		PostStates:           []SnapshotPostStateUpdate{{PostID: 9, Expected: snapshotPostState("reserved", 0), Next: snapshotPostState("used", 0)}},
		PendingHistoryChange: SnapshotPendingHistoryAdd, PendingHistoryPage: 0,
	}
	if committed, err := store.Apply(context.Background(), snapshot.ID, 1, mutation); err != nil || !committed {
		t.Fatalf("page commit applied=%v err=%v", committed, err)
	}
	if committed, err := store.Apply(context.Background(), snapshot.ID, 1, mutation); err != nil || committed {
		t.Fatalf("stale mutation applied=%v err=%v", committed, err)
	}
	loaded, err = store.Load(context.Background(), snapshot.ID)
	if err != nil || loaded.Revision != 2 {
		t.Fatalf("updated snapshot=%#v err=%v", loaded, err)
	}
	known, err := store.KnownPostIDs(context.Background(), snapshot.ID, []uint{9, 10})
	if err != nil || len(known) != 1 {
		t.Fatalf("known IDs=%v err=%v", known, err)
	}
	pending, err := store.LoadPendingHistory(context.Background(), snapshot.ID, 1)
	if err != nil || len(pending) != 1 || pending[0].Index != 0 {
		t.Fatalf("pending history=%#v err=%v", pending, err)
	}
	updated.Revision = 3
	completedPage := updatedPage
	completedPage.HistoryRecorded = true
	if committed, err := store.Apply(context.Background(), snapshot.ID, 2, SnapshotMutation{
		Snapshot: updated, Page: &completedPage,
		PendingHistoryChange: SnapshotPendingHistoryRemove, PendingHistoryPage: 0,
	}); err != nil || !committed {
		t.Fatalf("history receipt applied=%v err=%v", committed, err)
	}
	pending, err = store.LoadPendingHistory(context.Background(), snapshot.ID, 1)
	if err != nil || len(pending) != 0 {
		t.Fatalf("completed pending history=%#v err=%v", pending, err)
	}
	time.Sleep(2100 * time.Millisecond)
	if _, err := store.Load(context.Background(), snapshot.ID); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired snapshot error=%v want cursor expired", err)
	}
}
