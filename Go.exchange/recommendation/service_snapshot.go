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
	"sync/atomic"
	"time"

	"Go.exchange/models"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const candidateSnapshotStrategyVersion = "select-next-page-v1"

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
	if request.Viewer.Kind != ViewerAuthenticated || len(result.Selected) == 0 {
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
	if len(candidates) == 0 {
		return nil
	}
	now := request.Now.UTC()
	snapshot := CandidatePoolSnapshot{
		Version: candidatePoolSnapshotVersion, ID: uuid.NewString(),
		OwnerDigest: codec.OwnerDigest(request.Viewer), CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(service.config.Recommendation.Snapshot.TTLSeconds) * time.Second),
		PageSize:  request.Limit, EmbeddingVersion: servingVersion,
		RankerConfigHash:  RankerConfigHash(service.config.Recommendation, servingVersion),
		ProfileComputedAt: result.Profile.ComputedAt.UTC(), LanguageKey: recommendationLanguageKey(request.BrowserLanguage, result.LanguageContext),
		StrategyVersion: SelectionPolicyVersion + ":" + candidateSnapshotStrategyVersion,
		Candidates:      candidates,
	}
	pageZero := cachedPageFromSelected(0, request.RequestID, now, generationDuration, result.Selected)
	snapshot.Pages = append(snapshot.Pages, pageZero)
	snapshot.UsedPostIDs = append(snapshot.UsedPostIDs, cachedPagePostIDs(pageZero)...)
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
		prior := selectedContextFromPages(snapshot.Pages, embeddings)
		pageRequestID := uuid.NewString()
		selected := selectRankedPage(
			result.freshRankedCandidates, result.softRankedCandidates, prior,
			request.Limit, now, pageRequestID, selectionConfig(service.config.Recommendation),
		)
		if len(selected) == 0 {
			break
		}
		page := cachedPageFromSelected(pageIndex, pageRequestID, now, generationDuration, selected)
		snapshot.Pages = append(snapshot.Pages, page)
		snapshot.UsedPostIDs = appendUniqueIDs(snapshot.UsedPostIDs, cachedPagePostIDs(page)...)
	}
	if err := service.dependencies.Snapshots.Create(
		ctx, snapshot, time.Duration(service.config.Recommendation.Snapshot.TTLSeconds)*time.Second, defaultSnapshotMaxBytes,
	); err != nil {
		return err
	}
	result.PoolID = snapshot.ID
	result.snapshotPrepared = true
	result.traceGenerationDuration = generationDuration
	result.finalization = &servingFinalization{snapshotID: snapshot.ID, pageIndex: 0, request: request, generationDuration: generationDuration}
	result.NextCursor, err = codec.Encode(snapshot.ID, 1, snapshot.PageSize, request.Viewer, snapshot.ExpiresAt)
	if err != nil {
		return err
	}
	return nil
}

func (service *RecommendationService) serveSnapshotCursor(ctx context.Context, request ServeRequest, servingVersion string, served ServedHistory) (ServeResult, ServeRequest, error) {
	codec, err := service.cursorCodec()
	if err != nil {
		return ServeResult{}, request, ErrInvalidCursor
	}
	payload, err := codec.Decode(request.Cursor, request.Viewer, request.Limit, request.Now)
	if err != nil {
		return ServeResult{}, request, err
	}
	snapshot, err := service.dependencies.Snapshots.Load(ctx, payload.SnapshotID)
	if err != nil {
		return ServeResult{}, request, err
	}
	if snapshot.OwnerDigest != payload.OwnerDigest || snapshot.PageSize != request.Limit || snapshot.ID != payload.SnapshotID || snapshot.ExpiresAt.UTC().Unix() != payload.ExpiresAt {
		return ServeResult{}, request, ErrInvalidCursor
	}
	if !snapshot.ExpiresAt.After(request.Now) {
		return ServeResult{}, request, ErrCursorExpired
	}
	if snapshot.EmbeddingVersion != servingVersion || snapshot.RankerConfigHash != RankerConfigHash(service.config.Recommendation, servingVersion) ||
		snapshot.StrategyVersion != SelectionPolicyVersion+":"+candidateSnapshotStrategyVersion {
		return ServeResult{}, request, ErrCursorExpired
	}
	profile, err := service.loadProfile(ctx, request.Viewer.UserID, servingVersion, request.Now, true)
	if err != nil {
		return ServeResult{}, request, err
	}
	language := BuildLanguageContext(
		request.BrowserLanguage,
		LanguagePrior{ZH: profile.LanguageZHWeight, JA: profile.LanguageJAWeight, EN: profile.LanguageENWeight},
		profile.LanguageEvidence,
		languageConfig(service.config.Recommendation),
	)
	if !profile.ComputedAt.UTC().Equal(snapshot.ProfileComputedAt.UTC()) || recommendationLanguageKey(request.BrowserLanguage, language) != snapshot.LanguageKey {
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
		if page, exists := snapshotPageAt(snapshot, pageIndex); exists {
			result, pageRequest, materializeErr := service.materializeSnapshotPage(ctx, request, snapshot, page, profile, language, servingVersion)
			return result, pageRequest, materializeErr
		}
		updated := cloneSnapshot(snapshot)
		page, err := service.buildSnapshotPage(ctx, request, servingVersion, profile, &updated, pageIndex, served)
		if err != nil {
			return ServeResult{}, request, err
		}
		updated.Pages = append(updated.Pages, page)
		updated.UsedPostIDs = appendUniqueIDs(updated.UsedPostIDs, cachedPagePostIDs(page)...)
		updated.Revision = snapshot.Revision + 1
		committed, err := service.dependencies.Snapshots.CompareAndSwap(ctx, snapshot.ID, snapshot.Revision, updated, defaultSnapshotMaxBytes)
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

func (service *RecommendationService) buildSnapshotPage(ctx context.Context, request ServeRequest, servingVersion string, profile Profile, snapshot *CandidatePoolSnapshot, pageIndex int, served ServedHistory) (CachedPage, error) {
	if snapshot.Exhausted {
		return CachedPage{Index: pageIndex, RequestID: uuid.NewString(), ServedAt: request.Now}, nil
	}
	requestID := uuid.NewString()
	start := time.Now()
	page, err := service.selectSnapshotPage(ctx, request, servingVersion, profile, snapshot, pageIndex, requestID)
	if err != nil {
		return CachedPage{}, err
	}
	if len(page.Items) == 0 && !snapshot.Exhausted {
		// A depleted candidate pool is refreshed at most once per cursor request.
		fresh, err := service.rebuildSnapshotCandidates(ctx, request, servingVersion, *snapshot, served)
		if err != nil {
			return CachedPage{}, err
		}
		*snapshot = fresh
		if !snapshot.Exhausted {
			page, err = service.selectSnapshotPage(ctx, request, servingVersion, profile, snapshot, pageIndex, requestID)
			if err != nil {
				return CachedPage{}, err
			}
		}
		if len(page.Items) == 0 {
			snapshot.Exhausted = true
		}
	}
	page.GenerationLatencyMS = time.Since(start).Milliseconds()
	return page, nil
}

func (service *RecommendationService) selectSnapshotPage(ctx context.Context, request ServeRequest, servingVersion string, profile Profile, snapshot *CandidatePoolSnapshot, pageIndex int, requestID string) (CachedPage, error) {
	used := make(map[uint]struct{}, len(snapshot.UsedPostIDs))
	for _, id := range snapshot.UsedPostIDs {
		used[id] = struct{}{}
	}
	for _, id := range snapshot.MapperRejectedPostIDs {
		used[id] = struct{}{}
	}
	remaining := make([]CachedCandidate, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		if _, exists := used[candidate.PostID]; !exists {
			remaining = append(remaining, candidate)
		}
	}
	if len(remaining) == 0 {
		snapshot.Candidates = nil
		return CachedPage{Index: pageIndex, RequestID: requestID, ServedAt: request.Now}, nil
	}
	cacheByID := make(map[uint]CachedCandidate, len(remaining))
	candidates := make([]Candidate, 0, len(remaining))
	for _, cached := range remaining {
		cacheByID[cached.PostID] = cached
		candidates = append(candidates, cached.Candidate)
	}
	var eligible []RankedCandidate
	var prior []SelectedCandidate
	var err error
	if repository, ok := service.dependencies.Candidates.(SnapshotPageRepository); ok {
		contextIDs := selectedSnapshotContextIDs(*snapshot, pageIndex)
		query := CandidateQuery{
			UserID: request.Viewer.UserID, ServingVersion: servingVersion, Now: request.Now,
			MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
			InteractedPostIDs:             profile.InteractedPostIDs,
		}
		var contextEmbeddings map[uint][]float32
		eligible, contextEmbeddings, err = repository.LoadSnapshotPage(ctx, query, candidates, contextIDs)
		if err != nil {
			return CachedPage{}, err
		}
		prior = selectedContextFromPages(snapshot.Pages, contextEmbeddings)
	} else {
		eligible, err = service.loadEligibleSnapshotCandidates(ctx, request, servingVersion, profile, candidates)
		if err != nil {
			return CachedPage{}, err
		}
		prior, err = service.selectedSnapshotContext(ctx, *snapshot, pageIndex, servingVersion)
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
		ranked = append(ranked, hydrated)
		eligibleIDs[hydrated.Post.ID] = struct{}{}
	}
	// Drop posts that have become ineligible so later pages do not keep
	// checking the same stale IDs from this pool.
	remainingCandidates := snapshot.Candidates[:0]
	for _, cached := range remaining {
		if _, exists := eligibleIDs[cached.PostID]; exists {
			remainingCandidates = append(remainingCandidates, cached)
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
	selected := selectRankedPage(fresh, soft, prior, snapshot.PageSize, request.Now, requestID, selectionConfig(service.config.Recommendation))
	return cachedPageFromSelected(pageIndex, requestID, request.Now, 0, selected), nil
}

func (service *RecommendationService) rebuildSnapshotCandidates(ctx context.Context, request ServeRequest, servingVersion string, snapshot CandidatePoolSnapshot, served ServedHistory) (CandidatePoolSnapshot, error) {
	if served == nil {
		served = make(ServedHistory)
	}
	for _, id := range append(append([]uint(nil), snapshot.UsedPostIDs...), snapshot.MapperRejectedPostIDs...) {
		served[id] = ServedItem{PostID: id, LastServedAt: snapshot.CreatedAt, Hard: true}
	}
	result, err := service.executeUser(ctx, request, servingVersion, served, false)
	if err != nil {
		return snapshot, err
	}
	if !result.Profile.ComputedAt.UTC().Equal(snapshot.ProfileComputedAt.UTC()) ||
		recommendationLanguageKey(request.BrowserLanguage, result.LanguageContext) != snapshot.LanguageKey ||
		RankerConfigHash(service.config.Recommendation, servingVersion) != snapshot.RankerConfigHash {
		return snapshot, ErrCursorExpired
	}
	limit := service.config.Recommendation.Snapshot.MaxCandidates
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	seen := make(map[uint]struct{}, limit)
	rejected := make(map[uint]struct{}, len(snapshot.MapperRejectedPostIDs))
	for _, id := range snapshot.MapperRejectedPostIDs {
		rejected[id] = struct{}{}
	}
	candidates := make([]CachedCandidate, 0, limit)
	appendRanked := func(items []RankedCandidate, phase SelectionPhase) {
		for _, item := range items {
			if len(candidates) >= limit || item.Post.ID == 0 {
				return
			}
			if _, isRejected := rejected[item.Post.ID]; isRejected {
				continue
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
	snapshot.Candidates = candidates
	snapshot.Exhausted = len(candidates) == 0
	return snapshot, nil
}

func (service *RecommendationService) materializeSnapshotPage(ctx context.Context, request ServeRequest, snapshot CandidatePoolSnapshot, page CachedPage, profile Profile, language LanguageContext, servingVersion string) (ServeResult, ServeRequest, error) {
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
	eligible, err := service.loadEligibleSnapshotCandidates(ctx, request, servingVersion, profile, inputs)
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
		selected = append(selected, SelectedCandidate{
			Candidate: item.Candidate, Post: item.Post, Embedding: item.Embedding,
			Breakdown: item.Breakdown, IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor,
			ExplorationOpportunity: cached.ExplorationOpportunity, SelectionMode: cached.SelectionMode,
			ExplorationReason: cached.ExplorationReason, ExplorationSemantic: cached.ExplorationSemantic,
		})
	}
	result := ServeResult{
		Viewer: request.Viewer, RequestID: request.RequestID, Now: request.Now, Limit: request.Limit,
		EmbeddingVersion: servingVersion, Profile: profile, LanguageContext: language,
		CandidateSummary:      candidateSummaryFromSnapshot(snapshot.Candidates, false),
		FreshCandidateSummary: candidateSummaryFromSnapshot(snapshot.Candidates, true),
		Selected:              selected, StrategyID: serviceStrategyFor(request.Viewer, profile),
		RankerConfigHash:    snapshot.RankerConfigHash,
		PersonalizationMode: personalizationMode(profile, 0),
		Depleted:            snapshot.Exhausted && len(selected) == 0,
		PoolID:              snapshot.ID, CacheHit: true, snapshotPrepared: true,
		traceGenerationDuration: time.Duration(page.GenerationLatencyMS) * time.Millisecond,
		finalization:            &servingFinalization{snapshotID: snapshot.ID, pageIndex: page.Index, request: request, generationDuration: time.Duration(page.GenerationLatencyMS) * time.Millisecond},
	}
	result.FallbackReason = fallbackReason(profile.PositiveSignalCount, len(selected), request.Limit)
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
	query := CandidateQuery{
		UserID: request.Viewer.UserID, ServingVersion: servingVersion, Now: request.Now,
		MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs:             profile.InteractedPostIDs,
	}
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
		page, exists := snapshotPageAt(snapshot, pageIndex)
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
		if changed {
			for _, later := range snapshot.Pages {
				if later.Index > pageIndex && snapshotPageCommitted(later) {
					return CachedPage{}, fmt.Errorf("%w: a later recommendation page is already committed", ErrCursorExpired)
				}
			}
		}
		page.Committed = true
		page.ReturnedPostIDs = append([]uint(nil), returnedIDs...)
		updated := cloneSnapshot(snapshot)
		pages := updated.Pages[:0]
		for index := range updated.Pages {
			laterUncommitted := changed && updated.Pages[index].Index > pageIndex && !snapshotPageCommitted(updated.Pages[index])
			if laterUncommitted {
				continue
			}
			if updated.Pages[index].Index == pageIndex {
				updated.Pages[index] = page
			}
			pages = append(pages, updated.Pages[index])
		}
		updated.Pages = pages
		if changed {
			preparedSet := make(map[uint]struct{}, len(preparedIDs))
			returnedSet := make(map[uint]struct{}, len(returnedIDs))
			for _, id := range preparedIDs {
				preparedSet[id] = struct{}{}
			}
			for _, id := range returnedIDs {
				returnedSet[id] = struct{}{}
			}
			for _, id := range mapperRejectedIDs {
				if _, prepared := preparedSet[id]; !prepared {
					continue
				}
				if _, returned := returnedSet[id]; returned {
					continue
				}
				updated.MapperRejectedPostIDs = appendUniqueIDs(updated.MapperRejectedPostIDs, id)
			}
		}
		updated.UsedPostIDs = snapshotUsedPostIDs(updated.Pages)
		updated.Revision = snapshot.Revision + 1
		committed, err := service.dependencies.Snapshots.CompareAndSwap(ctx, snapshotID, snapshot.Revision, updated, defaultSnapshotMaxBytes)
		if err != nil {
			return CachedPage{}, err
		}
		if committed {
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

func snapshotUsedPostIDs(pages []CachedPage) []uint {
	var used []uint
	for _, page := range pages {
		if snapshotPageCommitted(page) {
			used = appendUniqueIDs(used, page.ReturnedPostIDs...)
			continue
		}
		used = appendUniqueIDs(used, cachedPagePostIDs(page)...)
	}
	return used
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
		page, exists := snapshotPageAt(snapshot, pageIndex)
		if !exists {
			return CachedPage{}, false, ErrSnapshotUnavailable
		}
		if complete(page) {
			return page, false, nil
		}
		update(&page)
		updated := cloneSnapshot(snapshot)
		for index := range updated.Pages {
			if updated.Pages[index].Index == pageIndex {
				updated.Pages[index] = page
				break
			}
		}
		updated.Revision = snapshot.Revision + 1
		committed, err := service.dependencies.Snapshots.CompareAndSwap(ctx, snapshotID, snapshot.Revision, updated, defaultSnapshotMaxBytes)
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
	if request.Viewer.Kind != ViewerAuthenticated || request.Viewer.UserID == 0 || len(page.ReturnedPostIDs) == 0 || service.dependencies.History == nil {
		return nil
	}
	window := userHistoryWindow(request.Now, service.config.Recommendation)
	if history, ok := service.dependencies.History.(PageHistoryStore); ok {
		return history.RecordUserServedForPage(ctx, request.Viewer.UserID, fmt.Sprintf("%s:%d", snapshotID, page.Index), page.ReturnedPostIDs, window)
	}
	// Production Redis history uses the page-keyed atomic receipt. This fallback
	// keeps alternate stores usable but does not promise cross-process deduplication.
	return service.dependencies.History.RecordUserServed(ctx, request.Viewer.UserID, page.ReturnedPostIDs, window)
}

func (service *RecommendationService) recoverCommittedSnapshotHistory(ctx context.Context, request ServeRequest, snapshot CandidatePoolSnapshot) (CandidatePoolSnapshot, error) {
	recovered := false
	for _, page := range snapshot.Pages {
		if !snapshotPageCommitted(page) || page.HistoryRecorded {
			continue
		}
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

func (service *RecommendationService) selectedSnapshotContext(ctx context.Context, snapshot CandidatePoolSnapshot, pageIndex int, servingVersion string) ([]SelectedCandidate, error) {
	items := make([]CachedCandidate, 0)
	ids := selectedSnapshotContextIDs(snapshot, pageIndex)
	for _, page := range snapshot.Pages {
		if page.Index < pageIndex {
			items = append(items, pageContextItems(page)...)
		}
	}
	embeddings, err := service.dependencies.Candidates.LoadPostEmbeddings(ctx, ids, servingVersion)
	if err != nil {
		return nil, err
	}
	selected := make([]SelectedCandidate, 0, len(items))
	for _, item := range items {
		selected = append(selected, SelectedCandidate{
			Candidate: item.Candidate,
			Post:      models.Post{Model: gorm.Model{ID: item.PostID, CreatedAt: item.CreatedAt}, AuthorID: item.AuthorID},
			Embedding: embeddings[item.PostID], Breakdown: item.Breakdown,
			IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor,
			ExplorationOpportunity: item.ExplorationOpportunity, SelectionMode: item.SelectionMode,
			ExplorationReason: item.ExplorationReason, ExplorationSemantic: item.ExplorationSemantic,
		})
	}
	return selected, nil
}

func selectedSnapshotContextIDs(snapshot CandidatePoolSnapshot, pageIndex int) []uint {
	ids := make([]uint, 0)
	for _, page := range snapshot.Pages {
		if page.Index >= pageIndex {
			continue
		}
		for _, item := range pageContextItems(page) {
			ids = append(ids, item.PostID)
		}
	}
	return ids
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

func cachedRankedCandidate(item RankedCandidate, phase SelectionPhase) CachedCandidate {
	return CachedCandidate{
		Candidate: item.Candidate, PostID: item.Post.ID, AuthorID: item.Post.AuthorID, CreatedAt: item.Post.CreatedAt.UTC(),
		Breakdown: item.Breakdown, ExplorationSemantic: item.ExplorationSemantic,
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
	selected := make([]SelectedCandidate, 0)
	for _, page := range pages {
		for _, item := range pageContextItems(page) {
			selected = append(selected, SelectedCandidate{
				Candidate: item.Candidate,
				Post:      models.Post{Model: gorm.Model{ID: item.PostID, CreatedAt: item.CreatedAt}, AuthorID: item.AuthorID},
				Embedding: embeddings[item.PostID], Breakdown: item.Breakdown,
				IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor,
				ExplorationOpportunity: item.ExplorationOpportunity, SelectionMode: item.SelectionMode,
				ExplorationReason: item.ExplorationReason, ExplorationSemantic: item.ExplorationSemantic,
			})
		}
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
	return errors.Is(err, ErrSnapshotUnavailable) || errors.Is(err, ErrSnapshotTooLarge)
}
