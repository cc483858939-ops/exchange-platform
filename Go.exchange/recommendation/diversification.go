package recommendation

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"strconv"
)

const (
	publicDiversificationWindowMultiplier = 4
	publicDiversificationMinWindow        = 60
	publicDiversificationSubsetMultiplier = 2
	publicDiversificationMinSubset        = 40
	PublicDiversificationVersion          = "guest_public_diversification_v1"
)

type publicDiversificationCandidate struct {
	candidate RankedCandidate
	priority  float64
	rank      int
	postID    uint
}

// DiversifyPublicCandidates deterministically samples the already-ranked
// public window without changing candidate scores. RequestID is the explicit
// seed that keeps a page stable while allowing different requests to vary.
func DiversifyPublicCandidates(ranked []RankedCandidate, limit int, requestID string) []RankedCandidate {
	if limit <= 0 || len(ranked) == 0 {
		return nil
	}
	if len(ranked) <= limit*2 {
		return append([]RankedCandidate(nil), ranked...)
	}

	windowSize := limit * publicDiversificationWindowMultiplier
	if windowSize < publicDiversificationMinWindow {
		windowSize = publicDiversificationMinWindow
	}
	if windowSize > len(ranked) {
		windowSize = len(ranked)
	}

	subsetSize := limit * publicDiversificationSubsetMultiplier
	if subsetSize < publicDiversificationMinSubset {
		subsetSize = publicDiversificationMinSubset
	}
	if subsetSize > windowSize {
		subsetSize = windowSize
	}

	eligible := make([]publicDiversificationCandidate, 0, windowSize)
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

		eligible = append(eligible, publicDiversificationCandidate{
			candidate: candidate,
			priority:  publicDiversificationPriority(requestID, postID, rank+1),
			rank:      rank,
			postID:    postID,
		})
	}
	if len(eligible) <= subsetSize {
		result := make([]RankedCandidate, 0, len(eligible))
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

	result := make([]RankedCandidate, 0, subsetSize)
	for _, item := range eligible[:subsetSize] {
		result = append(result, item.candidate)
	}
	return result
}

func publicDiversificationPriority(requestID string, postID uint, rank int) float64 {
	seed := requestID + "|" + PublicDiversificationVersion + "|" + strconv.FormatUint(uint64(postID), 10)
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
