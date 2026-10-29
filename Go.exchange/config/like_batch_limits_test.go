package config

import "testing"

func TestLikeSnapshotRoundLimits(t *testing.T) {
	t.Setenv("LIKE_SNAPSHOT_BATCH_SIZE", "100")
	t.Setenv("LIKE_SNAPSHOT_ROUND_SIZE", "")
	if got := LikeSnapshotRoundSize(); got != 100 {
		t.Fatalf("default=%d", got)
	}
	t.Setenv("LIKE_SNAPSHOT_ROUND_SIZE", "1000")
	if got := LikeSnapshotRoundSize(); got != 1000 {
		t.Fatalf("round=%d", got)
	}
	t.Setenv("LIKE_SNAPSHOT_ROUND_SIZE", "100000")
	if got := LikeSnapshotRoundSize(); got != MaxLikeSnapshotRoundSize {
		t.Fatalf("cap=%d", got)
	}
	t.Setenv("LIKE_SNAPSHOT_ROUND_SIZE", "-1")
	if got := LikeSnapshotRoundSize(); got != 100 {
		t.Fatalf("fallback=%d", got)
	}
	t.Setenv("LIKE_CLAIM_LEASE", "2s")
	t.Setenv("LIKE_SNAPSHOT_ROUND_TIMEOUT", "30s")
	if got := LikeSnapshotRoundTimeout(); got.Seconds() != 1 {
		t.Fatalf("timeout=%v", got)
	}
}

func TestLikeQueueBatchLimits(t *testing.T) {
	t.Setenv("LIKE_SNAPSHOT_BATCH_SIZE", "1000000")
	t.Setenv("LIKE_BEHAVIOR_BATCH_SIZE", "1000000")
	if got := LikeSnapshotBatchSize(); got != MaxLikeSnapshotBatchSize {
		t.Fatalf("snapshot batch=%d want=%d", got, MaxLikeSnapshotBatchSize)
	}
	if got := LikeBehaviorBatchSize(); got != MaxLikeBehaviorBatchSize {
		t.Fatalf("behavior batch=%d want=%d", got, MaxLikeBehaviorBatchSize)
	}
	t.Setenv("LIKE_SNAPSHOT_BATCH_SIZE", "-1")
	t.Setenv("LIKE_BEHAVIOR_BATCH_SIZE", "-1")
	if got := LikeSnapshotBatchSize(); got != MaxLikeSnapshotBatchSize {
		t.Fatalf("invalid snapshot batch=%d want safe default", got)
	}
	if got := LikeBehaviorBatchSize(); got != MaxLikeBehaviorBatchSize {
		t.Fatalf("invalid behavior batch=%d want safe default", got)
	}
}
