package controllers

import (
	"math"
	"sort"
)

const defaultRecommendationFusionRankConstant = 60

type recommendationRecallSource uint8

const (
	recommendationRecallSourceSemantic recommendationRecallSource = iota + 1
	recommendationRecallSourceFollowing
	recommendationRecallSourceRecent
	recommendationRecallSourceTrending
)

type recommendationRecallList struct {
	Source     recommendationRecallSource
	Candidates []embeddingCandidate
}

// fuseRecommendationCandidates aggregates every recall entry before sorting
// and applying the candidate-pool limit. RRF is deliberately isolated from
// hydration, ranking, selection, and served-history handling.
func fuseRecommendationCandidates(limit int, rankConstant int, lists ...recommendationRecallList) []embeddingCandidate {
	if limit <= 0 {
		return nil
	}
	if rankConstant <= 0 {
		rankConstant = defaultRecommendationFusionRankConstant
	}

	fused := make([]embeddingCandidate, 0)
	byID := make(map[uint]int)
	for _, list := range lists {
		if !recommendationRecallSourceSupported(list.Source) {
			continue
		}
		seen := make(map[uint]struct{}, len(list.Candidates))
		for index, candidate := range list.Candidates {
			if candidate.PostID == 0 {
				continue
			}
			if _, exists := seen[candidate.PostID]; exists {
				continue
			}
			seen[candidate.PostID] = struct{}{}

			candidateIndex, exists := byID[candidate.PostID]
			if !exists {
				// Fusion metadata is calculated from the supplied recall lists,
				// rather than trusting metadata from a previous candidate pool.
				candidate.FromSemantic = false
				candidate.FromFollowing = false
				candidate.FromRecent = false
				candidate.FromTrending = false
				candidate.SemanticRank = 0
				candidate.FollowingRank = 0
				candidate.RecentRank = 0
				candidate.TrendingRank = 0
				candidate.FusionScore = 0
				candidate.SourceCount = 0
				candidateIndex = len(fused)
				byID[candidate.PostID] = candidateIndex
				fused = append(fused, candidate)
			}

			current := &fused[candidateIndex]

			if recommendationRecordRecallSource(current, list.Source, index+1, candidate.PositiveSemanticSimilarity) {
				current.FusionScore += 1.0 / (float64(rankConstant) + float64(index+1))
			}
		}
	}

	if len(fused) == 0 {
		return nil
	}
	sort.SliceStable(fused, func(i, j int) bool {
		return recommendationFusionCandidateBefore(fused[i], fused[j])
	})
	if len(fused) > limit {
		return fused[:limit]
	}
	return fused
}

func recommendationFusionCandidateBefore(left, right embeddingCandidate) bool {
	if left.FusionScore != right.FusionScore {
		return left.FusionScore > right.FusionScore
	}
	if left.SourceCount != right.SourceCount {
		return left.SourceCount > right.SourceCount
	}
	if leftRank, rightRank := recommendationBestRecallRank(left), recommendationBestRecallRank(right); leftRank != rightRank {
		return leftRank < rightRank
	}
	return left.PostID > right.PostID
}

func recommendationRecallSourceSupported(source recommendationRecallSource) bool {
	switch source {
	case recommendationRecallSourceSemantic,
		recommendationRecallSourceFollowing,
		recommendationRecallSourceRecent,
		recommendationRecallSourceTrending:
		return true
	default:
		return false
	}
}

func recommendationRecordRecallSource(candidate *embeddingCandidate, source recommendationRecallSource, rank int, semanticSimilarity float64) bool {
	switch source {
	case recommendationRecallSourceSemantic:
		candidate.FromSemantic = true
		if candidate.SemanticRank != 0 {
			return false
		}
		candidate.SemanticRank = rank
		candidate.PositiveSemanticSimilarity = semanticSimilarity
	case recommendationRecallSourceFollowing:
		candidate.FromFollowing = true
		if candidate.FollowingRank != 0 {
			return false
		}
		candidate.FollowingRank = rank
	case recommendationRecallSourceRecent:
		candidate.FromRecent = true
		if candidate.RecentRank != 0 {
			return false
		}
		candidate.RecentRank = rank
	case recommendationRecallSourceTrending:
		candidate.FromTrending = true
		if candidate.TrendingRank != 0 {
			return false
		}
		candidate.TrendingRank = rank
	default:
		return false
	}
	candidate.SourceCount++
	return true
}

func recommendationBestRecallRank(candidate embeddingCandidate) int {
	best := math.MaxInt
	for _, rank := range []int{candidate.SemanticRank, candidate.FollowingRank, candidate.RecentRank, candidate.TrendingRank} {
		if rank > 0 && rank < best {
			best = rank
		}
	}
	return best
}

func recommendationMinNonZeroRank(left, right int) int {
	if left == 0 {
		return right
	}
	if right == 0 || left < right {
		return left
	}
	return right
}
