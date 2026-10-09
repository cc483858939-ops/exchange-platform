package controllers

import (
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

func TestClassifyPostLikeRecoveryOnlyAllowsNeverManagedZeroBootstrap(t *testing.T) {
	zero, err := classifyPostLikeRecovery(false, nil, postLikeBaseline{})
	if err != nil || !zero.AllowZeroBootstrap || zero.ExpectedVersion != nil {
		t.Fatalf("zero fence=%+v err=%v", zero, err)
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
