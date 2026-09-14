package tasks

import (
	"errors"
	"testing"

	"Go.exchange/likes"
)

func TestValidateLikeStateMaintenanceBaseline(t *testing.T) {
	if err := validateLikeStateMaintenanceBaseline(likeStateMaintenanceBaseline{}); err != nil {
		t.Fatalf("clean zero error=%v", err)
	}
	if err := validateLikeStateMaintenanceBaseline(likeStateMaintenanceBaseline{
		Count: 0, Version: 4, ReactionRowCount: 2, LikedReactionCount: 0, MaxReactionVersion: 4,
	}); err != nil {
		t.Fatalf("all-unliked baseline error=%v", err)
	}
	for _, baseline := range []likeStateMaintenanceBaseline{
		{Count: 1, Version: 4, ReactionRowCount: 1, LikedReactionCount: 1, MaxReactionVersion: 3},
		{Count: 1, Version: 4, ReactionRowCount: 1, LikedReactionCount: 0, MaxReactionVersion: 4},
		{Count: 0, Version: 0, ReactionRowCount: 1, MaxReactionVersion: 0},
		{Count: 1, Version: 4, ReactionRowCount: 1, LikedReactionCount: 1, MaxReactionVersion: 4, InvalidVersionCount: 1},
	} {
		if err := validateLikeStateMaintenanceBaseline(baseline); !errors.Is(err, likes.ErrLikeProjectionNotReady) {
			t.Fatalf("baseline=%+v error=%v", baseline, err)
		}
	}
}

func TestValidateLikeStateMaintenanceBaselineRejectsAggregateInconsistencies(t *testing.T) {
	for _, baseline := range []likeStateMaintenanceBaseline{
		{Count: 1, Version: 4, ReactionRowCount: 1, LikedReactionCount: 2, MaxReactionVersion: 4},
		{Count: 1, Version: 4, ReactionRowCount: 1, LikedReactionCount: 1, MaxReactionVersion: 4, InvalidVersionCount: 1},
		{Count: -1, Version: 4, ReactionRowCount: 1, LikedReactionCount: 1, MaxReactionVersion: 4},
		{Count: 1, Version: 0, ReactionRowCount: 1, LikedReactionCount: 1, MaxReactionVersion: 0},
	} {
		if err := validateLikeStateMaintenanceBaseline(baseline); !errors.Is(err, likes.ErrLikeProjectionNotReady) {
			t.Fatalf("baseline=%+v error=%v", baseline, err)
		}
	}
}

func TestRegisterWorkerPipelinesIncludesLikeStateMaintenance(t *testing.T) {
	RegisterWorkerPipelines()
	snapshots := pipelineSnapshots()
	if _, ok := snapshots[PipelineLikeStateMaintenance]; !ok {
		t.Fatalf("maintenance pipeline missing from %#v", snapshots)
	}
}
