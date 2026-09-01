package controllers

import (
	"errors"
	"testing"

	"Go.exchange/likes"
	"Go.exchange/models"
)

func TestValidatePostLikeBaseline(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		baseline postLikeBaseline
		wantErr  error
	}{
		{
			name:     "clean zero",
			baseline: postLikeBaseline{},
		},
		{
			name: "liked rows converge",
			baseline: postLikeBaseline{
				Count: 2, Version: 8, UserIDs: []uint{11, 12},
				ReactionRowCount: 3, MaxReactionVersion: 8,
			},
		},
		{
			name: "all rows currently unliked",
			baseline: postLikeBaseline{
				Count: 0, Version: 8, ReactionRowCount: 2, MaxReactionVersion: 8,
			},
		},
		{name: "count mismatch", baseline: postLikeBaseline{Count: 1, Version: 8, ReactionRowCount: 1, MaxReactionVersion: 8}, wantErr: likes.ErrLikeProjectionNotReady},
		{name: "post ahead of reactions", baseline: postLikeBaseline{Count: 1, Version: 9, UserIDs: []uint{11}, ReactionRowCount: 1, MaxReactionVersion: 8}, wantErr: likes.ErrLikeProjectionNotReady},
		{name: "reaction ahead of post", baseline: postLikeBaseline{Count: 1, Version: 8, UserIDs: []uint{11}, ReactionRowCount: 1, MaxReactionVersion: 9}, wantErr: likes.ErrLikeProjectionNotReady},
		{name: "zero with reaction history", baseline: postLikeBaseline{Count: 0, Version: 0, ReactionRowCount: 1, MaxReactionVersion: 0}, wantErr: likes.ErrLikeProjectionNotReady},
		{name: "invalid reaction version", baseline: postLikeBaseline{Count: 1, Version: 8, UserIDs: []uint{11}, ReactionRowCount: 1, MaxReactionVersion: 8, invalidReactionVersion: true}, wantErr: likes.ErrLikeProjectionNotReady},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validatePostLikeBaseline(testCase.baseline)
			if testCase.wantErr == nil {
				if err != nil {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error=%v want=%v", err, testCase.wantErr)
			}
		})
	}
}

func TestBuildPostLikeBaselineIncludesUnlikeReactionHistory(t *testing.T) {
	baseline := buildPostLikeBaseline(1, 7, []models.PostReaction{
		{UserID: 11, Reaction: models.PostReactionLike, Liked: true, Version: 7},
		{UserID: 12, Reaction: models.PostReactionLike, Liked: false, Version: 7},
	})

	if baseline.ReactionRowCount != 2 || baseline.MaxReactionVersion != 7 {
		t.Fatalf("baseline history = (%d, %d), want (2, 7)", baseline.ReactionRowCount, baseline.MaxReactionVersion)
	}
	if err := validatePostLikeBaseline(baseline); err != nil {
		t.Fatalf("validatePostLikeBaseline() error = %v", err)
	}
}

func TestClassifyPostLikeRecoveryUsesRegistryAndMarkerFences(t *testing.T) {
	zero, err := classifyPostLikeRecovery(false, nil, postLikeBaseline{})
	if err != nil || !zero.AllowZeroBootstrap || zero.ExpectedVersion != nil {
		t.Fatalf("zero fence=%+v err=%v", zero, err)
	}
	if _, err := classifyPostLikeRecovery(true, nil, postLikeBaseline{}); !errors.Is(err, likes.ErrLikeRecoveryUnsafe) {
		t.Fatalf("managed zero error=%v", err)
	}
	marker := int64(10)
	if _, err := classifyPostLikeRecovery(false, &marker, postLikeBaseline{Count: 1, Version: 10, UserIDs: []uint{11}, ReactionRowCount: 1, MaxReactionVersion: 10}); !errors.Is(err, likes.ErrLikeRecoveryUnsafe) {
		t.Fatalf("unregistered marker error=%v", err)
	}
	fence, err := classifyPostLikeRecovery(true, &marker, postLikeBaseline{Count: 1, Version: 10, UserIDs: []uint{11}, ReactionRowCount: 1, MaxReactionVersion: 10})
	if err != nil || fence.ExpectedVersion == nil || *fence.ExpectedVersion != 10 {
		t.Fatalf("marker fence=%+v err=%v", fence, err)
	}
}
