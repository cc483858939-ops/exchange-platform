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
		Count: 0, Version: 4, ReactionRowCount: 2, MaxReactionVersion: 4,
	}); err != nil {
		t.Fatalf("all-unliked baseline error=%v", err)
	}
	for _, baseline := range []likeStateMaintenanceBaseline{
		{Count: 1, Version: 4, ReactionRowCount: 1, MaxReactionVersion: 3, UserIDs: []uint{11}},
		{Count: 1, Version: 4, ReactionRowCount: 1, MaxReactionVersion: 4},
		{Count: 0, Version: 0, ReactionRowCount: 1},
		{Count: 1, Version: 4, ReactionRowCount: 1, MaxReactionVersion: 4, UserIDs: []uint{11}, invalidVersion: true},
	} {
		if err := validateLikeStateMaintenanceBaseline(baseline); !errors.Is(err, likes.ErrLikeProjectionNotReady) {
			t.Fatalf("baseline=%+v error=%v", baseline, err)
		}
	}
}

func TestSameLikeStateRequiresExactUsersCountVersionAndMembership(t *testing.T) {
	baseline := likeStateMaintenanceBaseline{Count: 2, Version: 8, UserIDs: []uint{11, 12}}
	if !sameLikeState(likes.FullState{Count: 2, Version: 8, UserIDs: []uint{11, 12}}, baseline) {
		t.Fatal("exact state should match")
	}
	for _, state := range []likes.FullState{
		{Count: 1, Version: 8, UserIDs: []uint{11, 12}},
		{Count: 2, Version: 7, UserIDs: []uint{11, 12}},
		{Count: 2, Version: 8, UserIDs: []uint{11, 13}},
		{Count: 2, Version: 8, UserIDs: []uint{11}},
	} {
		if sameLikeState(state, baseline) {
			t.Fatalf("state=%+v must not match", state)
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
