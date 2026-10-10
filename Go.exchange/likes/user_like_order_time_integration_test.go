package likes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestMutationOrderTimeMatchesBehaviorStateAndConcurrentOrderIntegration(t *testing.T) {
	client, store, basePostID := openRecoverableStoreIntegration(t)
	userID := likeIntegrationUserID(basePostID, 1)
	postIDs := []uint{basePostID, basePostID + 1, basePostID + 2, basePostID + 3}
	t.Cleanup(func() {
		for _, postID := range postIDs {
			cleanupRecoverableStorePost(client, postID, userID)
		}
	})
	ctx := context.Background()
	for _, postID := range postIDs {
		if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
			t.Fatalf("initialize Post %d created=%t err=%v", postID, created, err)
		}
	}
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatal(err)
	}

	first, err := store.Mutate(ctx, userID, postIDs[0], true)
	if err != nil || !first.Changed {
		t.Fatalf("first Like result=%+v err=%v", first, err)
	}
	firstState, firstScore := assertMutationOrderTimeMatchesBehaviorState(t, client, userID, postIDs[0], true, first.Version)

	duplicate, err := store.Mutate(ctx, userID, postIDs[0], true)
	if err != nil || duplicate.Changed {
		t.Fatalf("duplicate Like result=%+v err=%v", duplicate, err)
	}
	duplicateState, duplicateScore := assertMutationOrderTimeMatchesBehaviorState(t, client, userID, postIDs[0], true, first.Version)
	if !duplicateState.Equal(firstState) || duplicateScore != firstScore {
		t.Fatalf("duplicate Like changed order time: state %s -> %s, score %v -> %v", firstState, duplicateState, firstScore, duplicateScore)
	}

	time.Sleep(2 * time.Millisecond)
	unliked, err := store.Mutate(ctx, userID, postIDs[0], false)
	if err != nil || !unliked.Changed {
		t.Fatalf("Unlike result=%+v err=%v", unliked, err)
	}
	unlikeState := readMutationBehaviorState(t, client, userID, postIDs[0], false, unliked.Version)
	if !unlikeState.After(firstState) {
		t.Fatalf("Unlike timestamp=%s did not advance from Like timestamp=%s", unlikeState, firstState)
	}
	if _, err := client.ZScore(UserLikesOrderKey(userID), strconv.FormatUint(uint64(postIDs[0]), 10)).Result(); !errors.Is(err, redis.Nil) {
		t.Fatal("Unlike left the Post in the User Like order index")
	}

	time.Sleep(2 * time.Millisecond)
	reliked, err := store.Mutate(ctx, userID, postIDs[0], true)
	if err != nil || !reliked.Changed {
		t.Fatalf("re-Like result=%+v err=%v", reliked, err)
	}
	relikedState, relikedScore := assertMutationOrderTimeMatchesBehaviorState(t, client, userID, postIDs[0], true, reliked.Version)
	if !relikedState.After(unlikeState) || relikedScore <= firstScore {
		t.Fatalf("re-Like order time did not advance: unlike=%s re-like=%s old_score=%v new_score=%v", unlikeState, relikedState, firstScore, relikedScore)
	}

	var wait sync.WaitGroup
	errCh := make(chan error, len(postIDs)-1)
	for _, postID := range postIDs[1:] {
		wait.Add(1)
		go func(postID uint) {
			defer wait.Done()
			result, mutateErr := store.Mutate(ctx, userID, postID, true)
			if mutateErr != nil {
				errCh <- mutateErr
			} else if !result.Changed {
				errCh <- fmt.Errorf("concurrent Like of Post %d was unexpectedly idempotent", postID)
			}
		}(postID)
	}
	wait.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	type orderEntry struct {
		member string
		at     time.Time
	}
	wantOrder := make([]orderEntry, 0, len(postIDs))
	for _, postID := range postIDs {
		at, _ := assertMutationOrderTimeMatchesBehaviorState(t, client, userID, postID, true, -1)
		wantOrder = append(wantOrder, orderEntry{member: strconv.FormatUint(uint64(postID), 10), at: at})
	}
	sort.Slice(wantOrder, func(i, j int) bool {
		if wantOrder[i].at.Equal(wantOrder[j].at) {
			return wantOrder[i].member < wantOrder[j].member
		}
		return wantOrder[i].at.Before(wantOrder[j].at)
	})
	wantMembers := make([]string, len(wantOrder))
	for index, entry := range wantOrder {
		wantMembers[index] = entry.member
	}
	gotMembers, err := client.ZRange(UserLikesOrderKey(userID), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotMembers) != len(wantMembers) {
		t.Fatalf("order members=%v want=%v", gotMembers, wantMembers)
	}
	for index := range wantMembers {
		if gotMembers[index] != wantMembers[index] {
			t.Fatalf("concurrent Like order=%v want=%v", gotMembers, wantMembers)
		}
	}
}

func assertMutationOrderTimeMatchesBehaviorState(t *testing.T, client *redis.Client, userID, postID uint, liked bool, version int64) (time.Time, float64) {
	t.Helper()
	at := readMutationBehaviorState(t, client, userID, postID, liked, version)
	member := strconv.FormatUint(uint64(postID), 10)
	score, err := client.ZScore(UserLikesOrderKey(userID), member).Result()
	if err != nil {
		t.Fatal(err)
	}
	if score != float64(at.UnixMicro()) {
		t.Fatalf("Post %d ZSET score=%v differs from Behavior state time=%s (%d micros)", postID, score, at, at.UnixMicro())
	}
	return at, score
}

func readMutationBehaviorState(t *testing.T, client *redis.Client, userID, postID uint, liked bool, version int64) time.Time {
	t.Helper()
	encoded, err := client.HGet(BehaviorStateKey, BehaviorPair(userID, postID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	gotLiked, gotVersion, at, err := parseBehaviorState(encoded)
	if err != nil || gotLiked != liked || (version > 0 && gotVersion != version) {
		t.Fatalf("Behavior state=%q liked=%t version=%d err=%v", encoded, gotLiked, gotVersion, err)
	}
	return at
}
