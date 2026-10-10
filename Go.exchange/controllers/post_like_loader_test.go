package controllers

import (
	"context"
	"errors"
	"testing"

	"Go.exchange/likes"
)

func TestValidatePostLikeBaselineRejectsInvalidAggregates(t *testing.T) {
	for _, baseline := range []postLikeBaseline{
		{Count: -1},
		{Version: -1},
		{ReactionRowCount: -1},
	} {
		if err := validatePostLikeBaseline(baseline); !errors.Is(err, likes.ErrLikeProjectionNotReady) {
			t.Fatalf("baseline=%+v error=%v want projection-not-ready", baseline, err)
		}
	}
	if err := validatePostLikeBaseline(postLikeBaseline{}); err != nil {
		t.Fatalf("zero aggregate error=%v", err)
	}
}

func TestRequestLikeLoadersDoNotBootstrapMissingStateFromSQL(t *testing.T) {
	oldSet := setPostLikedStateWithRedis
	oldGet := loadPostLikeStateFromRedis
	oldGetMany := loadPostLikeStatesFromRedis
	oldLoadOne := loadPostLikeBaselineFromDB
	oldLoadMany := loadPostLikeBaselinesFromDB
	oldRecoverUser := recoverColdUserLikeState
	t.Cleanup(func() {
		setPostLikedStateWithRedis = oldSet
		loadPostLikeStateFromRedis = oldGet
		loadPostLikeStatesFromRedis = oldGetMany
		loadPostLikeBaselineFromDB = oldLoadOne
		loadPostLikeBaselinesFromDB = oldLoadMany
		recoverColdUserLikeState = oldRecoverUser
	})
	baselineReads := 0
	loadPostLikeBaselineFromDB = func(context.Context, uint) (postLikeBaseline, error) {
		baselineReads++
		return postLikeBaseline{}, nil
	}
	loadPostLikeBaselinesFromDB = func(context.Context, []uint) (map[uint]postLikeBaseline, error) {
		baselineReads++
		return map[uint]postLikeBaseline{}, nil
	}
	recoveryCalls := 0
	recoverColdUserLikeState = func(context.Context, uint) error {
		recoveryCalls++
		return likes.ErrUserLikeNotReady
	}
	setPostLikedStateWithRedis = func(context.Context, uint, uint, bool) (postLikeMutationResult, error) {
		return postLikeMutationResult{}, likes.ErrUserLikeNotReady
	}
	if _, err := setPostLikedStateWithRecovery(t.Context(), 7, 11, true); !errors.Is(err, likes.ErrUserLikeNotReady) {
		t.Fatalf("User NotReady mutation error=%v", err)
	}
	loadPostLikeStateFromRedis = func(context.Context, uint, uint) (postLikeStateResult, error) {
		return postLikeStateResult{}, likes.ErrPostLikeNotReady
	}
	if _, err := loadPostLikeStateWithRecovery(t.Context(), 7, 11); !errors.Is(err, likes.ErrPostLikeNotReady) {
		t.Fatalf("Post NotReady read error=%v", err)
	}
	loadPostLikeStatesFromRedis = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
		return postLikeStatesLoadResult{States: map[uint]postLikeStateResult{}, Unavailable: []uint{11}}, nil
	}
	if result, err := loadPostLikeStatesWithRecovery(t.Context(), 7, []uint{11}); err != nil || len(result.Unavailable) != 1 {
		t.Fatalf("batch result=%+v err=%v", result, err)
	}
	if recoveryCalls != 1 {
		t.Fatalf("request recovery called %d times, want one attempt for User NotReady only", recoveryCalls)
	}
	if baselineReads != 0 {
		t.Fatalf("request loader performed %d SQL recovery baseline reads", baselineReads)
	}
}

func TestClassifyPostLikeRecoveryRejectsUnprovenZeroBootstrap(t *testing.T) {
	if _, err := classifyPostLikeRecovery(false, nil, postLikeBaseline{}); !errors.Is(err, likes.ErrLikeRecoveryUnsafe) {
		t.Fatalf("unproven zero baseline error=%v want unsafe", err)
	}

	marker := int64(10)
	for _, testCase := range []struct {
		name       string
		registered bool
		marker     *int64
		baseline   postLikeBaseline
	}{
		{name: "registered zero", registered: true},
		{name: "recoverable marker", marker: &marker},
		{name: "unregistered zero"},
		{name: "nonzero count", baseline: postLikeBaseline{Count: 1, Version: 1, ReactionRowCount: 1}},
		{name: "nonzero version", baseline: postLikeBaseline{Version: 1}},
		{name: "reaction history", baseline: postLikeBaseline{ReactionRowCount: 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := classifyPostLikeRecovery(testCase.registered, testCase.marker, testCase.baseline); !errors.Is(err, likes.ErrLikeRecoveryUnsafe) {
				t.Fatalf("recovery error=%v want unsafe", err)
			}
		})
	}
}
