package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

type recommendationSelectionMode int

const (
	recommendationSelectionFresh recommendationSelectionMode = iota
	recommendationSelectionSoft
)

type recommendationResultSelectionMode string

const (
	recommendationResultSelectionRanked              recommendationResultSelectionMode = "ranked"
	recommendationResultSelectionExploration         recommendationResultSelectionMode = "exploration"
	recommendationExplorationReasonRecent                                              = "recent"
	recommendationExplorationReasonNovelAuthor                                         = "novel_author"
	recommendationExplorationReasonRecentNovelAuthor                                   = "recent_novel_author"
)

type selectedRecommendation struct {
	Candidate              embeddingCandidate
	Post                   models.Post
	PostArticle            *models.PostArticle
	Embedding              []float32
	Breakdown              recommendationScoreBreakdown
	IsInNetwork            bool
	IsNovelAuthor          bool
	ExplorationOpportunity bool
	SelectionMode          recommendationResultSelectionMode
	ExplorationReason      string
	ExplorationSemantic    float64
}

func balancedPositions(limit, target int) []int {
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

func recommendationExplorationTarget(limit int, cfg config.RecommendationConfig) int {
	if limit <= 0 || cfg.Exploration.Ratio <= 0 {
		return 0
	}
	target := int(math.Round(float64(limit) * cfg.Exploration.Ratio))
	if target > cfg.Exploration.MaxSlots {
		target = cfg.Exploration.MaxSlots
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

func selectRecommendationCandidates(candidates []hydratedRecommendationCandidate, initial []selectedRecommendation, limit int, cfg config.RecommendationConfig, now time.Time, mode recommendationSelectionMode, requestIDs ...string) []selectedRecommendation {
	if limit <= 0 {
		return nil
	}
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = requestIDs[0]
	}
	result := append([]selectedRecommendation(nil), initial...)
	selectedIDs := make(map[uint]struct{}, len(result))
	for _, item := range result {
		selectedIDs[item.Post.ID] = struct{}{}
	}
	outPositions := make(map[int]struct{})
	for _, position := range balancedPositions(limit, int(math.Round(float64(limit)*cfg.OutOfNetworkMinRatio))) {
		outPositions[position] = struct{}{}
	}
	explorationPositionsByPage := make(map[int]struct{})
	if mode == recommendationSelectionFresh {
		for _, position := range explorationPositions(requestID, limit, recommendationExplorationTarget(limit, cfg)) {
			explorationPositionsByPage[position] = struct{}{}
		}
	}

	for len(result) < limit {
		position := len(result) + 1
		onlyFresh := mode == recommendationSelectionFresh
		onlySoft := mode == recommendationSelectionSoft
		available := func(item hydratedRecommendationCandidate) bool {
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
		_, normal, normalOK := chooseNormalRecommendationCandidate(candidates, result, available, outPositions, position, cfg, mode)
		chosen := normal
		ok := normalOK
		selectionOpportunity := false
		selectionMode := recommendationResultSelectionRanked
		selectionReason := ""
		selectionSemantic := 0.0
		opportunity := mode == recommendationSelectionFresh
		if opportunity {
			_, opportunityPosition := explorationPositionsByPage[position]
			opportunity = opportunityPosition
		}
		if opportunity {
			_, strict, strictOK := chooseStrictExplorationCandidate(candidates, result, available, outPositions, position, cfg, now)
			if strictOK && (!normalOK || strict.Post.ID != normal.Post.ID) {
				chosen = strict
				ok = true
				selectionOpportunity = true
				selectionMode = recommendationResultSelectionExploration
				selectionReason = recommendationExplorationReason(strict, now, cfg)
				selectionSemantic = clampUnit(strict.ExplorationSemantic)
			} else if normalOK {
				selectionOpportunity = true
			}
		}
		if !ok {
			break
		}
		selectedIDs[chosen.Post.ID] = struct{}{}
		chosen.Breakdown.FinalScore = chosen.Breakdown.BaseScore - chosen.Breakdown.DiversityPenalty
		result = append(result, selectedRecommendation{
			Candidate: chosen.Candidate, Post: chosen.Post, PostArticle: chosen.PostArticle, Embedding: chosen.Embedding,
			Breakdown: chosen.Breakdown, IsInNetwork: chosen.IsInNetwork, IsNovelAuthor: chosen.IsNovelAuthor,
			ExplorationOpportunity: selectionOpportunity, SelectionMode: selectionMode,
			ExplorationReason: selectionReason, ExplorationSemantic: selectionSemantic,
		})
	}
	return result
}

func chooseNormalRecommendationCandidate(candidates []hydratedRecommendationCandidate, selected []selectedRecommendation, available func(hydratedRecommendationCandidate) bool, outPositions map[int]struct{}, position int, cfg config.RecommendationConfig, mode recommendationSelectionMode) (int, hydratedRecommendationCandidate, bool) {
	preferOutOfNetwork := func(item hydratedRecommendationCandidate) bool {
		if _, ok := outPositions[position]; ok {
			return !item.IsInNetwork
		}
		return true
	}
	_, chosen, ok := chooseRecommendationCandidate(candidates, selected, available, preferOutOfNetwork, cfg, mode, true)
	if ok {
		return 0, chosen, true
	}
	_, chosen, ok = chooseRecommendationCandidate(candidates, selected, available, func(hydratedRecommendationCandidate) bool { return true }, cfg, mode, true)
	if ok {
		return 0, chosen, true
	}
	_, chosen, ok = chooseRecommendationCandidate(candidates, selected, available, preferOutOfNetwork, cfg, mode, false)
	if ok {
		return 0, chosen, true
	}
	_, chosen, ok = chooseRecommendationCandidate(candidates, selected, available, func(hydratedRecommendationCandidate) bool { return true }, cfg, mode, false)
	return 0, chosen, ok
}

func chooseStrictExplorationCandidate(candidates []hydratedRecommendationCandidate, selected []selectedRecommendation, available func(hydratedRecommendationCandidate) bool, outPositions map[int]struct{}, position int, cfg config.RecommendationConfig, now time.Time) (int, hydratedRecommendationCandidate, bool) {
	preferOutOfNetwork := func(item hydratedRecommendationCandidate) bool {
		if _, ok := outPositions[position]; ok {
			return !item.IsInNetwork
		}
		return true
	}
	if _, chosen, ok := chooseStrictExplorationCandidateFrom(candidates, selected, available, preferOutOfNetwork, cfg, now); ok {
		return 0, chosen, true
	}
	return chooseStrictExplorationCandidateFrom(candidates, selected, available, func(hydratedRecommendationCandidate) bool { return true }, cfg, now)
}

func chooseStrictExplorationCandidateFrom(candidates []hydratedRecommendationCandidate, selected []selectedRecommendation, available func(hydratedRecommendationCandidate) bool, preferred func(hydratedRecommendationCandidate) bool, cfg config.RecommendationConfig, now time.Time) (int, hydratedRecommendationCandidate, bool) {
	bestIndex := -1
	var best hydratedRecommendationCandidate
	found := false
	for index, candidate := range candidates {
		if !available(candidate) || !preferred(candidate) || recommendationExplorationReason(candidate, now, cfg) == "" {
			continue
		}
		if recommendationIsSemanticDuplicate(candidate, selected, cfg) || !recommendationAuthorWindowAllows(candidate, selected, cfg) {
			continue
		}
		evaluated := candidate
		evaluated.Breakdown.DiversityPenalty = recommendationDiversityPenalty(evaluated, selected, cfg)
		if !found || recommendationStrictExplorationBefore(evaluated, best, now, cfg) {
			found, bestIndex, best = true, index, evaluated
		}
	}
	return bestIndex, best, found
}

func chooseRecommendationCandidate(candidates []hydratedRecommendationCandidate, selected []selectedRecommendation, available func(hydratedRecommendationCandidate) bool, preferred func(hydratedRecommendationCandidate) bool, cfg config.RecommendationConfig, mode recommendationSelectionMode, enforceAuthorWindow bool) (int, hydratedRecommendationCandidate, bool) {
	bestIndex := -1
	var best hydratedRecommendationCandidate
	found := false
	for index, candidate := range candidates {
		if !available(candidate) || !preferred(candidate) {
			continue
		}
		evaluated := candidate
		evaluated.Breakdown.DiversityPenalty = recommendationDiversityPenalty(evaluated, selected, cfg)
		if enforceAuthorWindow && !recommendationAuthorWindowAllows(evaluated, selected, cfg) {
			continue
		}
		if !found || recommendationSelectionBefore(evaluated, best, mode) {
			found, bestIndex, best = true, index, evaluated
		}
	}
	if found {
		return bestIndex, best, true
	}
	return bestIndex, best, found
}

func recommendationAuthorWindowAllows(candidate hydratedRecommendationCandidate, selected []selectedRecommendation, cfg config.RecommendationConfig) bool {
	if !cfg.Diversity.Enabled || cfg.Diversity.AuthorWindowSize <= 0 || cfg.Diversity.MaxSameAuthorInWindow <= 0 {
		return true
	}
	start := len(selected) - cfg.Diversity.AuthorWindowSize
	if start < 0 {
		start = 0
	}
	count := 0
	for index := start; index < len(selected); index++ {
		if selected[index].Post.AuthorID == candidate.Post.AuthorID {
			count++
		}
	}
	return count < cfg.Diversity.MaxSameAuthorInWindow
}

func recommendationDiversityPenalty(candidate hydratedRecommendationCandidate, selected []selectedRecommendation, cfg config.RecommendationConfig) float64 {
	if recommendationIsSemanticDuplicate(candidate, selected, cfg) {
		return cfg.Diversity.SemanticDuplicatePenalty
	}
	return 0
}

func recommendationIsSemanticDuplicate(candidate hydratedRecommendationCandidate, selected []selectedRecommendation, cfg config.RecommendationConfig) bool {
	if !cfg.Diversity.Enabled || cfg.Diversity.SemanticDuplicateThreshold < -1 || cfg.Diversity.SemanticDuplicateThreshold > 1 || !validEmbeddingVector(candidate.Embedding) || len(selected) == 0 {
		return false
	}
	maxSimilarity := -1.0
	comparable := false
	for _, item := range selected {
		if !validComparableRecommendationEmbedding(item.Embedding, candidate.Embedding) {
			continue
		}
		comparable = true
		maxSimilarity = math.Max(maxSimilarity, cosineSimilarity(candidate.Embedding, item.Embedding))
	}
	return comparable && maxSimilarity >= cfg.Diversity.SemanticDuplicateThreshold
}

func recommendationPostAgeDays(post models.Post, now time.Time) float64 {
	ageDays := now.Sub(recommendationPostTime(post)).Hours() / 24
	if ageDays < 0 {
		return 0
	}
	return ageDays
}

func recommendationExplorationReason(candidate hydratedRecommendationCandidate, now time.Time, cfg config.RecommendationConfig) string {
	ageDays := now.Sub(recommendationPostTimeWithArticle(candidate.Post, candidate.PostArticle)).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	recent := candidate.Candidate.FromRecent && ageDays <= float64(cfg.Exploration.RecentWindowDays)
	novel := candidate.IsNovelAuthor && ageDays <= float64(cfg.Exploration.NovelPostMaxAgeDays)
	if recent && novel {
		return recommendationExplorationReasonRecentNovelAuthor
	}
	if novel {
		return recommendationExplorationReasonNovelAuthor
	}
	if recent {
		return recommendationExplorationReasonRecent
	}
	return ""
}

func recommendationExplorationReasonPriority(reason string) int {
	switch reason {
	case recommendationExplorationReasonRecentNovelAuthor:
		return 3
	case recommendationExplorationReasonNovelAuthor:
		return 2
	case recommendationExplorationReasonRecent:
		return 1
	default:
		return 0
	}
}

func recommendationStrictExplorationBefore(left, right hydratedRecommendationCandidate, now time.Time, cfg config.RecommendationConfig) bool {
	if left.ExplorationSemantic != right.ExplorationSemantic {
		return left.ExplorationSemantic > right.ExplorationSemantic
	}
	leftReason := recommendationExplorationReasonPriority(recommendationExplorationReason(left, now, cfg))
	rightReason := recommendationExplorationReasonPriority(recommendationExplorationReason(right, now, cfg))
	if leftReason != rightReason {
		return leftReason > rightReason
	}
	leftScore := left.Breakdown.BaseScore - left.Breakdown.DiversityPenalty
	rightScore := right.Breakdown.BaseScore - right.Breakdown.DiversityPenalty
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	leftTime := recommendationPostTimeWithArticle(left.Post, left.PostArticle)
	rightTime := recommendationPostTimeWithArticle(right.Post, right.PostArticle)
	if !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	return left.Post.ID > right.Post.ID
}

func recommendationSelectionBefore(left, right hydratedRecommendationCandidate, mode recommendationSelectionMode) bool {
	if mode == recommendationSelectionSoft && !left.Candidate.LastServedAt.Equal(right.Candidate.LastServedAt) {
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
	leftTime := recommendationPostTimeWithArticle(left.Post, left.PostArticle)
	rightTime := recommendationPostTimeWithArticle(right.Post, right.PostArticle)
	if !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	return left.Post.ID > right.Post.ID
}

func selectedRecommendationResponses(selected []selectedRecommendation) []recommendedPostResponse {
	result := make([]recommendedPostResponse, 0, len(selected))
	for _, item := range selected {
		post, err := postResponseFromModel(item.Post, item.PostArticle)
		if err != nil {
			continue
		}
		_ = hydratePostResponseReferences(&post, time.Now().UTC())
		result = append(result, recommendedPostResponse{
			Post: post, Score: item.Breakdown.FinalScore,
		})
	}
	return result
}
