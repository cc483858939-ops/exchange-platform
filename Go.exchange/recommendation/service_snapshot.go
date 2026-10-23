package recommendation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const candidateSnapshotStrategyVersion = "tiered-history-keyset-v3"

var traceDropLogCount uint64

func (service *RecommendationService) cursorCodec() (*CursorCodec, error) {
	if !service.config.Recommendation.Snapshot.Enabled || service.dependencies.Snapshots == nil {
		return nil, ErrSnapshotUnavailable
	}
	if len(service.config.SnapshotSigningKey) < 32 {
		return nil, ErrSnapshotUnavailable
	}
	return NewCursorCodec(service.config.SnapshotSigningKey)
}

func (service *RecommendationService) snapshotConfigured() bool {
	if !service.config.Recommendation.Snapshot.Enabled || service.dependencies.Snapshots == nil || len(service.config.SnapshotSigningKey) < 32 {
		return false
	}
	_, ok := service.dependencies.Candidates.(SnapshotCandidateRepository)
	return ok
}

func (service *RecommendationService) createInitialSnapshot(ctx context.Context, request ServeRequest, servingVersion string, result *ServeResult, generationDuration time.Duration) error {
	codec, err := service.cursorCodec()
	if err != nil {
		return err
	}
	if (request.Viewer.Kind != ViewerAuthenticated && request.Viewer.Kind != ViewerGuest) ||
		(request.Viewer.Kind == ViewerGuest && request.Viewer.GuestSessionID == "") {
		return nil
	}
	maxCandidates := service.config.Recommendation.Snapshot.MaxCandidates
	if maxCandidates <= 0 || maxCandidates > 500 {
		maxCandidates = 500
	}
	candidates := make([]CachedCandidate, 0, maxCandidates)
	seen := make(map[uint]struct{}, maxCandidates)
	appendRanked := func(items []RankedCandidate, phase SelectionPhase) {
		for _, item := range items {
			if len(candidates) >= maxCandidates || item.Post.ID == 0 {
				return
			}
			if _, exists := seen[item.Post.ID]; exists {
				continue
			}
			seen[item.Post.ID] = struct{}{}
			candidates = append(candidates, cachedRankedCandidate(item, phase))
		}
	}
	appendRanked(result.freshRankedCandidates, SelectionPhaseFresh)
	appendRanked(result.softRankedCandidates, SelectionPhaseSoft)
	now := request.Now.UTC()
	recentCutoff, _ := config.RecommendationPrimaryWindowBounds(now, service.config.Recommendation.PrimaryWindowDays)
	snapshot := CandidatePoolSnapshot{
		Version: candidatePoolSnapshotVersion, ID: uuid.NewString(),
		OwnerDigest: codec.OwnerDigest(request.Viewer), CreatedAt: now,
		RecentCutoff: recentCutoff,
		ExpiresAt:    now.Add(time.Duration(service.config.Recommendation.Snapshot.TTLSeconds) * time.Second),
		PageSize:     request.Limit, EmbeddingVersion: servingVersion,
		RankerConfigHash:  RankerConfigHash(service.config.Recommendation, servingVersion),
		ProfileComputedAt: result.Profile.ComputedAt.UTC(), LanguageKey: recommendationLanguageKey(request.BrowserLanguage, result.LanguageContext),
		StrategyVersion:             SelectionPolicyVersion + ":" + candidateSnapshotStrategyVersion,
		Candidates:                  candidates,
		HistoricalCursor:            cloneHistoricalCursor(result.historicalCursor),
		HistoricalExhausted:         result.historicalExhausted,
		HistoricalBudgetLimited:     result.historicalBudgetLimited,
		HistoricalCandidatesScanned: result.historicalScanned,
		HistoricalBatches:           result.historicalBatches,
		OldestHistoricalCreatedAt:   result.oldestHistoricalCreatedAt,
		SoftCursor:                  cloneHistoricalCursor(result.softCursor),
		SoftExhausted:               result.softExhausted,
	}
	pageZero := cachedPageFromSelected(0, request.RequestID, now, generationDuration, result.Selected)
	initialPages := []CachedPage{pageZero}
	preselectedPostIDs := make(map[uint]struct{}, maxCandidates)
	for _, item := range pageZero.Items {
		if item.PostID != 0 {
			preselectedPostIDs[item.PostID] = struct{}{}
		}
	}
	contextEntries := appendSnapshotContext(nil, pageZero, snapshotContextWindow(service.config.Recommendation.Diversity))
	embeddings := make(map[uint][]float32, len(result.freshRankedCandidates)+len(result.softRankedCandidates))
	for _, item := range result.freshRankedCandidates {
		if item.Post.ID != 0 && len(item.Embedding) > 0 {
			embeddings[item.Post.ID] = item.Embedding
		}
	}
	for _, item := range result.softRankedCandidates {
		if item.Post.ID != 0 && len(item.Embedding) > 0 {
			embeddings[item.Post.ID] = item.Embedding
		}
	}

	preselect := service.config.Recommendation.Snapshot.PreselectPages
	if preselect < 1 {
		preselect = 1
	}
	for pageIndex := 1; pageIndex < preselect && pageIndex*request.Limit < maxCandidates; pageIndex++ {
		prior := selectedContextFromEntries(contextEntries, embeddings)
		availableFresh := excludePreselectedRankedCandidates(result.freshRankedCandidates, preselectedPostIDs)
		availableSoft := excludePreselectedRankedCandidates(result.softRankedCandidates, preselectedPostIDs)
		pageRequestID := uuid.NewString()
		selected := selectRankedPage(
			availableFresh, availableSoft, prior,
			request.Limit, now, pageRequestID, selectionConfig(service.config.Recommendation),
		)
		// Preselection cannot refill historical or soft candidates. Leave
		// incomplete pages for dynamic generation, which performs the full
		// Recent -> Historical -> Soft fallback sequence.
		if len(selected) < request.Limit {
			break
		}
		page := cachedPageFromSelected(pageIndex, pageRequestID, now, generationDuration, selected)
		initialPages = append(initialPages, page)
		for _, item := range page.Items {
			if item.PostID != 0 {
				preselectedPostIDs[item.PostID] = struct{}{}
			}
		}
		contextEntries = appendSnapshotContext(contextEntries, page, snapshotContextWindow(service.config.Recommendation.Diversity))
	}
	snapshot.PageCount = len(initialPages)
	snapshot.Exhausted = initialSnapshotCanDeclareExhausted(snapshot, initialPages)
	if err := service.dependencies.Snapshots.Create(
		ctx, snapshot, initialPages, contextEntries, time.Duration(service.config.Recommendation.Snapshot.TTLSeconds)*time.Second,
	); err != nil {
		return err
	}
	result.PoolID = snapshot.ID
	result.snapshotPrepared = true
	result.traceGenerationDuration = generationDuration
	result.finalization = &servingFinalization{snapshotID: snapshot.ID, pageIndex: 0, request: request, generationDuration: generationDuration}
	result.Depleted = snapshot.Exhausted
	if result.Depleted {
		result.ContinuationStatus = ContinuationExhausted
	} else if result.historicalBudgetLimited || result.softBudgetLimited {
		result.ContinuationStatus = ContinuationBudgetLimited
	} else {
		result.ContinuationStatus = ContinuationAvailable
	}
	if !result.Depleted {
		result.NextCursor, err = codec.Encode(snapshot.ID, 1, snapshot.PageSize, request.Viewer, snapshot.ExpiresAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *RecommendationService) serveSnapshotCursor(ctx context.Context, request ServeRequest, servingVersion string, served ServedHistory) (ServeResult, ServeRequest, error) {
	codec, err := service.cursorCodec()
	if err != nil {
		return ServeResult{}, request, ErrSnapshotUnavailable
	}
	payload, err := codec.Decode(request.Cursor, request.Viewer, request.Limit, request.Now)
	if err != nil {
		return ServeResult{}, request, err
	}
	snapshot, err := service.dependencies.Snapshots.Load(ctx, payload.SnapshotID)
	if err != nil {
		return ServeResult{}, request, err
	}
	if snapshot.Version != candidatePoolSnapshotVersion || snapshot.OwnerDigest != payload.OwnerDigest || snapshot.PageSize != request.Limit || snapshot.ID != payload.SnapshotID || snapshot.ExpiresAt.UTC().Unix() != payload.ExpiresAt {
		return ServeResult{}, request, ErrInvalidCursor
	}
	if !snapshot.ExpiresAt.After(request.Now) {
		return ServeResult{}, request, ErrCursorExpired
	}
	if snapshot.EmbeddingVersion != servingVersion || snapshot.RankerConfigHash != RankerConfigHash(service.config.Recommendation, servingVersion) ||
		snapshot.StrategyVersion != SelectionPolicyVersion+":"+candidateSnapshotStrategyVersion {
		return ServeResult{}, request, ErrCursorExpired
	}
	profile := Profile{}
	if request.Viewer.Kind == ViewerAuthenticated {
		profile, err = service.loadProfile(ctx, request.Viewer.UserID, servingVersion, request.Now, true)
		if err != nil {
			return ServeResult{}, request, err
		}
	}
	language := BuildLanguageContext(
		request.BrowserLanguage,
		LanguagePrior{ZH: profile.LanguageZHWeight, JA: profile.LanguageJAWeight, EN: profile.LanguageENWeight},
		profile.LanguageEvidence,
		languageConfig(service.config.Recommendation),
	)
	if (request.Viewer.Kind == ViewerAuthenticated && !profile.ComputedAt.UTC().Equal(snapshot.ProfileComputedAt.UTC())) ||
		recommendationLanguageKey(request.BrowserLanguage, language) != snapshot.LanguageKey {
		return ServeResult{}, request, ErrCursorExpired
	}
	if _, ok := service.dependencies.Candidates.(SnapshotCandidateRepository); !ok {
		return ServeResult{}, request, fmt.Errorf("%w: candidate repository cannot revalidate cached candidates", ErrSnapshotUnavailable)
	}
	snapshot, err = service.recoverCommittedSnapshotHistory(ctx, request, snapshot)
	if err != nil {
		return ServeResult{}, request, err
	}

	pageIndex := payload.PageIndex
	for attempt := 0; attempt < 8; attempt++ {
		if err := ctx.Err(); err != nil {
			return ServeResult{}, request, err
		}
		if pageIndex >= defaultSnapshotMaxPages {
			return ServeResult{}, request, ErrSnapshotCapacity
		}
		page, exists, pageErr := service.dependencies.Snapshots.LoadPage(ctx, snapshot.ID, pageIndex)
		if pageErr != nil {
			return ServeResult{}, request, pageErr
		}
		if exists {
			result, pageRequest, materializeErr := service.materializeSnapshotPage(ctx, request, snapshot, page, profile, language, servingVersion)
			return result, pageRequest, materializeErr
		}
		if pageIndex < snapshot.PageCount {
			return ServeResult{}, request, fmt.Errorf("%w: snapshot page index is missing", ErrSnapshotUnavailable)
		}
		if pageIndex > snapshot.PageCount {
			return ServeResult{}, request, ErrInvalidCursor
		}
		updated := cloneSnapshot(snapshot)
		contextEntries, err := service.dependencies.Snapshots.LoadContext(ctx, snapshot.ID, pageIndex)
		if err != nil {
			return ServeResult{}, request, err
		}
		page, err = service.buildSnapshotPage(ctx, request, servingVersion, profile, &updated, pageIndex, served, contextEntries)
		if err != nil {
			return ServeResult{}, request, err
		}
		updated.PageCount = pageIndex + 1
		updated.Exhausted, err = service.snapshotCanDeclareExhausted(ctx, updated, page)
		if err != nil {
			return ServeResult{}, request, err
		}
		updated.Revision = snapshot.Revision + 1
		postStates, err := pageInitialPostStates(page)
		if err != nil {
			return ServeResult{}, request, err
		}
		for _, postID := range updated.DiscardedPostIDs {
			postStates = append(postStates, SnapshotPostStateUpdate{
				PostID: postID, Expected: "", Next: snapshotPostState("invalid", pageIndex),
			})
		}
		updatedContext := appendSnapshotContext(contextEntries, page, snapshotContextWindow(service.config.Recommendation.Diversity))
		committed, err := service.dependencies.Snapshots.Apply(ctx, snapshot.ID, snapshot.Revision, SnapshotMutation{
			Snapshot: updated, Page: &page, PostStates: postStates, Context: &updatedContext,
		})
		if err != nil {
			return ServeResult{}, request, err
		}
		if committed {
			snapshot = updated
			result, pageRequest, materializeErr := service.materializeSnapshotPage(ctx, request, snapshot, page, profile, language, servingVersion)
			return result, pageRequest, materializeErr
		}
		snapshot, err = service.dependencies.Snapshots.Load(ctx, payload.SnapshotID)
		if err != nil {
			return ServeResult{}, request, err
		}
	}
	return ServeResult{}, request, fmt.Errorf("%w: snapshot page compare-and-swap contention", ErrSnapshotUnavailable)
}

func (service *RecommendationService) buildSnapshotPage(ctx context.Context, request ServeRequest, servingVersion string, profile Profile, snapshot *CandidatePoolSnapshot, pageIndex int, served ServedHistory, contextEntries []SnapshotContextEntry) (CachedPage, error) {
	if snapshot.Exhausted {
		return CachedPage{Index: pageIndex, RequestID: uuid.NewString(), ServedAt: request.Now}, nil
	}
	requestID := uuid.NewString()
	start := time.Now()
	page, err := service.selectSnapshotPage(ctx, request, servingVersion, profile, snapshot, pageIndex, requestID, served, contextEntries)
	if err != nil {
		return CachedPage{}, err
	}
	page.GenerationLatencyMS = time.Since(start).Milliseconds()
	return page, nil
}

func (service *RecommendationService) selectSnapshotPage(ctx context.Context, request ServeRequest, servingVersion string, profile Profile, snapshot *CandidatePoolSnapshot, pageIndex int, requestID string, served ServedHistory, contextEntries []SnapshotContextEntry) (CachedPage, error) {
	candidateIDs := make([]uint, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		candidateIDs = append(candidateIDs, candidate.PostID)
	}
	used, err := service.dependencies.Snapshots.KnownPostIDs(ctx, snapshot.ID, candidateIDs)
	if err != nil {
		return CachedPage{}, err
	}
	poolCandidateIDs := snapshotCandidateIDSet(snapshot, true)
	remaining := make([]CachedCandidate, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		if _, exists := used[candidate.PostID]; !exists {
			remaining = append(remaining, candidate)
		}
	}
	cacheByID := make(map[uint]CachedCandidate, len(remaining))
	candidates := make([]Candidate, 0, len(remaining))
	for _, cached := range remaining {
		cacheByID[cached.PostID] = cached
		candidates = append(candidates, cached.Candidate)
	}
	var eligible []RankedCandidate
	var prior []SelectedCandidate
	if repository, ok := service.dependencies.Candidates.(SnapshotPageRepository); ok {
		contextIDs := selectedSnapshotContextIDs(contextEntries, pageIndex)
		query := recommendationSnapshotCandidateQuery(servingVersion, request.Viewer.UserID, profile, request.Now, service.config.Recommendation)
		var contextEmbeddings map[uint][]float32
		eligible, contextEmbeddings, err = repository.LoadSnapshotPage(ctx, query, candidates, contextIDs)
		if err != nil {
			return CachedPage{}, err
		}
		prior = selectedContextFromEntries(contextEntries, contextEmbeddings)
	} else {
		eligible, err = service.loadEligibleSnapshotCandidates(ctx, request, servingVersion, profile, candidates)
		if err != nil {
			return CachedPage{}, err
		}
		prior, err = service.selectedSnapshotContext(ctx, contextEntries, pageIndex, servingVersion)
		if err != nil {
			return CachedPage{}, err
		}
	}
	ranked := make([]RankedCandidate, 0, len(eligible))
	eligibleIDs := make(map[uint]struct{}, len(eligible))
	for _, hydrated := range eligible {
		cached, exists := cacheByID[hydrated.Post.ID]
		if !exists {
			continue
		}
		hydrated.Candidate = cached.Candidate
		hydrated.Breakdown = cached.Breakdown
		hydrated.ExplorationSemantic = cached.ExplorationSemantic
		hydrated.IsInNetwork = cached.IsInNetwork
		hydrated.IsNovelAuthor = cached.IsNovelAuthor
		hydrated = refreshSnapshotSemanticScores(hydrated, profileFeatures(profile), rankingConfig(service.config.Recommendation))
		ranked = append(ranked, hydrated)
		eligibleIDs[hydrated.Post.ID] = struct{}{}
	}
	// Drop posts that have become ineligible so later pages do not keep
	// checking the same stale IDs from this pool.
	remainingCandidates := make([]CachedCandidate, 0, len(remaining))
	for _, cached := range remaining {
		if _, exists := eligibleIDs[cached.PostID]; exists {
			remainingCandidates = append(remainingCandidates, cached)
		} else {
			snapshot.DiscardedPostIDs = append(snapshot.DiscardedPostIDs, cached.PostID)
		}
	}
	snapshot.Candidates = remainingCandidates
	fresh := make([]RankedCandidate, 0, len(ranked))
	soft := make([]RankedCandidate, 0)
	for _, item := range ranked {
		if cacheByID[item.Post.ID].Phase == SelectionPhaseSoft {
			soft = append(soft, item)
		} else {
			fresh = append(fresh, item)
		}
	}
	selected := selectRankedPage(fresh, nil, prior, snapshot.PageSize, request.Now, requestID, selectionConfig(service.config.Recommendation))
	maxPool := candidatePoolLimit(service.config.Recommendation.Snapshot.MaxCandidates)
	maxScan := service.config.Recommendation.HistoricalBackfill.Normalized().MaxScanPerRequest
	snapshot.HistoricalBudgetLimited = false
	usedCandidateIDs := poolCandidateIDs
	knownPostIDs := func(lookupCtx context.Context, ids []uint) (map[uint]struct{}, error) {
		return service.dependencies.Snapshots.KnownPostIDs(lookupCtx, snapshot.ID, ids)
	}
	historicalState := historicalScanState{cursor: cloneHistoricalCursor(snapshot.HistoricalCursor), exhausted: snapshot.HistoricalExhausted}
	if len(selected) < snapshot.PageSize && !snapshot.HistoricalExhausted {
		remainingCapacity := maxPool - len(snapshot.Candidates)
		if remainingCapacity > 0 {
			initialRankedCount := len(fresh)
			extended, nextSelected, _, state, supplementErr := service.supplementRecallTier(
				ctx, request, servingVersion, profilePointer(profile, request.Viewer.Kind),
				BuildLanguageContext(request.BrowserLanguage, LanguagePrior{ZH: profile.LanguageZHWeight, JA: profile.LanguageJAWeight, EN: profile.LanguageENWeight}, profile.LanguageEvidence, languageConfig(service.config.Recommendation)),
				served, nil, CandidateRangeHistorical, snapshot.RecentCutoff, false,
				snapshot.HistoricalCursor, maxScan, remainingCapacity, prior, usedCandidateIDs, fresh, selected, knownPostIDs,
			)
			if supplementErr != nil {
				return CachedPage{}, supplementErr
			}
			fresh = extended
			selected = nextSelected
			historicalState = state
			snapshot.HistoricalCursor = cloneHistoricalCursor(state.cursor)
			snapshot.HistoricalExhausted = state.exhausted
			snapshot.HistoricalBudgetLimited = state.budgetLimited
			snapshot.HistoricalCandidatesScanned += state.scanned
			snapshot.HistoricalBatches += state.batches
			if !state.oldestCreatedAt.IsZero() && (snapshot.OldestHistoricalCreatedAt.IsZero() || state.oldestCreatedAt.Before(snapshot.OldestHistoricalCreatedAt)) {
				snapshot.OldestHistoricalCreatedAt = state.oldestCreatedAt
			}
			for _, item := range fresh[initialRankedCount:] {
				if item.Post.ID == 0 {
					continue
				}
				cached := cachedRankedCandidate(item, SelectionPhaseFresh)
				cacheByID[cached.PostID] = cached
				snapshot.Candidates = append(snapshot.Candidates, cached)
			}
		} else {
			snapshot.HistoricalBudgetLimited = true
		}
	}
	if len(selected) < snapshot.PageSize && snapshot.HistoricalExhausted {
		selected = selectRankedPage(fresh, soft, prior, snapshot.PageSize, request.Now, requestID, selectionConfig(service.config.Recommendation))
		if len(selected) < snapshot.PageSize && !snapshot.SoftExhausted {
			remainingCapacity := maxPool - len(snapshot.Candidates)
			softBudget := maxScan - historicalState.scanned
			if softBudget < 0 {
				softBudget = 0
			}
			if remainingCapacity > 0 {
				extended, nextSelected, _, state, supplementErr := service.supplementRecallTier(
					ctx, request, servingVersion, profilePointer(profile, request.Viewer.Kind),
					BuildLanguageContext(request.BrowserLanguage, LanguagePrior{ZH: profile.LanguageZHWeight, JA: profile.LanguageJAWeight, EN: profile.LanguageENWeight}, profile.LanguageEvidence, languageConfig(service.config.Recommendation)),
					served, nil, CandidateRangeAny, snapshot.RecentCutoff, true,
					snapshot.SoftCursor, softBudget, remainingCapacity, prior, poolCandidateIDs, nil, selected, knownPostIDs,
				)
				if supplementErr != nil {
					return CachedPage{}, supplementErr
				}
				soft = append(soft, extended...)
				selected = nextSelected
				snapshot.SoftCursor = cloneHistoricalCursor(state.cursor)
				snapshot.SoftExhausted = state.exhausted
				snapshot.HistoricalBudgetLimited = snapshot.HistoricalBudgetLimited || state.budgetLimited
				for _, item := range extended {
					if item.Post.ID == 0 {
						continue
					}
					cached := cachedRankedCandidate(item, SelectionPhaseSoft)
					cacheByID[cached.PostID] = cached
					snapshot.Candidates = append(snapshot.Candidates, cached)
				}
			} else {
				snapshot.HistoricalBudgetLimited = true
			}
		}
	}
	if snapshot.HistoricalExhausted {
		selected = selectRankedPage(fresh, soft, prior, snapshot.PageSize, request.Now, requestID, selectionConfig(service.config.Recommendation))
	}
	return cachedPageFromSelected(pageIndex, requestID, request.Now, 0, selected), nil
}

func profilePointer(profile Profile, kind ViewerKind) *Profile {
	if kind != ViewerAuthenticated {
		return nil
	}
	copy := profile
	return &copy
}

func candidatePoolLimit(limit int) int {
	if limit <= 0 || limit > 500 {
		return 500
	}
	return limit
}

func snapshotCandidateIDSet(snapshot *CandidatePoolSnapshot, includeAll bool) map[uint]struct{} {
	ids := make(map[uint]struct{}, len(snapshot.Candidates))
	if includeAll {
		for _, item := range snapshot.Candidates {
			ids[item.PostID] = struct{}{}
		}
	}
	return ids
}

func initialSnapshotCanDeclareExhausted(snapshot CandidatePoolSnapshot, pages []CachedPage) bool {
	if !snapshot.HistoricalExhausted || !snapshot.SoftExhausted || len(pages) > 1 {
		return false
	}
	reserved := make(map[uint]struct{})
	for _, page := range pages {
		for _, id := range cachedPagePostIDs(page) {
			reserved[id] = struct{}{}
		}
	}
	for _, candidate := range snapshot.Candidates {
		if _, exists := reserved[candidate.PostID]; !exists {
			return false
		}
	}
	return true
}

func (service *RecommendationService) snapshotCanDeclareExhausted(ctx context.Context, snapshot CandidatePoolSnapshot, page CachedPage) (bool, error) {
	if !snapshot.HistoricalExhausted || !snapshot.SoftExhausted || snapshot.PageCount > page.Index+1 {
		return false, nil
	}
	ids := make([]uint, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		ids = append(ids, candidate.PostID)
	}
	known, err := service.dependencies.Snapshots.KnownPostIDs(ctx, snapshot.ID, ids)
	if err != nil {
		return false, err
	}
	selected := make(map[uint]struct{}, len(page.Items))
	for _, item := range page.Items {
		selected[item.PostID] = struct{}{}
	}
	for _, candidate := range snapshot.Candidates {
		if _, exists := known[candidate.PostID]; exists {
			continue
		}
		if _, selectedNow := selected[candidate.PostID]; !selectedNow {
			return false, nil
		}
	}
	return true, nil
}

func (service *RecommendationService) materializeSnapshotPage(ctx context.Context, request ServeRequest, snapshot CandidatePoolSnapshot, page CachedPage, profile Profile, language LanguageContext, servingVersion string) (ServeResult, ServeRequest, error) {
	eligibilityRequest := request
	request.RequestID = page.RequestID
	if !page.ServedAt.IsZero() {
		request.Now = page.ServedAt.UTC()
	}
	pageItems := page.Items
	if snapshotPageCommitted(page) {
		pageItems = pageContextItems(page)
	}
	wanted := make(map[uint]struct{}, len(pageItems))
	for _, item := range pageItems {
		wanted[item.PostID] = struct{}{}
	}
	inputs := make([]Candidate, 0, len(pageItems))
	for _, item := range pageItems {
		if _, exists := wanted[item.PostID]; !exists {
			continue
		}
		inputs = append(inputs, item.Candidate)
	}
	eligible, err := service.loadEligibleSnapshotCandidates(ctx, eligibilityRequest, servingVersion, profile, inputs)
	if err != nil {
		return ServeResult{}, request, err
	}
	byID := make(map[uint]RankedCandidate, len(eligible))
	for _, item := range eligible {
		byID[item.Post.ID] = item
	}
	selected := make([]SelectedCandidate, 0, len(pageItems))
	for _, cached := range pageItems {
		if _, exists := wanted[cached.PostID]; !exists {
			continue
		}
		item, exists := byID[cached.PostID]
		if !exists {
			continue
		}
		item.Candidate = cached.Candidate
		item.Breakdown = cached.Breakdown
		item.ExplorationSemantic = cached.ExplorationSemantic
		item.IsInNetwork = cached.IsInNetwork
		item.IsNovelAuthor = cached.IsNovelAuthor
		item = refreshSnapshotSemanticScores(item, profileFeatures(profile), rankingConfig(service.config.Recommendation))
		selected = append(selected, SelectedCandidate{
			Candidate: item.Candidate, Post: item.Post, Embedding: item.Embedding,
			Breakdown: item.Breakdown, IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor,
			ExplorationOpportunity: cached.ExplorationOpportunity, SelectionMode: cached.SelectionMode,
			ExplorationReason: cached.ExplorationReason, ExplorationSemantic: item.ExplorationSemantic,
		})
	}
	pageDepleted := snapshot.Exhausted
	result := ServeResult{
		Viewer: request.Viewer, RequestID: request.RequestID, Now: request.Now, Limit: request.Limit,
		EmbeddingVersion: servingVersion, Profile: profile, LanguageContext: language,
		CandidateSummary:      candidateSummaryFromSnapshot(snapshot.Candidates, false),
		FreshCandidateSummary: candidateSummaryFromSnapshot(snapshot.Candidates, true),
		Selected:              selected, StrategyID: serviceStrategyFor(request.Viewer, profile),
		RankerConfigHash:    snapshot.RankerConfigHash,
		PersonalizationMode: personalizationMode(profile, 0),
		Depleted:            pageDepleted,
		PoolID:              snapshot.ID, CacheHit: true, snapshotPrepared: true,
		traceGenerationDuration: time.Duration(page.GenerationLatencyMS) * time.Millisecond,
		finalization:            &servingFinalization{snapshotID: snapshot.ID, pageIndex: page.Index, request: request, generationDuration: time.Duration(page.GenerationLatencyMS) * time.Millisecond},
	}
	if result.Depleted {
		result.ContinuationStatus = ContinuationExhausted
	} else if snapshot.HistoricalBudgetLimited {
		result.ContinuationStatus = ContinuationBudgetLimited
	} else {
		result.ContinuationStatus = ContinuationAvailable
	}
	result.FallbackReason = fallbackReason(profile.PositiveSignalCount, selected, request.Limit, result.ContinuationStatus)
	result.OnboardingInterestActive = result.CandidateSummary.OnboardingInterestActive
	result.OnboardingInterestCandidateCount = result.CandidateSummary.OnboardingInterestCandidateCount
	codec, codecErr := service.cursorCodec()
	if codecErr == nil && !result.Depleted {
		if cursor, encodeErr := codec.Encode(snapshot.ID, page.Index+1, snapshot.PageSize, request.Viewer, snapshot.ExpiresAt); encodeErr == nil {
			result.NextCursor = cursor
		}
	}
	return result, request, nil
}

func (service *RecommendationService) loadEligibleSnapshotCandidates(ctx context.Context, request ServeRequest, servingVersion string, profile Profile, candidates []Candidate) ([]RankedCandidate, error) {
	repository, ok := service.dependencies.Candidates.(SnapshotCandidateRepository)
	if !ok {
		return nil, ErrSnapshotUnavailable
	}
	query := recommendationSnapshotCandidateQuery(servingVersion, request.Viewer.UserID, profile, request.Now, service.config.Recommendation)
	return repository.LoadEligibleSnapshotCandidates(ctx, query, candidates)
}

func (service *RecommendationService) FinalizeServing(ctx context.Context, result ServeResult, returned []SelectedCandidate) error {
	_, err := service.FinalizeMappedServing(ctx, result, returned)
	return err
}

func (service *RecommendationService) FinalizeMappedServing(ctx context.Context, result ServeResult, returned []SelectedCandidate) ([]TrackingFact, error) {
	if ctx == nil {
		return nil, errors.New("recommendation finalization context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	meta := result.finalization
	if meta == nil {
		return result.Tracking, nil
	}
	if meta.traceContext != nil {
		ctx = trace.ContextWithSpan(ctx, trace.SpanFromContext(meta.traceContext))
	}
	selected := uniqueSelected(returned)
	request := meta.request
	request.RequestID = result.RequestID
	request.Now = result.Now.UTC()
	finalResult := result
	finalResult.RequestID = request.RequestID
	finalResult.Now = request.Now
	finalResult.Selected = selected

	if meta.snapshotID != "" && meta.pageIndex >= 0 {
		mapperRejectedIDs := selectedIDsNotIn(selectedPostIDs(result.Selected), selectedPostIDs(selected))
		page, err := service.commitMappedPage(ctx, meta.snapshotID, meta.pageIndex, selected, mapperRejectedIDs)
		if err != nil {
			return nil, err
		}
		request.RequestID = page.RequestID
		if !page.ServedAt.IsZero() {
			request.Now = page.ServedAt.UTC()
		}
		finalResult.RequestID = request.RequestID
		finalResult.Now = request.Now

		historyReady := page.HistoryRecorded
		if !historyReady {
			if err := service.recordCommittedPageHistory(ctx, request, meta.snapshotID, page); err != nil {
				log.Printf("[Recommendation] committed page history recovery pending for snapshot %s page %d: %v", meta.snapshotID, meta.pageIndex, err)
			} else {
				page, _, err = service.updateSnapshotPage(ctx, meta.snapshotID, meta.pageIndex,
					func(current CachedPage) bool { return current.HistoryRecorded },
					func(current *CachedPage) { current.HistoryRecorded = true; current.Finalized = current.TraceAttempted },
				)
				if err != nil {
					log.Printf("[Recommendation] committed page history receipt update pending for snapshot %s page %d: %v", meta.snapshotID, meta.pageIndex, err)
				} else {
					historyReady = true
				}
			}
		}

		if historyReady {
			page, enqueueTrace, err := service.updateSnapshotPage(ctx, meta.snapshotID, meta.pageIndex,
				func(current CachedPage) bool { return current.TraceAttempted },
				func(current *CachedPage) { current.TraceAttempted = true; current.Finalized = current.HistoryRecorded },
			)
			if err != nil {
				log.Printf("[RecommendationTrace] committed page trace claim pending for snapshot %s page %d: %v", meta.snapshotID, meta.pageIndex, err)
			} else if enqueueTrace {
				finalResult.RequestID = page.RequestID
				service.enqueueServingTraceBestEffort(ctx, request, finalResult, meta.generationDuration)
			}
		}
	} else {
		service.recordHistory(ctx, request, selected)
		service.enqueueServingTraceBestEffort(ctx, request, finalResult, meta.generationDuration)
	}
	var tracking []TrackingFact
	if request.Viewer.Kind == ViewerAuthenticated {
		var trackingErr error
		tracking, trackingErr = BuildTrackingFacts(finalResult, service.config.Tracking)
		if trackingErr != nil {
			log.Printf("[RecommendationTelemetry] omit tracking metadata: %v", trackingErr)
			tracking = nil
		}
		service.metrics.AddTrackingResults("tracked", len(tracking))
		service.metrics.AddTrackingResults("untracked", len(selected)-len(tracking))
	}
	service.metrics.ObserveResultCount(len(selected))
	service.recordResultMetrics(selected)
	return tracking, nil
}

func (service *RecommendationService) commitMappedPage(ctx context.Context, snapshotID string, pageIndex int, returned []SelectedCandidate, mapperRejectedIDs []uint) (CachedPage, error) {
	returnedIDs := selectedPostIDs(returned)
	for attempt := 0; attempt < 8; attempt++ {
		snapshot, err := service.dependencies.Snapshots.Load(ctx, snapshotID)
		if err != nil {
			return CachedPage{}, err
		}
		page, exists, err := service.dependencies.Snapshots.LoadPage(ctx, snapshotID, pageIndex)
		if err != nil {
			return CachedPage{}, err
		}
		if !exists {
			return CachedPage{}, ErrSnapshotUnavailable
		}
		if snapshotPageCommitted(page) {
			if !samePostIDs(returnedIDs, page.ReturnedPostIDs) {
				return CachedPage{}, fmt.Errorf("%w: committed recommendation page content changed", ErrCursorExpired)
			}
			return page, nil
		}
		allowed := make(map[uint]struct{}, len(page.Items))
		for _, item := range page.Items {
			allowed[item.PostID] = struct{}{}
		}
		for _, id := range returnedIDs {
			if _, exists := allowed[id]; !exists {
				return CachedPage{}, fmt.Errorf("%w: mapped page contains post outside the prepared page", ErrSnapshotUnavailable)
			}
		}
		preparedIDs := cachedPagePostIDs(page)
		changed := !samePostIDs(preparedIDs, returnedIDs)
		laterPages := make([]CachedPage, 0)
		if changed {
			for laterIndex := pageIndex + 1; laterIndex < snapshot.PageCount; laterIndex++ {
				later, found, loadErr := service.dependencies.Snapshots.LoadPage(ctx, snapshotID, laterIndex)
				if loadErr != nil {
					return CachedPage{}, loadErr
				}
				if !found {
					return CachedPage{}, fmt.Errorf("%w: prepared later page is missing", ErrSnapshotUnavailable)
				}
				if snapshotPageCommitted(later) {
					return CachedPage{}, fmt.Errorf("%w: a later recommendation page is already committed", ErrCursorExpired)
				}
				laterPages = append(laterPages, later)
			}
		}
		page.Committed = true
		page.ReturnedPostIDs = append([]uint(nil), returnedIDs...)
		updated := cloneSnapshot(snapshot)
		newlyRejectedCount := 0
		returnedSet := make(map[uint]struct{}, len(returnedIDs))
		mapperRejectedSet := make(map[uint]struct{}, len(mapperRejectedIDs))
		for _, id := range returnedIDs {
			returnedSet[id] = struct{}{}
		}
		for _, id := range mapperRejectedIDs {
			mapperRejectedSet[id] = struct{}{}
		}
		postStates := make([]SnapshotPostStateUpdate, 0, len(preparedIDs))
		releasedCandidates := make([]CachedCandidate, 0)
		for _, item := range page.Items {
			if _, returned := returnedSet[item.PostID]; returned {
				postStates = append(postStates, SnapshotPostStateUpdate{PostID: item.PostID, Expected: snapshotPostState("reserved", pageIndex), Next: snapshotPostState("used", pageIndex)})
				continue
			}
			if _, rejected := mapperRejectedSet[item.PostID]; rejected {
				postStates = append(postStates, SnapshotPostStateUpdate{PostID: item.PostID, Expected: snapshotPostState("reserved", pageIndex), Next: snapshotPostState("rejected", pageIndex)})
				newlyRejectedCount++
				continue
			}
			postStates = append(postStates, SnapshotPostStateUpdate{PostID: item.PostID, Expected: snapshotPostState("reserved", pageIndex)})
			releasedCandidates = append(releasedCandidates, item)
		}
		deletePageIndices := make([]int, 0, len(laterPages))
		for _, later := range laterPages {
			deletePageIndices = append(deletePageIndices, later.Index)
			for _, item := range later.Items {
				postStates = append(postStates, SnapshotPostStateUpdate{PostID: item.PostID, Expected: snapshotPostState("reserved", later.Index)})
				releasedCandidates = append(releasedCandidates, item)
			}
		}
		if changed && len(laterPages) > 0 {
			updated.PageCount = pageIndex + 1
		}
		if len(releasedCandidates) > 0 {
			updated.Candidates = prependUniqueCachedCandidates(releasedCandidates, updated.Candidates, candidatePoolLimit(service.config.Recommendation.Snapshot.MaxCandidates))
			updated.Exhausted = false
		} else {
			updated.Exhausted, err = service.snapshotCanDeclareExhausted(ctx, updated, page)
			if err != nil {
				return CachedPage{}, err
			}
		}
		contextEntries, err := service.dependencies.Snapshots.LoadContext(ctx, snapshotID, defaultSnapshotMaxPages)
		if err != nil {
			return CachedPage{}, err
		}
		updatedContext := replaceSnapshotContextPage(
			contextEntries, pageIndex, page, returnedSet, changed, snapshotContextWindow(service.config.Recommendation.Diversity),
		)
		updated.Revision = snapshot.Revision + 1
		committed, err := service.dependencies.Snapshots.Apply(ctx, snapshotID, snapshot.Revision, SnapshotMutation{
			Snapshot: updated, Page: &page, DeletePageIndices: deletePageIndices,
			PostStates: postStates, Context: &updatedContext,
			PendingHistoryChange: SnapshotPendingHistoryAdd, PendingHistoryPage: pageIndex,
		})
		if err != nil {
			return CachedPage{}, err
		}
		if committed {
			if newlyRejectedCount > 0 {
				if diagnostics, ok := service.metrics.(RecommendationDiagnosticsMetrics); ok {
					diagnostics.AddMapperRejected(newlyRejectedCount)
				}
			}
			return page, nil
		}
	}
	return CachedPage{}, fmt.Errorf("%w: finalize recommendation page contention", ErrSnapshotUnavailable)
}

func selectedIDsNotIn(selectedIDs, excludedIDs []uint) []uint {
	excluded := make(map[uint]struct{}, len(excludedIDs))
	for _, id := range excludedIDs {
		excluded[id] = struct{}{}
	}
	result := make([]uint, 0)
	for _, id := range selectedIDs {
		if _, found := excluded[id]; !found {
			result = append(result, id)
		}
	}
	return result
}

func samePostIDs(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (service *RecommendationService) updateSnapshotPage(ctx context.Context, snapshotID string, pageIndex int, complete func(CachedPage) bool, update func(*CachedPage)) (CachedPage, bool, error) {
	for attempt := 0; attempt < 8; attempt++ {
		snapshot, err := service.dependencies.Snapshots.Load(ctx, snapshotID)
		if err != nil {
			return CachedPage{}, false, err
		}
		page, exists, err := service.dependencies.Snapshots.LoadPage(ctx, snapshotID, pageIndex)
		if err != nil {
			return CachedPage{}, false, err
		}
		if !exists {
			return CachedPage{}, false, ErrSnapshotUnavailable
		}
		if complete(page) {
			return page, false, nil
		}
		update(&page)
		updated := cloneSnapshot(snapshot)
		updated.Revision = snapshot.Revision + 1
		pendingChange := SnapshotPendingHistoryUnchanged
		if page.HistoryRecorded {
			pendingChange = SnapshotPendingHistoryRemove
		}
		committed, err := service.dependencies.Snapshots.Apply(ctx, snapshotID, snapshot.Revision, SnapshotMutation{
			Snapshot: updated, Page: &page, PendingHistoryChange: pendingChange, PendingHistoryPage: pageIndex,
		})
		if err != nil {
			return CachedPage{}, false, err
		}
		if committed {
			return page, true, nil
		}
	}
	return CachedPage{}, false, fmt.Errorf("%w: update recommendation page contention", ErrSnapshotUnavailable)
}

func (service *RecommendationService) recordCommittedPageHistory(ctx context.Context, request ServeRequest, snapshotID string, page CachedPage) error {
	if len(page.ReturnedPostIDs) == 0 || service.dependencies.History == nil {
		return nil
	}
	pageKey := fmt.Sprintf("%s:%d", snapshotID, page.Index)
	switch request.Viewer.Kind {
	case ViewerAuthenticated:
		if request.Viewer.UserID == 0 {
			return nil
		}
		window := userHistoryWindow(request.Now, service.config.Recommendation)
		if history, ok := service.dependencies.History.(PageHistoryStore); ok {
			return history.RecordUserServedForPage(ctx, request.Viewer.UserID, pageKey, page.ReturnedPostIDs, window)
		}
		return service.dependencies.History.RecordUserServed(ctx, request.Viewer.UserID, page.ReturnedPostIDs, window)
	case ViewerGuest:
		if strings.TrimSpace(request.Viewer.GuestSessionID) == "" {
			return nil
		}
		window := guestHistoryWindow(request.Now, service.config.Recommendation)
		if history, ok := service.dependencies.History.(GuestPageHistoryStore); ok {
			return history.RecordGuestServedForPage(ctx, request.Viewer.GuestSessionID, pageKey, page.ReturnedPostIDs, window)
		}
		return service.dependencies.History.RecordGuestServed(ctx, request.Viewer.GuestSessionID, page.ReturnedPostIDs, window)
	}
	return nil
}

func (service *RecommendationService) recoverCommittedSnapshotHistory(ctx context.Context, request ServeRequest, snapshot CandidatePoolSnapshot) (CandidatePoolSnapshot, error) {
	recovered := false
	pages, err := service.dependencies.Snapshots.LoadPendingHistory(ctx, snapshot.ID, 8)
	if err != nil {
		return snapshot, err
	}
	for _, page := range pages {
		recoveryRequest := request
		recoveryRequest.RequestID = page.RequestID
		if !page.ServedAt.IsZero() {
			recoveryRequest.Now = page.ServedAt.UTC()
		}
		if err := service.recordCommittedPageHistory(ctx, recoveryRequest, snapshot.ID, page); err != nil {
			if ctx.Err() != nil {
				return snapshot, ctx.Err()
			}
			log.Printf("[Recommendation] committed page history recovery deferred for snapshot %s page %d: %v", snapshot.ID, page.Index, err)
			continue
		}
		recovered = true
		if _, _, err := service.updateSnapshotPage(ctx, snapshot.ID, page.Index,
			func(current CachedPage) bool { return current.HistoryRecorded },
			func(current *CachedPage) { current.HistoryRecorded = true; current.Finalized = current.TraceAttempted },
		); err != nil {
			if ctx.Err() != nil {
				return snapshot, ctx.Err()
			}
			log.Printf("[Recommendation] committed page history receipt recovery deferred for snapshot %s page %d: %v", snapshot.ID, page.Index, err)
		}
	}
	if !recovered {
		return snapshot, nil
	}
	return service.dependencies.Snapshots.Load(ctx, snapshot.ID)
}

func (service *RecommendationService) enqueueServingTrace(ctx context.Context, request ServeRequest, result ServeResult, generationDuration time.Duration) TraceEnqueueResult {
	if request.Viewer.Kind != ViewerAuthenticated || ctx.Err() != nil || service.dependencies.TraceEnqueuer == nil {
		return ""
	}
	traceRequest := buildRecommendationRequest(request, result, generationDuration, service.config.Recommendation)
	return service.dependencies.TraceEnqueuer.TryEnqueue(TracePersistJob{
		Request: traceRequest,
		Results: buildResultTraces(traceRequest, result.Selected, request.Now, service.config.Recommendation),
	})
}

func (service *RecommendationService) enqueueServingTraceBestEffort(ctx context.Context, request ServeRequest, result ServeResult, generationDuration time.Duration) {
	queued := service.enqueueServingTrace(ctx, request, result, generationDuration)
	if queued == "" || queued == TraceEnqueueQueued {
		return
	}
	// The dispatcher records bounded enqueue outcome metrics. A log line keeps
	// queue pressure visible without making optional trace persistence part of
	// the recommendation response's success path.
	if atomic.AddUint64(&traceDropLogCount, 1)%100 == 1 {
		log.Printf("[RecommendationTrace] serving trace enqueue dropped: %s", queued)
	}
}

func uniqueSelected(input []SelectedCandidate) []SelectedCandidate {
	seen := make(map[uint]struct{}, len(input))
	result := make([]SelectedCandidate, 0, len(input))
	for _, item := range input {
		if item.Post.ID == 0 {
			continue
		}
		if _, exists := seen[item.Post.ID]; exists {
			continue
		}
		seen[item.Post.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}

func finalTrackingFacts(facts []TrackingFact, ids map[uint]struct{}) []TrackingFact {
	result := make([]TrackingFact, 0, len(facts))
	for _, fact := range facts {
		if _, exists := ids[fact.PostID]; exists {
			result = append(result, fact)
		}
	}
	return result
}

func (service *RecommendationService) selectedSnapshotContext(ctx context.Context, contextEntries []SnapshotContextEntry, pageIndex int, servingVersion string) ([]SelectedCandidate, error) {
	ids := selectedSnapshotContextIDs(contextEntries, pageIndex)
	embeddings, err := service.dependencies.Candidates.LoadPostEmbeddings(ctx, ids, servingVersion)
	if err != nil {
		return nil, err
	}
	return selectedContextFromEntries(contextEntries, embeddings), nil
}

func selectedSnapshotContextIDs(contextEntries []SnapshotContextEntry, pageIndex int) []uint {
	ids := make([]uint, 0)
	for _, entry := range contextEntries {
		if entry.PageIndex >= pageIndex {
			continue
		}
		ids = append(ids, entry.Candidate.PostID)
	}
	return ids
}

func snapshotContextWindow(diversity config.RecommendationDiversityConfig) int {
	window := diversity.SemanticHistoryWindowSize
	if window <= 0 {
		window = defaultSemanticHistoryWindowSize
	}
	if diversity.AuthorWindowSize > window {
		window = diversity.AuthorWindowSize
	}
	if window > maxSnapshotContextEntries {
		window = maxSnapshotContextEntries
	}
	return window
}

func appendSnapshotContext(entries []SnapshotContextEntry, page CachedPage, window int) []SnapshotContextEntry {
	items := pageContextItems(page)
	updated := append([]SnapshotContextEntry(nil), entries...)
	for _, item := range items {
		updated = append(updated, SnapshotContextEntry{PageIndex: page.Index, Candidate: item})
	}
	if window <= 0 {
		window = defaultSemanticHistoryWindowSize
	}
	if len(updated) > window {
		updated = append([]SnapshotContextEntry(nil), updated[len(updated)-window:]...)
	}
	return updated
}

func replaceSnapshotContextPage(entries []SnapshotContextEntry, pageIndex int, page CachedPage, returned map[uint]struct{}, invalidateLater bool, window int) []SnapshotContextEntry {
	updated := make([]SnapshotContextEntry, 0, len(entries)+len(page.Items))
	for _, entry := range entries {
		if entry.PageIndex == pageIndex || (invalidateLater && entry.PageIndex > pageIndex) {
			continue
		}
		updated = append(updated, entry)
	}
	for _, item := range pageContextItems(page) {
		if _, keep := returned[item.PostID]; keep {
			updated = append(updated, SnapshotContextEntry{PageIndex: pageIndex, Candidate: item})
		}
	}
	sort.SliceStable(updated, func(i, j int) bool { return updated[i].PageIndex < updated[j].PageIndex })
	if window <= 0 {
		window = defaultSemanticHistoryWindowSize
	}
	if len(updated) > window {
		updated = append([]SnapshotContextEntry(nil), updated[len(updated)-window:]...)
	}
	return updated
}

func prependUniqueCachedCandidates(preferred, existing []CachedCandidate, maxCandidates int) []CachedCandidate {
	if maxCandidates <= 0 || maxCandidates > 500 {
		maxCandidates = 500
	}
	combined := make([]CachedCandidate, 0, min(maxCandidates, len(preferred)+len(existing)))
	seen := make(map[uint]struct{}, len(preferred)+len(existing))
	for _, group := range [][]CachedCandidate{preferred, existing} {
		for _, candidate := range group {
			if candidate.PostID == 0 {
				continue
			}
			if _, exists := seen[candidate.PostID]; exists {
				continue
			}
			seen[candidate.PostID] = struct{}{}
			combined = append(combined, candidate)
			if len(combined) == maxCandidates {
				return combined
			}
		}
	}
	return combined
}

func pageContextItems(page CachedPage) []CachedCandidate {
	if !snapshotPageCommitted(page) {
		return append([]CachedCandidate(nil), page.Items...)
	}
	itemsByID := make(map[uint]CachedCandidate, len(page.Items))
	for _, item := range page.Items {
		itemsByID[item.PostID] = item
	}
	items := make([]CachedCandidate, 0, len(page.ReturnedPostIDs))
	for _, id := range page.ReturnedPostIDs {
		if item, exists := itemsByID[id]; exists {
			items = append(items, item)
		}
	}
	return items
}

func selectRankedPage(fresh, soft []RankedCandidate, prior []SelectedCandidate, limit int, now time.Time, requestID string, cfg SelectionConfig) []SelectedCandidate {
	selected := SelectNextPage(SelectionInput{
		Candidates: fresh, Limit: limit, Now: now, Phase: SelectionPhaseFresh, RequestID: requestID, Config: cfg,
	}, prior)
	if len(selected) < limit && len(soft) > 0 {
		selected = SelectNextPage(SelectionInput{
			Candidates: soft, Initial: selected, Limit: limit, Now: now, Phase: SelectionPhaseSoft, RequestID: requestID, Config: cfg,
		}, prior)
	}
	return selected
}

func excludePreselectedRankedCandidates(candidates []RankedCandidate, preselectedPostIDs map[uint]struct{}) []RankedCandidate {
	available := make([]RankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Post.ID == 0 {
			continue
		}
		if _, preselected := preselectedPostIDs[candidate.Post.ID]; preselected {
			continue
		}
		available = append(available, candidate)
	}
	return available
}

func cachedRankedCandidate(item RankedCandidate, phase SelectionPhase) CachedCandidate {
	return CachedCandidate{
		Candidate: item.Candidate, PostID: item.Post.ID, AuthorID: item.Post.AuthorID, CreatedAt: item.Post.CreatedAt.UTC(),
		RecallTier: item.Candidate.RecallTier,
		Breakdown:  item.Breakdown, ExplorationSemantic: item.ExplorationSemantic,
		IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor, Phase: phase,
	}
}

func cachedPageFromSelected(index int, requestID string, servedAt time.Time, latency time.Duration, selected []SelectedCandidate) CachedPage {
	page := CachedPage{
		Index: index, RequestID: requestID, ServedAt: servedAt.UTC(),
		GenerationLatencyMS: latency.Milliseconds(), Items: make([]CachedCandidate, 0, len(selected)),
	}
	for _, item := range selected {
		phase := SelectionPhaseFresh
		if item.Candidate.WasSoftServed {
			phase = SelectionPhaseSoft
		}
		cached := CachedCandidate{
			Candidate: item.Candidate, PostID: item.Post.ID, AuthorID: item.Post.AuthorID, CreatedAt: item.Post.CreatedAt.UTC(),
			Breakdown: item.Breakdown, ExplorationSemantic: item.ExplorationSemantic,
			IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor, Phase: phase,
			SelectionMode: item.SelectionMode, ExplorationReason: item.ExplorationReason,
			ExplorationOpportunity: item.ExplorationOpportunity,
		}
		page.Items = append(page.Items, cached)
	}
	return page
}

func cachedPagePostIDs(page CachedPage) []uint {
	ids := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		if item.PostID != 0 {
			ids = append(ids, item.PostID)
		}
	}
	return ids
}

func snapshotPageAt(snapshot CandidatePoolSnapshot, pageIndex int) (CachedPage, bool) {
	for _, page := range snapshot.Pages {
		if page.Index == pageIndex {
			return page, true
		}
	}
	return CachedPage{}, false
}

func cloneSnapshot(snapshot CandidatePoolSnapshot) CandidatePoolSnapshot {
	snapshot.Candidates = append([]CachedCandidate(nil), snapshot.Candidates...)
	snapshot.UsedPostIDs = append([]uint(nil), snapshot.UsedPostIDs...)
	snapshot.MapperRejectedPostIDs = append([]uint(nil), snapshot.MapperRejectedPostIDs...)
	snapshot.Pages = append([]CachedPage(nil), snapshot.Pages...)
	snapshot.DiscardedPostIDs = append([]uint(nil), snapshot.DiscardedPostIDs...)
	for index := range snapshot.Pages {
		snapshot.Pages[index].Items = append([]CachedCandidate(nil), snapshot.Pages[index].Items...)
		snapshot.Pages[index].ReturnedPostIDs = append([]uint(nil), snapshot.Pages[index].ReturnedPostIDs...)
	}
	return snapshot
}

func appendUniqueIDs(target []uint, ids ...uint) []uint {
	seen := make(map[uint]struct{}, len(target)+len(ids))
	for _, id := range target {
		seen[id] = struct{}{}
	}
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		target = append(target, id)
	}
	return target
}

func recommendationLanguageKey(browser LanguageContext, language LanguageContext) string {
	value := struct {
		BrowserPrimary string
		BrowserPrior   LanguagePrior
		Behavior       LanguagePrior
		Combined       LanguagePrior
		Source         string
	}{browser.BrowserPrimary, browser.Browser, language.Behavior, language.Combined, language.Source}
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func candidateSummaryFromSnapshot(candidates []CachedCandidate, freshOnly bool) CandidateSetSummary {
	summary := CandidateSetSummary{Candidates: make([]Candidate, 0, len(candidates))}
	for _, item := range candidates {
		if freshOnly && item.Phase != SelectionPhaseFresh {
			continue
		}
		candidate := item.Candidate
		summary.Candidates = append(summary.Candidates, candidate)
		switch candidate.RecallTier {
		case RecallTierHistoricalUnseen:
			summary.HistoricalCount++
		case RecallTierSoftRevisit:
			summary.SoftCount++
		}
		if candidate.FromSemantic {
			summary.SemanticCount++
		}
		if candidate.FromFollowing {
			summary.FollowingCount++
		}
		if candidate.FromRecent {
			summary.RecentCount++
			summary.RecentPostIDs = append(summary.RecentPostIDs, candidate.PostID)
		}
		if candidate.FromTrending {
			summary.TrendingCount++
		}
		if candidate.FromOnboardingInterest {
			summary.OnboardingInterestActive = true
			summary.OnboardingInterestCandidateCount++
		}
	}
	return summary
}

func snapshotReturnedIDs(page CachedPage) []uint {
	if snapshotPageCommitted(page) {
		return append([]uint(nil), page.ReturnedPostIDs...)
	}
	return cachedPagePostIDs(page)
}

func requestCursorPageIndex(snapshot CandidatePoolSnapshot, requestID string) int {
	for _, page := range snapshot.Pages {
		if page.RequestID == requestID {
			return page.Index
		}
	}
	return -1
}

func uniqueCachedCandidates(candidates []CachedCandidate) []CachedCandidate {
	seen := make(map[uint]struct{}, len(candidates))
	result := make([]CachedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PostID == 0 {
			continue
		}
		if _, exists := seen[candidate.PostID]; exists {
			continue
		}
		seen[candidate.PostID] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func selectedContextFromPages(pages []CachedPage, embeddings map[uint][]float32) []SelectedCandidate {
	entries := make([]SnapshotContextEntry, 0)
	for _, page := range pages {
		for _, item := range pageContextItems(page) {
			entries = append(entries, SnapshotContextEntry{PageIndex: page.Index, Candidate: item})
		}
	}
	return selectedContextFromEntries(entries, embeddings)
}

func selectedContextFromEntries(entries []SnapshotContextEntry, embeddings map[uint][]float32) []SelectedCandidate {
	selected := make([]SelectedCandidate, 0, len(entries))
	for _, entry := range entries {
		item := entry.Candidate
		selected = append(selected, SelectedCandidate{
			Candidate: item.Candidate,
			Post:      models.Post{Model: gorm.Model{ID: item.PostID, CreatedAt: item.CreatedAt}, AuthorID: item.AuthorID},
			Embedding: embeddings[item.PostID], Breakdown: item.Breakdown,
			IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor,
			ExplorationOpportunity: item.ExplorationOpportunity, SelectionMode: item.SelectionMode,
			ExplorationReason: item.ExplorationReason, ExplorationSemantic: item.ExplorationSemantic,
		})
	}
	return selected
}

func snapshotPageCommitted(page CachedPage) bool {
	return page.Committed || page.Finalized
}

func snapshotPageReturnedSet(page CachedPage) map[uint]struct{} {
	set := make(map[uint]struct{}, len(page.ReturnedPostIDs))
	for _, id := range page.ReturnedPostIDs {
		set[id] = struct{}{}
	}
	return set
}

func sortedPostIDs(set map[uint]struct{}) []uint {
	ids := make([]uint, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func snapshotContextError(err error) bool {
	return errors.Is(err, ErrSnapshotUnavailable) || errors.Is(err, ErrSnapshotCapacity)
}
