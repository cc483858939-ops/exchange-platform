package postmediagc

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"Go.exchange/postmediaupload"
	"github.com/minio/minio-go/v7"
)

type retryCall struct {
	claim   postmediaupload.CleanupClaim
	retryAt time.Time
	reason  string
}

type fakeStore struct {
	batches     [][]postmediaupload.CleanupClaim
	claimCalls  int
	limits      []int
	completed   []postmediaupload.CleanupClaim
	retries     []retryCall
	completeErr error
	retryErr    error
}

func (store *fakeStore) ClaimCleanupBatch(_ context.Context, _ time.Time, _ time.Duration, limit int) ([]postmediaupload.CleanupClaim, error) {
	store.claimCalls++
	store.limits = append(store.limits, limit)
	if len(store.batches) == 0 {
		return nil, nil
	}
	batch := store.batches[0]
	store.batches = store.batches[1:]
	if len(batch) > limit {
		store.batches = append([][]postmediaupload.CleanupClaim{batch[limit:]}, store.batches...)
		batch = batch[:limit]
	}
	return batch, nil
}

func (store *fakeStore) CompleteCleanup(_ context.Context, claim postmediaupload.CleanupClaim) error {
	store.completed = append(store.completed, claim)
	return store.completeErr
}

func (store *fakeStore) ScheduleCleanupRetry(_ context.Context, claim postmediaupload.CleanupClaim, retryAt time.Time, reason string) error {
	store.retries = append(store.retries, retryCall{claim: claim, retryAt: retryAt, reason: reason})
	return store.retryErr
}

type fakeDeleter struct {
	keys       []string
	failKeys   map[string]error
	checkBound bool
}

func (deleter *fakeDeleter) DeleteObject(ctx context.Context, key string) error {
	deleter.keys = append(deleter.keys, key)
	if deleter.checkBound {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("object deletion has no deadline")
		}
	}
	return deleter.failKeys[key]
}

func TestDefaultConfigAndEnvironmentFallbacks(t *testing.T) {
	defaults := DefaultConfig()
	if err := defaults.Validate(); err != nil {
		t.Fatalf("default config is invalid: %v", err)
	}
	if defaults.BatchSize != 100 || defaults.MaxRowsPerRun != 1000 || defaults.ClaimTimeout != 15*time.Minute || defaults.RetryBase != 5*time.Minute || defaults.RetryMax != 6*time.Hour || defaults.ObjectTimeout != 30*time.Second || defaults.RunTimeout != 8*time.Minute {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}

	t.Setenv("POST_MEDIA_GC_BATCH_SIZE", "1001")
	t.Setenv("POST_MEDIA_GC_MAX_ROWS_PER_RUN", "-1")
	t.Setenv("POST_MEDIA_GC_CLAIM_TIMEOUT", "invalid")
	t.Setenv("POST_MEDIA_GC_RETRY_BASE", "10h")
	t.Setenv("POST_MEDIA_GC_RETRY_MAX", "1m")
	t.Setenv("POST_MEDIA_GC_OBJECT_TIMEOUT", "0s")
	t.Setenv("POST_MEDIA_GC_RUN_TIMEOUT", "24h")

	loaded := LoadConfigFromEnv()
	if err := loaded.Validate(); err != nil {
		t.Fatalf("fallback config is invalid: %v", err)
	}
	if loaded != defaults {
		t.Fatalf("invalid environment values did not fall back to safe defaults: got=%+v want=%+v", loaded, defaults)
	}
}

func TestConfigRequiresClaimLeaseToOutliveRun(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ClaimTimeout = cfg.RunTimeout
	if err := cfg.Validate(); err == nil {
		t.Fatal("config accepted a claim timeout that can expire during the run")
	}
}

func TestRetryDelayUsesBoundedExponentialBackoff(t *testing.T) {
	base, maximum := 5*time.Minute, 6*time.Hour
	cases := []struct {
		attempt int64
		want    time.Duration
	}{
		{attempt: 0, want: 0},
		{attempt: 1, want: 5 * time.Minute},
		{attempt: 2, want: 10 * time.Minute},
		{attempt: 3, want: 20 * time.Minute},
		{attempt: 20, want: maximum},
	}
	for _, testCase := range cases {
		if got := RetryDelay(testCase.attempt, base, maximum); got != testCase.want {
			t.Errorf("RetryDelay(%d)=%s want %s", testCase.attempt, got, testCase.want)
		}
	}
}

func TestRunnerDeletesAllRecordedKeysThenAcknowledges(t *testing.T) {
	claim := testCleanupClaim("media-1")
	store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{{claim}}}
	deleter := &fakeDeleter{checkBound: true}
	cfg := DefaultConfig()
	runner := NewRunner(store, deleter, cfg)
	runner.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	runner.logf = func(string, ...interface{}) {}

	summary, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	wantKeys := []string{"stored/original", "stored/medium", "stored/large", "stored/manifest"}
	if !reflect.DeepEqual(deleter.keys, wantKeys) {
		t.Fatalf("deleted keys=%v want row metadata keys=%v", deleter.keys, wantKeys)
	}
	if len(store.completed) != 1 || store.completed[0].ClaimToken != claim.ClaimToken || len(store.retries) != 0 {
		t.Fatalf("completed=%+v retries=%+v", store.completed, store.retries)
	}
	if summary.Claimed != 1 || summary.Cleaned != 1 || summary.RetryScheduled != 0 || summary.StaleClaims != 0 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestRunnerContinuesBatchAndPersistsBoundedRetryOnPartialDeleteFailure(t *testing.T) {
	claim := testCleanupClaim("media-2")
	store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{{claim}}}
	deleter := &fakeDeleter{failKeys: map[string]error{"stored/large": errors.New("access denied: signed URL must not be persisted")}}
	runner := NewRunner(store, deleter, DefaultConfig())
	runner.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	runner.logf = func(string, ...interface{}) {}

	summary, err := runner.Run(context.Background())
	if !errors.Is(err, ErrRowsRetryScheduled) {
		t.Fatalf("Run() error=%v want ErrRowsRetryScheduled", err)
	}
	if len(deleter.keys) != 4 {
		t.Fatalf("partial failure stopped object cleanup early: keys=%v", deleter.keys)
	}
	if len(store.retries) != 1 || store.retries[0].retryAt.Sub(runner.now()) != DefaultConfig().RetryBase || store.retries[0].reason != postmediaupload.CleanupErrorStorageDeleteFailure {
		t.Fatalf("retry state=%+v", store.retries)
	}
	if len(store.completed) != 0 || summary.Claimed != 1 || summary.Cleaned != 0 || summary.RetryScheduled != 1 {
		t.Fatalf("summary=%+v completed=%+v", summary, store.completed)
	}
}

func TestRunnerIgnoresStaleCompletionAndRetryOwnership(t *testing.T) {
	t.Run("stale success", func(t *testing.T) {
		claim := testCleanupClaim("media-stale-success")
		store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{{claim}}, completeErr: postmediaupload.ErrStaleCleanupClaim}
		deleter := &fakeDeleter{}
		runner := NewRunner(store, deleter, DefaultConfig())
		runner.logf = func(string, ...interface{}) {}
		summary, err := runner.Run(context.Background())
		if err != nil || summary.StaleClaims != 1 || summary.Cleaned != 0 {
			t.Fatalf("summary=%+v err=%v", summary, err)
		}
	})

	t.Run("stale retry", func(t *testing.T) {
		claim := testCleanupClaim("media-stale-retry")
		store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{{claim}}, retryErr: postmediaupload.ErrStaleCleanupClaim}
		deleter := &fakeDeleter{failKeys: map[string]error{"stored/large": errors.New("storage unavailable")}}
		runner := NewRunner(store, deleter, DefaultConfig())
		runner.logf = func(string, ...interface{}) {}
		summary, err := runner.Run(context.Background())
		if err != nil || summary.StaleClaims != 1 || summary.RetryScheduled != 0 {
			t.Fatalf("summary=%+v err=%v", summary, err)
		}
	})
}

func TestRunnerBoundsRowsPerInvocation(t *testing.T) {
	claims := []postmediaupload.CleanupClaim{testCleanupClaim("media-a"), testCleanupClaim("media-b")}
	store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{claims}}
	deleter := &fakeDeleter{}
	cfg := DefaultConfig()
	cfg.BatchSize = 2
	cfg.MaxRowsPerRun = 1
	runner := NewRunner(store, deleter, cfg)
	runner.logf = func(string, ...interface{}) {}

	summary, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Claimed != 1 || summary.Cleaned != 1 || store.claimCalls != 1 || len(store.limits) != 1 || store.limits[0] != 1 {
		t.Fatalf("per-run limit not enforced: summary=%+v claims=%d limits=%v", summary, store.claimCalls, store.limits)
	}
}

func TestMissingMinIOObjectsAreIdempotentSuccess(t *testing.T) {
	if !isMissingObjectError(minio.ErrorResponse{Code: "NoSuchKey"}) || !isMissingObjectError(minio.ErrorResponse{Code: "NoSuchObject"}) {
		t.Fatal("missing-key MinIO responses must be treated as already deleted")
	}
	if isMissingObjectError(minio.ErrorResponse{Code: "NoSuchBucket"}) || isMissingObjectError(errors.New("untyped error")) {
		t.Fatal("bucket and untyped errors must remain failures")
	}
}

func TestMinIONoSuchBucketRemainsFailure(t *testing.T) {
	response := minio.ErrorResponse{Code: "NoSuchBucket"}
	if isMissingObjectError(response) {
		t.Fatal("a missing bucket must not be treated as a missing object")
	}
}

func TestCleanupFailureReasonNeverPersistsRawStorageError(t *testing.T) {
	claim := testCleanupClaim("media-secret")
	store := &fakeStore{batches: [][]postmediaupload.CleanupClaim{{claim}}}
	deleter := &fakeDeleter{failKeys: map[string]error{"stored/original": errors.New("https://private.invalid/signed?credential=secret")}}
	runner := NewRunner(store, deleter, DefaultConfig())
	runner.logf = func(string, ...interface{}) {}
	_, _ = runner.Run(context.Background())
	if len(store.retries) != 1 || strings.Contains(store.retries[0].reason, "private.invalid") || strings.Contains(store.retries[0].reason, "secret") {
		t.Fatalf("unsafe retry reason persisted: %+v", store.retries)
	}
}

func testCleanupClaim(mediaID string) postmediaupload.CleanupClaim {
	return postmediaupload.CleanupClaim{
		MediaID:           mediaID,
		OwnerID:           7,
		OriginalObjectKey: "stored/original",
		MediumObjectKey:   "stored/medium",
		LargeObjectKey:    "stored/large",
		ManifestObjectKey: "stored/manifest",
		ClaimToken:        "550e8400-e29b-41d4-a716-446655440000",
		CleanupAttempts:   1,
	}
}
