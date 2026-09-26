package recommendation

import (
	"math"
	"sort"
)

const DefaultFusionRankConstant = 60

// FuseCandidates applies reciprocal-rank fusion to already loaded recall
// lists. Source precedence and DB access stay with the serving caller.
func FuseCandidates(limit int, cfg FusionConfig, lists ...CandidateSet) []Candidate {
	if limit <= 0 {
		return nil
	}
	rankConstant := cfg.RankConstant
	if rankConstant <= 0 {
		rankConstant = DefaultFusionRankConstant
	}

	fused := make([]Candidate, 0)
	byID := make(map[uint]int)
	for _, list := range lists {
		if !candidateSourceSupported(list.Source) {
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
				// Fusion metadata is derived from these recall lists instead of
				// trusting metadata left by a previous candidate pool.
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
			if recordRecallSource(current, list.Source, index+1, candidate.PositiveSemanticSimilarity) {
				current.FusionScore += 1.0 / (float64(rankConstant) + float64(index+1))
			}
		}
	}

	if len(fused) == 0 {
		return nil
	}
	sort.SliceStable(fused, func(i, j int) bool {
		return fusionCandidateBefore(fused[i], fused[j])
	})
	if len(fused) > limit {
		return fused[:limit]
	}
	return fused
}

func MergeCandidates(limit int, sources ...[]Candidate) []Candidate {
	if limit <= 0 {
		return nil
	}
	merged := make([]Candidate, 0, limit)
	byID := make(map[uint]int, limit)
	for _, source := range sources {
		for _, candidate := range source {
			if candidate.PostID == 0 {
				continue
			}
			if index, ok := byID[candidate.PostID]; ok {
				current := &merged[index]
				current.FromSemantic = current.FromSemantic || candidate.FromSemantic
				current.FromFollowing = current.FromFollowing || candidate.FromFollowing
				current.FromRecent = current.FromRecent || candidate.FromRecent
				current.FromTrending = current.FromTrending || candidate.FromTrending
				current.SemanticRank = minNonZeroRank(current.SemanticRank, candidate.SemanticRank)
				current.FollowingRank = minNonZeroRank(current.FollowingRank, candidate.FollowingRank)
				current.RecentRank = minNonZeroRank(current.RecentRank, candidate.RecentRank)
				current.TrendingRank = minNonZeroRank(current.TrendingRank, candidate.TrendingRank)
				if candidate.FusionScore > current.FusionScore {
					current.FusionScore = candidate.FusionScore
				}
				if candidate.SourceCount > current.SourceCount {
					current.SourceCount = candidate.SourceCount
				}
				current.WasSoftServed = current.WasSoftServed || candidate.WasSoftServed
				if current.LastServedAt.IsZero() || (!candidate.LastServedAt.IsZero() && candidate.LastServedAt.Before(current.LastServedAt)) {
					current.LastServedAt = candidate.LastServedAt
				}
				if candidate.FromSemantic {
					current.PositiveSemanticSimilarity = candidate.PositiveSemanticSimilarity
				}
				continue
			}
			if len(merged) >= limit {
				continue
			}
			byID[candidate.PostID] = len(merged)
			merged = append(merged, candidate)
		}
	}
	return merged
}

func SemanticRecallQuota(cap int, recentRatio float64) (int, int) {
	if cap <= 0 {
		return 0, 0
	}
	if cap == 1 {
		return 1, 0
	}
	if recentRatio <= 0 || recentRatio >= 1 {
		recentRatio = 0.80
	}
	recentCap := int(math.Round(float64(cap) * recentRatio))
	if recentCap < 1 {
		recentCap = 1
	}
	if recentCap > cap-1 {
		recentCap = cap - 1
	}
	return recentCap, cap - recentCap
}

func CandidatePostIDs(candidates []Candidate) []uint {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PostID != 0 {
			ids = append(ids, candidate.PostID)
		}
	}
	return ids
}

func candidateSourceSupported(source CandidateSource) bool {
	switch source {
	case CandidateSourceSemantic, CandidateSourceFollowing, CandidateSourceRecent, CandidateSourceTrending:
		return true
	default:
		return false
	}
}

func recordRecallSource(candidate *Candidate, source CandidateSource, rank int, semanticSimilarity float64) bool {
	switch source {
	case CandidateSourceSemantic:
		candidate.FromSemantic = true
		if candidate.SemanticRank != 0 {
			return false
		}
		candidate.SemanticRank = rank
		candidate.PositiveSemanticSimilarity = semanticSimilarity
	case CandidateSourceFollowing:
		candidate.FromFollowing = true
		if candidate.FollowingRank != 0 {
			return false
		}
		candidate.FollowingRank = rank
	case CandidateSourceRecent:
		candidate.FromRecent = true
		if candidate.RecentRank != 0 {
			return false
		}
		candidate.RecentRank = rank
	case CandidateSourceTrending:
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

func fusionCandidateBefore(left, right Candidate) bool {
	if left.FusionScore != right.FusionScore {
		return left.FusionScore > right.FusionScore
	}
	if left.SourceCount != right.SourceCount {
		return left.SourceCount > right.SourceCount
	}
	if leftRank, rightRank := bestRecallRank(left), bestRecallRank(right); leftRank != rightRank {
		return leftRank < rightRank
	}
	return left.PostID > right.PostID
}

func bestRecallRank(candidate Candidate) int {
	best := math.MaxInt
	for _, rank := range []int{candidate.SemanticRank, candidate.FollowingRank, candidate.RecentRank, candidate.TrendingRank} {
		if rank > 0 && rank < best {
			best = rank
		}
	}
	return best
}

func minNonZeroRank(left, right int) int {
	if left == 0 {
		return right
	}
	if right == 0 || left < right {
		return left
	}
	return right
}
