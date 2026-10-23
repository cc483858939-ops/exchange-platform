package recommendation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
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

func TestSnapshotMutationJSONListsUseArraySemantics(t *testing.T) {
	deleteJSON, postStateJSON, err := marshalSnapshotMutationLists(SnapshotMutation{})
	if err != nil {
		t.Fatalf("marshal nil mutation lists: %v", err)
	}
	if deleteJSON != "[]" || postStateJSON != "[]" {
		t.Fatalf("nil mutation lists encoded as deletes=%q post_states=%q, want [] and []", deleteJSON, postStateJSON)
	}

	explicitDeleteJSON, explicitPostStateJSON, err := marshalSnapshotMutationLists(SnapshotMutation{
		DeletePageIndices: []int{}, PostStates: []SnapshotPostStateUpdate{},
	})
	if err != nil {
		t.Fatalf("marshal explicit empty mutation lists: %v", err)
	}
	if explicitDeleteJSON != deleteJSON || explicitPostStateJSON != postStateJSON {
		t.Fatalf("nil and explicit empty lists differ: nil=(%q,%q) explicit=(%q,%q)", deleteJSON, postStateJSON, explicitDeleteJSON, explicitPostStateJSON)
	}

	deleteJSON, postStateJSON, err = marshalSnapshotMutationLists(SnapshotMutation{
		DeletePageIndices: []int{3, 1},
		PostStates:        []SnapshotPostStateUpdate{{PostID: 9, Expected: "reserved:0", Next: "used:0"}},
	})
	if err != nil {
		t.Fatalf("marshal populated mutation lists: %v", err)
	}
	if deleteJSON != "[3,1]" || postStateJSON != `[{"PostID":9,"Expected":"reserved:0","Next":"used:0"}]` {
		t.Fatalf("populated mutation lists changed: deletes=%q post_states=%q", deleteJSON, postStateJSON)
	}

}

func TestSnapshotStateNilCandidatePoolUsesArraySemantics(t *testing.T) {
	_, poolJSON, err := marshalSnapshotState(CandidatePoolSnapshot{
		ID: "empty-pool", PageSize: 20, ExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("marshal nil candidate pool: %v", err)
	}
	if poolJSON != "[]" {
		t.Fatalf("nil candidate pool encoded as %q, want []", poolJSON)
	}
}

func newRedisSnapshotIntegrationStore(t *testing.T) (*redis.Client, *RedisSnapshotStore) {
	t.Helper()
	client := openHistoryIntegrationRedis(t)
	store, err := NewRedisSnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	return client, store
}

func newRedisSnapshotIntegrationMetadata(now time.Time, pageSize int) CandidatePoolSnapshot {
	return CandidatePoolSnapshot{
		ID: uuid.NewString(), OwnerDigest: "snapshot-json-array-owner", CreatedAt: now,
		ExpiresAt: now.Add(10 * time.Minute), PageSize: pageSize,
		EmbeddingVersion: "embedding-v1", RankerConfigHash: "ranker-v1", StrategyVersion: "strategy-v3",
	}
}

func redisSnapshotTestCandidate(postID uint) CachedCandidate {
	return CachedCandidate{PostID: postID, Candidate: Candidate{PostID: postID}}
}

func cleanupRedisSnapshot(t *testing.T, client *redis.Client, snapshotID string) {
	t.Helper()
	t.Cleanup(func() {
		if err := client.Del(snapshotRedisKey(snapshotID)).Err(); err != nil {
			t.Errorf("delete Redis snapshot %s: %v", snapshotID, err)
		}
	})
}

func TestRedisSnapshotStoreApplyNilDeleteListWithPostStatesIntegration(t *testing.T) {
	client, store := newRedisSnapshotIntegrationStore(t)
	now := time.Now().UTC()
	snapshot := newRedisSnapshotIntegrationMetadata(now, 2)
	cleanupRedisSnapshot(t, client, snapshot.ID)
	snapshot.Candidates = []CachedCandidate{redisSnapshotTestCandidate(7), redisSnapshotTestCandidate(9)}
	firstPage := CachedPage{
		Index: 0, RequestID: "nil-delete-page-0", ServedAt: now,
		Items: []CachedCandidate{redisSnapshotTestCandidate(7)},
	}
	if err := store.Create(context.Background(), snapshot, []CachedPage{firstPage}, []SnapshotContextEntry{{PageIndex: 0, Candidate: firstPage.Items[0]}}, 10*time.Minute); err != nil {
		t.Fatalf("create first snapshot page: %v", err)
	}
	loaded, err := store.Load(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatalf("load initial snapshot: %v", err)
	}
	secondPage := CachedPage{
		Index: 1, RequestID: "nil-delete-page-1", ServedAt: now.Add(time.Second),
		Items: []CachedCandidate{redisSnapshotTestCandidate(9)},
	}
	updated := cloneSnapshot(loaded)
	updated.PageCount = 2
	updated.Revision++
	contextEntries := []SnapshotContextEntry{
		{PageIndex: 0, Candidate: firstPage.Items[0]},
		{PageIndex: 1, Candidate: secondPage.Items[0]},
	}
	mutation := SnapshotMutation{
		Snapshot: updated, Page: &secondPage,
		PostStates: []SnapshotPostStateUpdate{{PostID: 9, Next: snapshotPostState("reserved", 1)}},
		Context:    &contextEntries,
	}
	if mutation.DeletePageIndices != nil {
		t.Fatal("test must exercise a nil DeletePageIndices argument")
	}
	if applied, err := store.Apply(context.Background(), snapshot.ID, loaded.Revision, mutation); err != nil || !applied {
		t.Fatalf("apply dynamic page: applied=%t err=%v", applied, err)
	}
	current, err := store.Load(context.Background(), snapshot.ID)
	if err != nil || current.Revision != loaded.Revision+1 || current.PageCount != 2 {
		t.Fatalf("snapshot revision/page count=%d/%d err=%v, want %d/2", current.Revision, current.PageCount, err, loaded.Revision+1)
	}
	for pageIndex, requestID := range []string{firstPage.RequestID, secondPage.RequestID} {
		page, found, err := store.LoadPage(context.Background(), snapshot.ID, pageIndex)
		if err != nil || !found || page.RequestID != requestID {
			t.Fatalf("page %d found=%t request=%q err=%v, want request %q", pageIndex, found, page.RequestID, err, requestID)
		}
	}
	if state, err := client.HGet(snapshotRedisKey(snapshot.ID), snapshotPostField(9)).Result(); err != nil || state != snapshotPostState("reserved", 1) {
		t.Fatalf("post state=%q err=%v, want reserved:1", state, err)
	}
	known, err := store.KnownPostIDs(context.Background(), snapshot.ID, []uint{7, 9})
	if err != nil || len(known) != 2 {
		t.Fatalf("known post IDs=%v err=%v, want both page IDs", known, err)
	}
	firstRead, found, err := store.LoadPage(context.Background(), snapshot.ID, 1)
	replay, replayFound, replayErr := store.LoadPage(context.Background(), snapshot.ID, 1)
	if err != nil || replayErr != nil || !found || !replayFound || firstRead.RequestID != replay.RequestID ||
		!equalUintSlices(cachedPagePostIDs(firstRead), cachedPagePostIDs(replay)) {
		t.Fatalf("dynamic page replay changed: first=%#v replay=%#v errors=%v/%v", firstRead, replay, err, replayErr)
	}
}

func TestRedisSnapshotStoreNilCandidatePoolAndPureRevisionApplyIntegration(t *testing.T) {
	client, store := newRedisSnapshotIntegrationStore(t)
	now := time.Now().UTC()
	snapshot := newRedisSnapshotIntegrationMetadata(now, 20)
	key := snapshotRedisKey(snapshot.ID)
	cleanupRedisSnapshot(t, client, snapshot.ID)
	if err := store.Create(context.Background(), snapshot, nil, nil, 10*time.Minute); err != nil {
		t.Fatalf("create nil candidate pool: %v", err)
	}
	if raw, err := client.HGet(key, "pool").Result(); err != nil || raw != "[]" {
		t.Fatalf("Redis pool field=%q err=%v, want []", raw, err)
	}
	loaded, err := store.Load(context.Background(), snapshot.ID)
	if err != nil || len(loaded.Candidates) != 0 {
		t.Fatalf("load empty candidate pool: candidates=%v err=%v", loaded.Candidates, err)
	}
	contextBefore, err := store.LoadContext(context.Background(), snapshot.ID, 1)
	if err != nil || len(contextBefore) != 0 {
		t.Fatalf("initial context=%v err=%v, want empty", contextBefore, err)
	}
	pendingBefore, err := store.LoadPendingHistory(context.Background(), snapshot.ID, 1)
	if err != nil || len(pendingBefore) != 0 {
		t.Fatalf("initial pending history=%v err=%v, want empty", pendingBefore, err)
	}
	contextRawBefore, err := client.HGet(key, "context").Result()
	if err != nil {
		t.Fatal(err)
	}
	pendingRawBefore, err := client.HGet(key, "pending_history").Result()
	if err != nil {
		t.Fatal(err)
	}
	updated := cloneSnapshot(loaded)
	updated.Candidates = nil
	updated.Revision++
	if applied, err := store.Apply(context.Background(), snapshot.ID, loaded.Revision, SnapshotMutation{Snapshot: updated}); err != nil || !applied {
		t.Fatalf("pure revision update with nil arrays applied=%t err=%v", applied, err)
	}
	if raw, err := client.HGet(key, "pool").Result(); err != nil || raw != "[]" {
		t.Fatalf("pool after nil candidate Apply=%q err=%v, want []", raw, err)
	}
	updated, err = store.Load(context.Background(), snapshot.ID)
	if err != nil || updated.Revision != loaded.Revision+1 {
		t.Fatalf("pure revision result revision=%d err=%v", updated.Revision, err)
	}
	updated.Revision++
	if applied, err := store.Apply(context.Background(), snapshot.ID, updated.Revision-1, SnapshotMutation{
		Snapshot: updated, DeletePageIndices: []int{}, PostStates: []SnapshotPostStateUpdate{},
	}); err != nil || !applied {
		t.Fatalf("pure revision update with explicit empty arrays applied=%t err=%v", applied, err)
	}
	contextAfter, err := store.LoadContext(context.Background(), snapshot.ID, 1)
	if err != nil || len(contextAfter) != len(contextBefore) {
		t.Fatalf("pure revision update changed context: before=%v after=%v err=%v", contextBefore, contextAfter, err)
	}
	contextRawAfter, err := client.HGet(key, "context").Result()
	if err != nil || contextRawAfter != contextRawBefore {
		t.Fatalf("pure revision update changed serialized context from %q to %q err=%v", contextRawBefore, contextRawAfter, err)
	}
	pendingAfter, err := store.LoadPendingHistory(context.Background(), snapshot.ID, 1)
	if err != nil || len(pendingAfter) != len(pendingBefore) {
		t.Fatalf("pure revision update changed pending history: before=%v after=%v err=%v", pendingBefore, pendingAfter, err)
	}
	pendingRawAfter, err := client.HGet(key, "pending_history").Result()
	if err != nil || pendingRawAfter != pendingRawBefore {
		t.Fatalf("pure revision update changed serialized pending history from %q to %q err=%v", pendingRawBefore, pendingRawAfter, err)
	}
	for _, field := range []string{snapshotPageField(0), snapshotPostField(9)} {
		if exists, err := client.HExists(key, field).Result(); err != nil || exists {
			t.Fatalf("pure revision update created unexpected field %q: exists=%t err=%v", field, exists, err)
		}
	}
	final, err := store.Load(context.Background(), snapshot.ID)
	if err != nil || final.Revision != loaded.Revision+2 {
		t.Fatalf("nil and explicit empty arrays did not have equivalent revision updates: revision=%d err=%v", final.Revision, err)
	}
}

func TestRedisSnapshotStoreTraceReceiptWithNilMutationListsIntegration(t *testing.T) {
	client, store := newRedisSnapshotIntegrationStore(t)
	now := time.Now().UTC()
	snapshot := newRedisSnapshotIntegrationMetadata(now, 20)
	cleanupRedisSnapshot(t, client, snapshot.ID)
	page := CachedPage{
		Index: 0, RequestID: "trace-receipt-page", ServedAt: now,
		Items: []CachedCandidate{redisSnapshotTestCandidate(9)}, ReturnedPostIDs: []uint{9},
		Committed: true, HistoryRecorded: true,
	}
	if err := store.Create(context.Background(), snapshot, []CachedPage{page}, nil, 10*time.Minute); err != nil {
		t.Fatalf("create committed page: %v", err)
	}
	key := snapshotRedisKey(snapshot.ID)
	stateBefore, err := client.HGet(key, snapshotPostField(9)).Result()
	if err != nil || stateBefore != snapshotPostState("used", 0) {
		t.Fatalf("initial post state=%q err=%v, want used:0", stateBefore, err)
	}
	loaded, err := store.Load(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	page.TraceAttempted = true
	page.Finalized = true
	updated := cloneSnapshot(loaded)
	updated.Revision++
	if applied, err := store.Apply(context.Background(), snapshot.ID, loaded.Revision, SnapshotMutation{Snapshot: updated, Page: &page}); err != nil || !applied {
		t.Fatalf("trace receipt applied=%t err=%v", applied, err)
	}
	storedPage, found, err := store.LoadPage(context.Background(), snapshot.ID, 0)
	if err != nil || !found || !storedPage.TraceAttempted || !storedPage.Finalized {
		t.Fatalf("stored trace page=%#v found=%t err=%v", storedPage, found, err)
	}
	stateAfter, err := client.HGet(key, snapshotPostField(9)).Result()
	if err != nil || stateAfter != stateBefore {
		t.Fatalf("trace update changed post state from %q to %q err=%v", stateBefore, stateAfter, err)
	}
	if applied, err := store.Apply(context.Background(), snapshot.ID, loaded.Revision, SnapshotMutation{Snapshot: updated, Page: &page}); err != nil || applied {
		t.Fatalf("stale trace receipt applied=%t err=%v, want a normal CAS conflict", applied, err)
	}
	afterStale, err := store.Load(context.Background(), snapshot.ID)
	storedAfterStale, found, pageErr := store.LoadPage(context.Background(), snapshot.ID, 0)
	stateAfterStale, stateErr := client.HGet(key, snapshotPostField(9)).Result()
	if err != nil || pageErr != nil || stateErr != nil || afterStale.Revision != loaded.Revision+1 || !found ||
		!storedAfterStale.TraceAttempted || !storedAfterStale.Finalized || stateAfterStale != stateBefore {
		t.Fatalf("stale trace receipt changed state: revision=%d page=%#v post=%q errors=%v/%v/%v", afterStale.Revision, storedAfterStale, stateAfterStale, err, pageErr, stateErr)
	}
}

func TestRedisSnapshotStoreConcurrentNilListMutationsUseRevisionCASIntegration(t *testing.T) {
	client, store := newRedisSnapshotIntegrationStore(t)
	now := time.Now().UTC()
	snapshot := newRedisSnapshotIntegrationMetadata(now, 20)
	cleanupRedisSnapshot(t, client, snapshot.ID)
	if err := store.Create(context.Background(), snapshot, nil, nil, 10*time.Minute); err != nil {
		t.Fatalf("create CAS snapshot: %v", err)
	}
	loaded, err := store.Load(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []SnapshotMutation{cloneSnapshotMutationWithBatches(loaded, 1), cloneSnapshotMutationWithBatches(loaded, 2)}
	start := make(chan struct{})
	type outcome struct {
		applied bool
		err     error
	}
	outcomes := make(chan outcome, len(mutations))
	var wait sync.WaitGroup
	for _, mutation := range mutations {
		wait.Add(1)
		go func(mutation SnapshotMutation) {
			defer wait.Done()
			<-start
			applied, err := store.Apply(context.Background(), snapshot.ID, loaded.Revision, mutation)
			outcomes <- outcome{applied: applied, err: err}
		}(mutation)
	}
	close(start)
	wait.Wait()
	close(outcomes)
	committed := 0
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("concurrent CAS error: %v", result.err)
		}
		if result.applied {
			committed++
		}
	}
	if committed != 1 {
		t.Fatalf("concurrent CAS committed %d mutations, want exactly one", committed)
	}
	current, err := store.Load(context.Background(), snapshot.ID)
	if err != nil || current.Revision != loaded.Revision+1 || (current.HistoricalBatches != 1 && current.HistoricalBatches != 2) {
		t.Fatalf("CAS winner state revision=%d batches=%d err=%v", current.Revision, current.HistoricalBatches, err)
	}
}

func cloneSnapshotMutationWithBatches(snapshot CandidatePoolSnapshot, batches int) SnapshotMutation {
	updated := cloneSnapshot(snapshot)
	updated.HistoricalBatches = batches
	updated.Revision++
	return SnapshotMutation{Snapshot: updated}
}

func TestRedisSnapshotStoreClosedClientDoesNotApplyMutationIntegration(t *testing.T) {
	client, store := newRedisSnapshotIntegrationStore(t)
	controlClient, controlStore := newRedisSnapshotIntegrationStore(t)
	now := time.Now().UTC()
	snapshot := newRedisSnapshotIntegrationMetadata(now, 20)
	cleanupRedisSnapshot(t, controlClient, snapshot.ID)
	if err := store.Create(context.Background(), snapshot, nil, nil, 10*time.Minute); err != nil {
		t.Fatalf("create snapshot before simulated Redis failure: %v", err)
	}
	before, err := controlStore.Load(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close mutation client: %v", err)
	}
	mutation := cloneSnapshotMutationWithBatches(before, 1)
	if applied, err := store.Apply(context.Background(), snapshot.ID, before.Revision, mutation); applied || !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("closed Redis client Apply applied=%t err=%v, want unavailable and not committed", applied, err)
	}
	after, err := controlStore.Load(context.Background(), snapshot.ID)
	if err != nil || after.Revision != before.Revision || after.HistoricalBatches != before.HistoricalBatches {
		t.Fatalf("failed write partially changed snapshot: before=%#v after=%#v err=%v", before, after, err)
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
		DeletePageIndices:    []int{},
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
	if raw, err := client.HGet(key, "pending_history").Result(); err != nil || raw != "[0]" {
		t.Fatalf("pending history JSON=%q err=%v, want [0]", raw, err)
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
	if raw, err := client.HGet(key, "pending_history").Result(); err != nil || raw != "[]" {
		t.Fatalf("completed pending history JSON=%q err=%v, want []", raw, err)
	}
	loaded, err = store.Load(context.Background(), snapshot.ID)
	completedPage, exists, pageErr := store.LoadPage(context.Background(), snapshot.ID, 0)
	if err != nil || pageErr != nil || loaded.Revision != 3 || !exists || !completedPage.HistoryRecorded {
		t.Fatalf("history receipt not persisted: revision=%d page=%#v exists=%t errors=%v/%v", loaded.Revision, completedPage, exists, err, pageErr)
	}
	if state, err := client.HGet(key, snapshotPostField(9)).Result(); err != nil || state != snapshotPostState("used", 0) {
		t.Fatalf("history receipt changed post state=%q err=%v, want used:0", state, err)
	}
	time.Sleep(2100 * time.Millisecond)
	if _, err := store.Load(context.Background(), snapshot.ID); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired snapshot error=%v want cursor expired", err)
	}
}
