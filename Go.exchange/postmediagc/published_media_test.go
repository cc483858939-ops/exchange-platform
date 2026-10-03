package postmediagc

import (
	"Go.exchange/postmedia"
	"Go.exchange/postmediaupload"
	"context"
	"errors"
	"reflect"
	"testing"
)

func publishedClaim(t *testing.T) (postmediaupload.CleanupClaim, []string) {
	t.Helper()
	id := "550e8400-e29b-41d4-a716-446655440000"
	paths, err := postmedia.BuildUserV1ObjectPaths(42, id, ".webp", ".png")
	if err != nil {
		t.Fatal(err)
	}
	_, keys, err := postmedia.UserDeletionKeys(42, postmedia.PublicURL(paths.MediumObjectKey), postmedia.PublicURL(paths.LargeObjectKey))
	if err != nil {
		t.Fatal(err)
	}
	return postmediaupload.CleanupClaim{Published: true, MediaID: id, OwnerID: 42, MediumObjectKey: paths.MediumObjectKey, LargeObjectKey: paths.LargeObjectKey, ClaimToken: "claim", CleanupAttempts: 1}, keys
}

func TestPublishedMediaGCDeletesBoundedOwnedObjectsAndRetriesPartialFailures(t *testing.T) {
	claim, keys := publishedClaim(t)
	store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{{claim}}}
	deleter := &fakeDeleter{checkBound: true, failKeys: map[string]error{keys[2]: errors.New("storage unavailable")}}
	runner := NewRunner(store, deleter, DefaultConfig())
	summary, err := runner.Run(context.Background())
	if !errors.Is(err, ErrRowsRetryScheduled) || summary.RetryScheduled != 1 || len(store.completed) != 0 || !reflect.DeepEqual(deleter.keys, keys) {
		t.Fatalf("summary=%+v err=%v keys=%v completed=%v", summary, err, deleter.keys, store.completed)
	}
	store.batches = [][]postmediaupload.CleanupClaim{{claim}}
	deleter.failKeys = nil
	deleter.keys = nil
	summary, err = runner.Run(context.Background())
	if err != nil || summary.Cleaned != 1 || len(store.completed) != 1 || !reflect.DeepEqual(deleter.keys, keys) {
		t.Fatalf("retry summary=%+v err=%v keys=%v", summary, err, deleter.keys)
	}
}

func TestPublishedMediaGCNeverDeletesReferencedOrUnverifiedObjects(t *testing.T) {
	for _, test := range []string{"shared", "database failure", "wrong owner", "wrong media ID"} {
		t.Run(test, func(t *testing.T) {
			claim, _ := publishedClaim(t)
			store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{{claim}}}
			switch test {
			case "shared":
				store.retainPublished = true
			case "database failure":
				store.checkErr = errors.New("database unavailable")
			case "wrong owner":
				claim.OwnerID = 43
				store.batches[0][0] = claim
			case "wrong media ID":
				claim.MediaID = "550e8400-e29b-41d4-a716-446655440001"
				store.batches[0][0] = claim
			}
			deleter := &fakeDeleter{}
			summary, err := NewRunner(store, deleter, DefaultConfig()).Run(context.Background())
			if !errors.Is(err, ErrRowsRetryScheduled) || summary.RetryScheduled != 1 || len(deleter.keys) != 0 || len(store.completed) != 0 {
				t.Fatalf("summary=%+v err=%v deleted=%v", summary, err, deleter.keys)
			}
		})
	}
}
