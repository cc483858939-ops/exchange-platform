package recommendation

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"time"
)

func BalancedPositions(limit, target int) []int {
	if limit <= 0 || target <= 0 {
		return nil
	}
	if target >= limit {
		result := make([]int, limit)
		for index := range result {
			result[index] = index + 1
		}
		return result
	}
	result := make([]int, 0, target)
	used := make(map[int]struct{}, target)
	for k := 1; k <= target; k++ {
		position := int(math.Round(float64(k*limit) / float64(target)))
		if position < 1 {
			position = 1
		}
		if position > limit {
			position = limit
		}
		if _, exists := used[position]; exists {
			for distance := 1; distance <= limit; distance++ {
				left, right := position-distance, position+distance
				if left >= 1 {
					if _, exists := used[left]; !exists {
						position = left
						break
					}
				}
				if right <= limit {
					if _, exists := used[right]; !exists {
						position = right
						break
					}
				}
			}
		}
		used[position] = struct{}{}
		result = append(result, position)
	}
	sort.Ints(result)
	return result
}

func ExplorationTarget(limit int, cfg ExplorationConfig) int {
	if limit <= 0 || cfg.Ratio <= 0 {
		return 0
	}
	target := int(math.Round(float64(limit) * cfg.Ratio))
	if target > cfg.MaxSlots {
		target = cfg.MaxSlots
	}
	if target > limit {
		target = limit
	}
	if target < 0 {
		return 0
	}
	return target
}

func explorationPositions(requestID string, limit, target int) []int {
	if limit <= 0 || target <= 0 {
		return nil
	}
	if target > limit {
		target = limit
	}
	type scoredPosition struct {
		position int
		score    string
	}
	positions := make([]scoredPosition, 0, limit)
	for position := 1; position <= limit; position++ {
		if limit > 2 && (position == 1 || position == limit) {
			continue
		}
		sum := sha256.Sum256([]byte(requestID + "|recommendation_exploration_positions_v1|" + strconv.Itoa(position)))
		positions = append(positions, scoredPosition{position: position, score: hex.EncodeToString(sum[:])})
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].score != positions[j].score {
			return positions[i].score < positions[j].score
		}
		return positions[i].position < positions[j].position
	})
	if target > len(positions) {
		remaining := make(map[int]struct{}, len(positions))
		for _, item := range positions {
			remaining[item.position] = struct{}{}
		}
		for position := 1; position <= limit && len(positions) < target; position++ {
			if _, exists := remaining[position]; exists {
				continue
			}
			positions = append(positions, scoredPosition{position: position})
		}
	}
	positions = positions[:target]
	result := make([]int, 0, len(positions))
	for _, item := range positions {
		result = append(result, item.position)
	}
	sort.Ints(result)
	return result
}

func SelectCandidates(input SelectionInput) []SelectedCandidate {
	if input.Limit <= 0 {
		return nil
	}
	result := append([]SelectedCandidate(nil), input.Initial...)
	selectedIDs := make(map[uint]struct{}, len(result))
	for _, item := range result {
		selectedIDs[item.Post.ID] = struct{}{}
	}
	outPositions := make(map[int]struct{})
	for _, position := range BalancedPositions(input.Limit, int(math.Round(float64(input.Limit)*input.Config.OutOfNetworkMinRatio))) {
		outPositions[position] = struct{}{}
	}
	explorationPositionsByPage := make(map[int]struct{})
	if input.Phase == SelectionPhaseFresh {
		for _, position := range explorationPositions(input.RequestID, input.Limit, ExplorationTarget(input.Limit, input.Config.Exploration)) {
			explorationPositionsByPage[position] = struct{}{}
		}
	}

	for len(result) < input.Limit {
		position := len(result) + 1
		onlyFresh := input.Phase == SelectionPhaseFresh
		onlySoft := input.Phase == SelectionPhaseSoft
		available := func(item RankedCandidate) bool {
			if _, exists := selectedIDs[item.Post.ID]; exists {
				return false
			}
			if onlyFresh && item.Candidate.WasSoftServed {
				return false
			}
			if onlySoft && !item.Candidate.WasSoftServed {
				return false
			}
			return true
		}
		_, normal, normalOK := chooseNormalCandidate(input.Candidates, result, available, outPositions, position, input.Config, input.Phase)
		chosen := normal
		ok := normalOK
		selectionOpportunity := false
		selectionMode := SelectionModeRanked
		selectionReason := ExplorationReason("")
		selectionSemantic := 0.0
		opportunity := input.Phase == SelectionPhaseFresh
		if opportunity {
			_, opportunityPosition := explorationPositionsByPage[position]
			opportunity = opportunityPosition
		}
		if opportunity {
			_, strict, strictOK := chooseStrictExplorationCandidateWithConfig(input.Candidates, result, available, outPositions, position, input.Config, input.Now)
			if strictOK && (!normalOK || strict.Post.ID != normal.Post.ID) {
				chosen = strict
				ok = true
				selectionOpportunity = true
				selectionMode = SelectionModeExploration
				selectionReason = explorationReason(strict, input.Now, input.Config.Exploration)
				selectionSemantic = ClampUnit(strict.ExplorationSemantic)
			} else if normalOK {
				selectionOpportunity = true
			}
		}
		if !ok {
			break
		}
		selectedIDs[chosen.Post.ID] = struct{}{}
		chosen.Breakdown.FinalScore = chosen.Breakdown.BaseScore - chosen.Breakdown.DiversityPenalty
		result = append(result, SelectedCandidate{
			Candidate: chosen.Candidate, Post: chosen.Post, Embedding: chosen.Embedding,
			Breakdown: chosen.Breakdown, IsInNetwork: chosen.IsInNetwork, IsNovelAuthor: chosen.IsNovelAuthor,
			ExplorationOpportunity: selectionOpportunity, SelectionMode: selectionMode,
			ExplorationReason: selectionReason, ExplorationSemantic: selectionSemantic,
		})
	}
	return result
}

func chooseNormalCandidate(candidates []RankedCandidate, selected []SelectedCandidate, available func(RankedCandidate) bool, outPositions map[int]struct{}, position int, cfg SelectionConfig, phase SelectionPhase) (int, RankedCandidate, bool) {
	preferOutOfNetwork := func(item RankedCandidate) bool {
		if _, ok := outPositions[position]; ok {
			return !item.IsInNetwork
		}
		return true
	}
	_, chosen, ok := chooseCandidate(candidates, selected, available, preferOutOfNetwork, cfg, phase, true)
	if ok {
		return 0, chosen, true
	}
	_, chosen, ok = chooseCandidate(candidates, selected, available, func(RankedCandidate) bool { return true }, cfg, phase, true)
	if ok {
		return 0, chosen, true
	}
	_, chosen, ok = chooseCandidate(candidates, selected, available, preferOutOfNetwork, cfg, phase, false)
	if ok {
		return 0, chosen, true
	}
	_, chosen, ok = chooseCandidate(candidates, selected, available, func(RankedCandidate) bool { return true }, cfg, phase, false)
	return 0, chosen, ok
}

func chooseStrictExplorationCandidateWithConfig(candidates []RankedCandidate, selected []SelectedCandidate, available func(RankedCandidate) bool, outPositions map[int]struct{}, position int, cfg SelectionConfig, now time.Time) (int, RankedCandidate, bool) {
	preferOutOfNetwork := func(item RankedCandidate) bool {
		if _, ok := outPositions[position]; ok {
			return !item.IsInNetwork
		}
		return true
	}
	if _, chosen, ok := chooseStrictExplorationCandidateFrom(candidates, selected, available, preferOutOfNetwork, cfg, now); ok {
		return 0, chosen, true
	}
	return chooseStrictExplorationCandidateFrom(candidates, selected, available, func(RankedCandidate) bool { return true }, cfg, now)
}

func chooseStrictExplorationCandidateFrom(candidates []RankedCandidate, selected []SelectedCandidate, available func(RankedCandidate) bool, preferred func(RankedCandidate) bool, cfg SelectionConfig, now time.Time) (int, RankedCandidate, bool) {
	bestIndex := -1
	var best RankedCandidate
	found := false
	for index, candidate := range candidates {
		if !available(candidate) || !preferred(candidate) || explorationReason(candidate, now, cfg.Exploration) == "" {
			continue
		}
		if isSemanticDuplicate(candidate, selected, cfg.Diversity) || !authorWindowAllows(candidate, selected, cfg.Diversity) {
			continue
		}
		evaluated := candidate
		evaluated.Breakdown.DiversityPenalty = diversityPenalty(evaluated, selected, cfg.Diversity)
		if !found || strictExplorationBefore(evaluated, best, now, cfg) {
			found, bestIndex, best = true, index, evaluated
		}
	}
	return bestIndex, best, found
}

func chooseCandidate(candidates []RankedCandidate, selected []SelectedCandidate, available func(RankedCandidate) bool, preferred func(RankedCandidate) bool, cfg SelectionConfig, phase SelectionPhase, enforceAuthorWindow bool) (int, RankedCandidate, bool) {
	bestIndex := -1
	var best RankedCandidate
	found := false
	for index, candidate := range candidates {
		if !available(candidate) || !preferred(candidate) {
			continue
		}
		evaluated := candidate
		evaluated.Breakdown.DiversityPenalty = diversityPenalty(evaluated, selected, cfg.Diversity)
		if enforceAuthorWindow && !authorWindowAllows(evaluated, selected, cfg.Diversity) {
			continue
		}
		if !found || selectionBefore(evaluated, best, phase) {
			found, bestIndex, best = true, index, evaluated
		}
	}
	return bestIndex, best, found
}

func authorWindowAllows(candidate RankedCandidate, selected []SelectedCandidate, cfg DiversityConfig) bool {
	if !cfg.Enabled || cfg.AuthorWindowSize <= 0 || cfg.MaxSameAuthorInWindow <= 0 {
		return true
	}
	start := len(selected) - cfg.AuthorWindowSize
	if start < 0 {
		start = 0
	}
	count := 0
	for index := start; index < len(selected); index++ {
		if selected[index].Post.AuthorID == candidate.Post.AuthorID {
			count++
		}
	}
	return count < cfg.MaxSameAuthorInWindow
}

func diversityPenalty(candidate RankedCandidate, selected []SelectedCandidate, cfg DiversityConfig) float64 {
	if isSemanticDuplicate(candidate, selected, cfg) {
		return cfg.SemanticDuplicatePenalty
	}
	return 0
}

func isSemanticDuplicate(candidate RankedCandidate, selected []SelectedCandidate, cfg DiversityConfig) bool {
	if !cfg.Enabled || cfg.SemanticDuplicateThreshold < -1 || cfg.SemanticDuplicateThreshold > 1 || !ValidEmbeddingVector(candidate.Embedding) || len(selected) == 0 {
		return false
	}
	maxSimilarity := -1.0
	comparable := false
	for _, item := range selected {
		if !validComparableEmbedding(item.Embedding, candidate.Embedding) {
			continue
		}
		comparable = true
		maxSimilarity = math.Max(maxSimilarity, CosineSimilarity(candidate.Embedding, item.Embedding))
	}
	return comparable && maxSimilarity >= cfg.SemanticDuplicateThreshold
}

func recommendationPostAgeDays(postAge time.Time, now time.Time) float64 {
	ageDays := now.Sub(postAge).Hours() / 24
	if ageDays < 0 {
		return 0
	}
	return ageDays
}

func explorationReason(candidate RankedCandidate, now time.Time, cfg ExplorationConfig) ExplorationReason {
	ageDays := recommendationPostAgeDays(recommendationPostTime(candidate.Post), now)
	recent := candidate.Candidate.FromRecent && ageDays <= float64(cfg.RecentWindowDays)
	novel := candidate.IsNovelAuthor && ageDays <= float64(cfg.NovelPostMaxAgeDays)
	if recent && novel {
		return ExplorationReasonRecentNovelAuthor
	}
	if novel {
		return ExplorationReasonNovelAuthor
	}
	if recent {
		return ExplorationReasonRecent
	}
	return ""
}

func explorationReasonPriority(reason ExplorationReason) int {
	switch reason {
	case ExplorationReasonRecentNovelAuthor:
		return 3
	case ExplorationReasonNovelAuthor:
		return 2
	case ExplorationReasonRecent:
		return 1
	default:
		return 0
	}
}

func strictExplorationBefore(left, right RankedCandidate, now time.Time, cfg SelectionConfig) bool {
	if left.ExplorationSemantic != right.ExplorationSemantic {
		return left.ExplorationSemantic > right.ExplorationSemantic
	}
	leftReason := explorationReasonPriority(explorationReason(left, now, cfg.Exploration))
	rightReason := explorationReasonPriority(explorationReason(right, now, cfg.Exploration))
	if leftReason != rightReason {
		return leftReason > rightReason
	}
	leftScore := left.Breakdown.BaseScore - left.Breakdown.DiversityPenalty
	rightScore := right.Breakdown.BaseScore - right.Breakdown.DiversityPenalty
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	leftTime := recommendationPostTime(left.Post)
	rightTime := recommendationPostTime(right.Post)
	if !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	return left.Post.ID > right.Post.ID
}

func selectionBefore(left, right RankedCandidate, phase SelectionPhase) bool {
	if phase == SelectionPhaseSoft && !left.Candidate.LastServedAt.Equal(right.Candidate.LastServedAt) {
		if left.Candidate.LastServedAt.IsZero() {
			return false
		}
		if right.Candidate.LastServedAt.IsZero() {
			return true
		}
		return left.Candidate.LastServedAt.Before(right.Candidate.LastServedAt)
	}
	leftScore := left.Breakdown.BaseScore - left.Breakdown.DiversityPenalty
	rightScore := right.Breakdown.BaseScore - right.Breakdown.DiversityPenalty
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	if left.Breakdown.BaseScore != right.Breakdown.BaseScore {
		return left.Breakdown.BaseScore > right.Breakdown.BaseScore
	}
	leftTime := recommendationPostTime(left.Post)
	rightTime := recommendationPostTime(right.Post)
	if !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	return left.Post.ID > right.Post.ID
}

func ExplorationCountsForSelection(selected []SelectedCandidate, target int) SelectionCounts {
	counts := SelectionCounts{Target: target}
	for _, item := range selected {
		if item.ExplorationOpportunity {
			counts.Opportunities++
		}
		if item.SelectionMode == SelectionModeExploration {
			counts.Results++
		}
	}
	return counts
}

func NovelAuthorCount(selected []SelectedCandidate) int {
	count := 0
	for _, item := range selected {
		if item.IsNovelAuthor {
			count++
		}
	}
	return count
}
