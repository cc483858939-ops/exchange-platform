package config

import "testing"

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
