package recommendation

import (
	"testing"
	"time"
)

func TestMergeEmbeddingCandidatesContinuesMetadataAggregationAfterCap(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	merged := MergeCandidates(
		2,
		[]Candidate{{PostID: 1, FromRecent: true}, {PostID: 2, FromFollowing: true}},
		[]Candidate{{PostID: 3, FromTrending: true}, {PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .9, SemanticRank: 4, FusionScore: .3, SourceCount: 2}},
		[]Candidate{{PostID: 2, FromTrending: true, WasSoftServed: true, LastServedAt: now.Add(-time.Hour)}},
	)
	if len(merged) != 2 || merged[0].PostID != 1 || merged[1].PostID != 2 {
		t.Fatalf("merged=%#v, want IDs [1 2]", merged)
	}
	if !merged[0].FromRecent || !merged[0].FromSemantic || merged[0].PositiveSemanticSimilarity != .9 || merged[0].SemanticRank != 4 || merged[0].FusionScore != .3 || merged[0].SourceCount != 2 {
		t.Fatalf("article 1 metadata=%#v", merged[0])
	}
	if !merged[1].FromFollowing || !merged[1].FromTrending || !merged[1].WasSoftServed || !merged[1].LastServedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("article 2 metadata=%#v", merged[1])
	}
}

func TestMergeEmbeddingCandidatesMergesFusionMetadataAsSetMetadata(t *testing.T) {
	merged := MergeCandidates(1,
		[]Candidate{{PostID: 1, SemanticRank: 10, FollowingRank: 4, FusionScore: .2, SourceCount: 3}},
		[]Candidate{{PostID: 1, SemanticRank: 4, FollowingRank: 0, RecentRank: 8, FusionScore: .5, SourceCount: 2}},
	)
	if len(merged) != 1 {
		t.Fatalf("merged=%#v, want one candidate", merged)
	}
	if merged[0].SemanticRank != 4 || merged[0].FollowingRank != 4 || merged[0].RecentRank != 8 || merged[0].FusionScore != .5 || merged[0].SourceCount != 3 {
		t.Fatalf("merged fusion metadata=%#v, want min ranks, max score, max source count", merged[0])
	}
}

func TestMergeEmbeddingCandidatesPreservesSourceFlagsAndCap(t *testing.T) {
	merged := MergeCandidates(4,
		[]Candidate{{PostID: 1, PositiveSemanticSimilarity: .9, FromSemantic: true}, {PostID: 2, FromSemantic: true}},
		[]Candidate{{PostID: 1, FromRecent: true}, {PostID: 3, FromRecent: true}},
		[]Candidate{{PostID: 2, FromTrending: true}},
	)
	if len(merged) != 3 || merged[0].PostID != 1 || !merged[0].FromSemantic || !merged[0].FromRecent || merged[1].PostID != 2 || !merged[1].FromTrending {
		t.Fatalf("merged=%#v", merged)
	}
}
