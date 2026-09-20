package controllers

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"strconv"
)

const (
	publicRecommendationDiversificationWindowMultiplier = 4
	publicRecommendationDiversificationMinWindow        = 60
	publicRecommendationDiversificationSubsetMultiplier = 2
	publicRecommendationDiversificationMinSubset        = 40
	publicRecommendationDiversificationVersion          = "guest_public_diversification_v1"
)

type publicRecommendationDiversificationCandidate struct {
	candidate hydratedRecommendationCandidate
	priority  float64
	rank      int
	postID    uint
}

// diversifyPublicRecommendationCandidates narrows a ranked public candidate
// window using deterministic weighted sampling without replacement. The
// returned candidates retain their original recommendation scores; only
// eligibility and order for the existing selector change.
func diversifyPublicRecommendationCandidates(ranked []hydratedRecommendationCandidate, limit int, requestID string) []hydratedRecommendationCandidate {
	if limit <= 0 || len(ranked) == 0 {
		return nil
	}
	if len(ranked) <= limit*2 {
		return append([]hydratedRecommendationCandidate(nil), ranked...)
	}

	windowSize := limit * publicRecommendationDiversificationWindowMultiplier
	if windowSize < publicRecommendationDiversificationMinWindow {
		windowSize = publicRecommendationDiversificationMinWindow
	}
	if windowSize > len(ranked) {
		windowSize = len(ranked)
	}

	subsetSize := limit * publicRecommendationDiversificationSubsetMultiplier
	if subsetSize < publicRecommendationDiversificationMinSubset {
		subsetSize = publicRecommendationDiversificationMinSubset
	}
	if subsetSize > windowSize {
		subsetSize = windowSize
	}

	eligible := make([]publicRecommendationDiversificationCandidate, 0, windowSize)
	seenPostIDs := make(map[uint]struct{}, windowSize)
	for rank, candidate := range ranked[:windowSize] {
		postID := candidate.Post.ID
		if postID == 0 {
			postID = candidate.Candidate.PostID
		}
		if postID == 0 {
			continue
		}
		if _, exists := seenPostIDs[postID]; exists {
			continue
		}
		seenPostIDs[postID] = struct{}{}

		eligible = append(eligible, publicRecommendationDiversificationCandidate{
			candidate: candidate,
			priority:  publicRecommendationDiversificationPriority(requestID, postID, rank+1),
			rank:      rank,
			postID:    postID,
		})
	}
	if len(eligible) <= subsetSize {
		result := make([]hydratedRecommendationCandidate, 0, len(eligible))
		for _, item := range eligible {
			result = append(result, item.candidate)
		}
		return result
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].priority != eligible[j].priority {
			return eligible[i].priority < eligible[j].priority
		}
		if eligible[i].rank != eligible[j].rank {
			return eligible[i].rank < eligible[j].rank
		}
		return eligible[i].postID < eligible[j].postID
	})

	result := make([]hydratedRecommendationCandidate, 0, subsetSize)
	for _, item := range eligible[:subsetSize] {
		result = append(result, item.candidate)
	}
	return result
}

func publicRecommendationDiversificationPriority(requestID string, postID uint, rank int) float64 {
	seed := requestID + "|" + publicRecommendationDiversificationVersion + "|" + strconv.FormatUint(uint64(postID), 10)
	digest := sha256.Sum256([]byte(seed))
	raw := binary.BigEndian.Uint64(digest[:8])
	u := float64(raw) / float64(^uint64(0))
	if u <= 0 {
		u = math.SmallestNonzeroFloat64
	}
	if u > 1 {
		u = 1
	}
	weight := 1 / math.Sqrt(float64(rank))
	return -math.Log(u) / weight
}
