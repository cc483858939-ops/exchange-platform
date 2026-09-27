package tasks

import (
	"errors"
	"testing"

	"Go.exchange/likes"
)

func TestValidateLikeStateMaintenanceBaseline(t *testing.T) {
	for _, baseline := range []likeStateMaintenanceBaseline{
		{},
		{Count: 0, Version: 10},
		{Count: 2, Version: 0},
		{Count: 2, Version: 10},
	} {
		if err := validateLikeStateMaintenanceBaseline(baseline); err != nil {
			t.Fatalf("baseline=%+v error=%v", baseline, err)
		}
	}
	for _, baseline := range []likeStateMaintenanceBaseline{
		{Count: -1, Version: 0},
		{Count: 0, Version: -1},
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
