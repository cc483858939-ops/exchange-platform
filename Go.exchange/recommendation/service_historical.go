package recommendation

import (
	"context"
	"fmt"
	"log"
	"time"

	"Go.exchange/config"
	"go.opentelemetry.io/otel/attribute"
)

type historicalScanState struct {
	cursor          *HistoricalCursor
	exhausted       bool
	budgetLimited   bool
	capacityLimited bool
	scanned         int
	batches         int
	oldestCreatedAt time.Time
}

type knownPostIDLookup func(context.Context, []uint) (map[uint]struct{}, error)

// supplementRecallTier scans a bounded keyset source until the page is filled,
// the source reaches its end, or the request budget is consumed. Both unseen
// history and soft revisits use the ordinary hydrator, ranker, and selector.
func (service *RecommendationService) supplementRecallTier(
	ctx context.Context,
	request ServeRequest,
	servingVersion string,
	profile *Profile,
	language LanguageContext,
	served ServedHistory,
	loadedAuthors map[uint]struct{},
	rangeScope CandidateRange,
	recentCutoff time.Time,
	softOnly bool,
	startCursor *HistoricalCursor,
	maxScan int,
	maxCandidates int,
	prior []SelectedCandidate,
	extraExcluded map[uint]struct{},
	ranked []RankedCandidate,
	selected []SelectedCandidate,
	knownPostIDs knownPostIDLookup,
) ([]RankedCandidate, []SelectedCandidate, CandidateSetSummary, historicalScanState, error) {
	scanStarted := time.Now()
	state := historicalScanState{cursor: cloneHistoricalCursor(startCursor)}
	summary := CandidateSetSummary{}
	phase := "historical"
	selectionPhase := SelectionPhaseFresh
	if softOnly {
		phase = "soft"
		selectionPhase = SelectionPhaseSoft
	}
	ctx, recallSpan := startRecommendationSpan(ctx, "recommendation.recall", attribute.String("recommendation.phase", phase))
	defer recallSpan.End()
	repository, ok := service.dependencies.Candidates.(HistoricalCandidateRepository)
	if !ok {
		// Test and alternative repositories without history support are treated as
		// an exhausted source; production GormCandidateRepository implements it.
		state.exhausted = true
		return ranked, selected, summary, state, nil
	}
	cfg := service.config.Recommendation
	batchConfig := config.RecommendationHistoricalBackfillConfig{
		BatchSize:         cfg.HistoricalBackfill.BatchSize,
		MaxScanPerRequest: cfg.HistoricalBackfill.MaxScanPerRequest,
	}.Normalized()
	if maxScan < 0 || maxScan > batchConfig.MaxScanPerRequest {
		maxScan = batchConfig.MaxScanPerRequest
	}
	if maxCandidates <= 0 {
		maxCandidates = 500
	}
	cutoff := recentCutoff.UTC()
	if recentCutoff.IsZero() {
		cutoff, _ = config.RecommendationPrimaryWindowBounds(request.Now, cfg.PrimaryWindowDays)
	}
	excluded := make(map[uint]struct{}, len(ranked)+len(selected))
	for _, item := range ranked {
		if item.Post.ID != 0 {
			excluded[item.Post.ID] = struct{}{}
		}
	}
	for _, item := range selected {
		if item.Post.ID != 0 {
			excluded[item.Post.ID] = struct{}{}
		}
	}
	for id := range extraExcluded {
		if id != 0 {
			excluded[id] = struct{}{}
		}
	}
	for state.scanned < maxScan && len(selected) < request.Limit && len(summary.Candidates) < maxCandidates {
		if err := ctx.Err(); err != nil {
			return ranked, selected, summary, state, err
		}
		limit := min(batchConfig.BatchSize, maxCandidates-len(summary.Candidates))
		if remaining := maxScan - state.scanned; limit > remaining {
			limit = remaining
		}
		query := recommendationCandidateQuery(servingVersion, request.Viewer.UserID, valueOrEmptyProfile(profile), served, request.Now, cfg, softOnly)
		query.Range = rangeScope
		query.RecentCutoff = cutoff
		query.HistoricalCursor = cloneHistoricalCursor(state.cursor)
		query.ExcludedPostIDs = clonePostIDSet(excluded)
		query.Limit = limit

		page, err := repository.LoadHistoricalCandidates(ctx, query)
		if err != nil {
			return ranked, selected, summary, state, err
		}
		state.batches++
		scanned := page.Scanned
		if scanned <= 0 && len(page.Candidates) > 0 {
			scanned = len(page.Candidates)
		}
		state.scanned += max(0, scanned)
		cursorAdvanced := false
		if !page.Cursor.CreatedAt.IsZero() && page.Cursor.PostID != 0 {
			if state.cursor != nil && sameHistoricalCursor(*state.cursor, page.Cursor) {
				return ranked, selected, summary, state, fmt.Errorf("historical keyset cursor did not advance")
			}
			state.cursor = &HistoricalCursor{CreatedAt: page.Cursor.CreatedAt.UTC(), PostID: page.Cursor.PostID}
			cursorAdvanced = true
			if state.oldestCreatedAt.IsZero() || state.cursor.CreatedAt.Before(state.oldestCreatedAt) {
				state.oldestCreatedAt = state.cursor.CreatedAt
			}
		}
		if page.Exhausted {
			state.exhausted = true
		}
		if len(page.Candidates) == 0 {
			if !state.exhausted {
				return ranked, selected, summary, state, fmt.Errorf("historical repository returned an empty non-terminal batch")
			}
			break
		}
		if knownPostIDs != nil {
			ids := make([]uint, 0, len(page.Candidates))
			for _, candidate := range page.Candidates {
				ids = append(ids, candidate.PostID)
			}
			known, err := knownPostIDs(ctx, ids)
			if err != nil {
				return ranked, selected, summary, state, err
			}
			filtered := make([]Candidate, 0, len(page.Candidates))
			for _, candidate := range page.Candidates {
				if _, exists := known[candidate.PostID]; exists {
					excluded[candidate.PostID] = struct{}{}
					continue
				}
				filtered = append(filtered, candidate)
			}
			page.Candidates = filtered
			if len(page.Candidates) == 0 && !state.exhausted && !cursorAdvanced {
				return ranked, selected, summary, state, fmt.Errorf("historical repository did not advance beyond snapshot duplicates")
			}
		}
		if len(page.Candidates) == 0 {
			if state.exhausted {
				break
			}
			continue
		}
		for index := range page.Candidates {
			candidate := page.Candidates[index]
			if softOnly {
				page.Candidates[index].RecallTier = RecallTierSoftRevisit
				if item, exists := served[candidate.PostID]; exists {
					page.Candidates[index].WasSoftServed = item.Soft && !item.Hard
					page.Candidates[index].LastServedAt = item.LastServedAt
				}
			} else {
				page.Candidates[index].RecallTier = RecallTierHistoricalUnseen
				page.Candidates[index].FromHistorical = true
			}
			if candidate.PostID != 0 {
				excluded[candidate.PostID] = struct{}{}
			}
		}
		if softOnly {
			summary.SoftCount += len(page.Candidates)
		} else {
			summary.HistoricalCount += len(page.Candidates)
		}
		summary.Candidates = append(summary.Candidates, page.Candidates...)
		for _, candidate := range page.Candidates {
			if candidate.FromFollowing {
				summary.FollowingCount++
			}
		}

		// The page cursor bounds the recall query only. Hydration must reapply
		// eligibility and the historical date scope to every returned ID.
		hydrationQuery := query
		hydrationQuery.HistoricalCursor = nil
		hydrationQuery.ExcludedPostIDs = nil
		hydrated, err := traceHydrateRecommendationCandidates(
			ctx, service.dependencies.Candidates, hydrationQuery, page.Candidates, "historical",
		)
		if err != nil {
			return ranked, selected, summary, state, err
		}
		if profile != nil && request.Viewer.Kind == ViewerAuthenticated {
			if err := loadRecommendationAuthorContext(ctx, service.dependencies.Profiles, request.Viewer.UserID, profile, hydrated, loadedAuthors, cfg, "historical"); err != nil {
				return ranked, selected, summary, state, err
			}
		}
		batchRanked := traceRankRecommendationCandidates(
			ctx, profileFeatures(valueOrEmptyProfile(profile)), hydrated, request.Now,
			rankingConfig(cfg), language, phase,
		)
		if request.Viewer.Kind == ViewerGuest {
			batchRanked = DiversifyPublicCandidates(batchRanked, request.Limit, request.RequestID)
		}
		ranked = append(ranked, batchRanked...)
		selected = traceSelectRecommendationCandidates(ctx, SelectionInput{
			Candidates: batchRanked, Initial: selected, Limit: request.Limit, Now: request.Now,
			Prior: prior, Phase: selectionPhase, RequestID: request.RequestID, Config: selectionConfig(cfg),
		}, phase)
		if state.exhausted {
			break
		}
	}
	if !state.exhausted && state.scanned >= maxScan && len(selected) < request.Limit {
		state.budgetLimited = true
	}
	if !state.exhausted && len(summary.Candidates) >= maxCandidates && len(selected) < request.Limit {
		state.capacityLimited = true
	}
	if state.batches > 0 {
		status := "available"
		if state.exhausted {
			status = "exhausted"
		} else if state.budgetLimited {
			status = "budget_limited"
		}
		oldestAge := time.Duration(0)
		if !state.oldestCreatedAt.IsZero() {
			oldestAge = request.Now.Sub(state.oldestCreatedAt)
			if oldestAge < 0 {
				oldestAge = 0
			}
		}
		candidateCount := summary.HistoricalCount
		if softOnly {
			candidateCount = summary.SoftCount
		}
		service.metrics.AddRecallCandidates(phase, candidateCount)
		if diagnostics, ok := service.metrics.(RecommendationDiagnosticsMetrics); ok {
			diagnostics.RecordHistoricalScan(phase, status, state.scanned, state.batches, oldestAge, time.Since(scanStarted))
		}
		log.Printf("[RecommendationRecall] tier=%s viewer=%s status=%s candidates=%d scanned=%d batches=%d oldest_age_days=%.2f", phase, viewerKindName(request.Viewer.Kind), status, candidateCount, state.scanned, state.batches, oldestAge.Hours()/24)
		_, span := startRecommendationSpan(ctx, "recommendation."+phase+".scan",
			attribute.String("recommendation.viewer_kind", viewerKindName(request.Viewer.Kind)),
			attribute.String("recommendation.status", status),
			attribute.Int("recommendation.candidates", candidateCount),
			attribute.Int("recommendation.scanned", state.scanned),
			attribute.Int("recommendation.batches", state.batches),
			attribute.Float64("recommendation.oldest_age_days", oldestAge.Hours()/24),
		)
		span.End()
	}
	recallSpan.SetAttributes(
		attribute.Int("recommendation.candidate_count", len(summary.Candidates)),
		attribute.Int("recommendation.scanned", state.scanned),
		attribute.Int("recommendation.batches", state.batches),
		attribute.Bool("recommendation.exhausted", state.exhausted),
		attribute.Bool("recommendation.budget_limited", state.budgetLimited || state.capacityLimited),
	)
	return ranked, selected, summary, state, nil
}

func valueOrEmptyProfile(profile *Profile) Profile {
	if profile == nil {
		return Profile{}
	}
	return *profile
}

func cloneHistoricalCursor(cursor *HistoricalCursor) *HistoricalCursor {
	if cursor == nil {
		return nil
	}
	copy := *cursor
	return &copy
}

func sameHistoricalCursor(left, right HistoricalCursor) bool {
	return left.PostID == right.PostID && left.CreatedAt.UTC().Equal(right.CreatedAt.UTC())
}

func clonePostIDSet(ids map[uint]struct{}) map[uint]struct{} {
	if len(ids) == 0 {
		return nil
	}
	copy := make(map[uint]struct{}, len(ids))
	for id := range ids {
		copy[id] = struct{}{}
	}
	return copy
}
