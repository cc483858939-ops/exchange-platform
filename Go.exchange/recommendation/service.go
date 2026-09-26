package recommendation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
)

const (
	defaultRecommendationLimit = 20
	maxRecommendationLimit     = 50
	defaultTracePersistTimeout = 5 * time.Second
)

var (
	ErrInvalidViewer  = errors.New("invalid recommendation viewer")
	ErrInvalidRequest = errors.New("invalid recommendation request")
)

type RecommendationService struct {
	dependencies ServiceDependencies
	config       ServiceConfig
	metrics      Metrics
}

func NewService(dependencies ServiceDependencies, cfg ServiceConfig) (VerificationService, error) {
	if dependencies.Candidates == nil {
		return nil, errors.New("recommendation candidate repository is required")
	}
	if dependencies.ServingVersions == nil {
		return nil, errors.New("recommendation serving version provider is required")
	}
	if dependencies.Metrics == nil {
		dependencies.Metrics = NoopMetrics{}
	}
	if cfg.TracePersistTimeout <= 0 {
		cfg.TracePersistTimeout = defaultTracePersistTimeout
	}
	cfg.Tracking.SigningKey = append([]byte(nil), cfg.Tracking.SigningKey...)
	return &RecommendationService{dependencies: dependencies, config: cfg, metrics: dependencies.Metrics}, nil
}

func (service *RecommendationService) Serve(ctx context.Context, request ServeRequest) (ServeResult, error) {
	started := time.Now()
	request, err := normalizeServeRequest(request)
	if err != nil {
		service.metrics.RecordRequest("error", RecommendationColdStartStrategyID)
		return ServeResult{}, err
	}
	if ctx == nil {
		service.metrics.RecordRequest("error", serviceStrategyFor(request.Viewer, Profile{}))
		return ServeResult{}, errors.New("recommendation context is nil")
	}
	if err := ctx.Err(); err != nil {
		service.metrics.RecordRequest(contextOutcome(err), serviceStrategyFor(request.Viewer, Profile{}))
		return ServeResult{}, err
	}
	servingVersion, err := service.dependencies.ServingVersions.LoadServingVersion(ctx)
	if err != nil {
		service.recordFailedRequest(request.Viewer, err, Profile{})
		return ServeResult{RequestID: request.RequestID, Now: request.Now, Viewer: request.Viewer}, err
	}
	served, historyErr := service.loadHistory(ctx, request)
	if historyErr != nil {
		service.recordFailedRequest(request.Viewer, historyErr, Profile{})
		return ServeResult{RequestID: request.RequestID, Now: request.Now, Viewer: request.Viewer}, historyErr
	}
	result, err := service.execute(ctx, request, servingVersion, served, true)
	if err != nil {
		service.recordFailedRequest(request.Viewer, err, result.Profile)
		result.RequestID = request.RequestID
		result.Now = request.Now
		result.Viewer = request.Viewer
		return result, err
	}
	result.Viewer = request.Viewer
	result.RequestID = request.RequestID
	result.Now = request.Now
	result.Limit = request.Limit
	result.EmbeddingVersion = servingVersion
	result.StrategyID = serviceStrategyFor(request.Viewer, result.Profile)
	result.RankerConfigHash = RankerConfigHash(service.config.Recommendation, servingVersion)
	result.PersonalizationMode = personalizationMode(result.Profile, result.CandidateSummary.FollowingCount)
	result.FallbackReason = fallbackReason(result.Profile.PositiveSignalCount, len(result.Selected), request.Limit)
	result.Depleted = len(result.Selected) == 0
	if request.Viewer.Kind == ViewerGuest {
		result.Depleted = len(result.Selected) < request.Limit
	}

	if request.Viewer.Kind == ViewerAuthenticated {
		tracking, trackingErr := BuildTrackingFacts(result, service.config.Tracking)
		if trackingErr != nil {
			log.Printf("[RecommendationTelemetry] omit tracking metadata: %v", trackingErr)
		} else {
			result.Tracking = tracking
		}
		service.metrics.AddTrackingResults("tracked", len(result.Tracking))
		service.metrics.AddTrackingResults("untracked", len(result.Selected)-len(result.Tracking))
	}
	service.recordHistory(ctx, request, result.Selected)
	if request.Viewer.Kind == ViewerAuthenticated {
		service.persistTrace(ctx, request, result, started)
	}
	service.metrics.ObserveCandidateCount(len(result.FreshCandidateSummary.Candidates))
	service.metrics.ObserveResultCount(len(result.Selected))
	service.metrics.ObserveGenerationDuration(result.StrategyID, time.Since(started))
	service.recordResultMetrics(result.Selected)
	requestOutcome := "success"
	if len(result.Selected) == 0 {
		requestOutcome = "empty"
	}
	service.metrics.RecordRequest(requestOutcome, result.StrategyID)
	return result, nil
}

func (service *RecommendationService) Verify(ctx context.Context, request VerificationRequest) (VerificationResult, error) {
	serveRequest, err := normalizeServeRequest(ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: request.UserID},
		Limit:  request.Limit, Now: request.Now, BrowserLanguage: request.BrowserLanguage,
	})
	if err != nil {
		return VerificationResult{}, err
	}
	if ctx == nil {
		return VerificationResult{}, errors.New("recommendation verification context is nil")
	}
	if err := ctx.Err(); err != nil {
		return VerificationResult{}, err
	}
	servingVersion, err := service.dependencies.ServingVersions.LoadServingVersion(ctx)
	if err != nil {
		return VerificationResult{}, err
	}
	result, err := service.execute(ctx, serveRequest, servingVersion, make(ServedHistory), false)
	if err != nil {
		return VerificationResult{}, err
	}
	return VerificationResult{
		RecentCandidateCount: result.FreshCandidateSummary.RecentCount,
		RecentPostIDs:        append([]uint(nil), result.FreshCandidateSummary.RecentPostIDs...),
		FinalPostIDs:         selectedPostIDs(result.Selected),
	}, nil
}

func normalizeServeRequest(request ServeRequest) (ServeRequest, error) {
	switch request.Viewer.Kind {
	case ViewerAuthenticated:
		if request.Viewer.UserID == 0 {
			return ServeRequest{}, fmt.Errorf("%w: authenticated user ID is required", ErrInvalidViewer)
		}
	case ViewerGuest:
		request.Viewer.GuestSessionID = strings.TrimSpace(request.Viewer.GuestSessionID)
	default:
		return ServeRequest{}, fmt.Errorf("%w: viewer kind is required", ErrInvalidViewer)
	}
	if request.Limit <= 0 {
		request.Limit = defaultRecommendationLimit
	}
	if request.Limit > maxRecommendationLimit {
		request.Limit = maxRecommendationLimit
	}
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	} else {
		request.Now = request.Now.UTC()
	}
	if strings.TrimSpace(request.RequestID) == "" {
		request.RequestID = uuid.NewString()
	}
	return request, nil
}

func (service *RecommendationService) execute(ctx context.Context, request ServeRequest, servingVersion string, served ServedHistory, observe bool) (ServeResult, error) {
	if err := ctx.Err(); err != nil {
		return ServeResult{}, err
	}
	if request.Viewer.Kind == ViewerGuest {
		return service.executeGuest(ctx, request, servingVersion, served, observe)
	}
	return service.executeUser(ctx, request, servingVersion, served, observe)
}

func (service *RecommendationService) executeUser(ctx context.Context, request ServeRequest, servingVersion string, served ServedHistory, observe bool) (ServeResult, error) {
	if service.dependencies.Profiles == nil {
		return ServeResult{}, errors.New("recommendation profile repository is required for authenticated serving")
	}
	profile, err := service.loadProfile(ctx, request.Viewer.UserID, servingVersion, request.Now, observe)
	result := ServeResult{Profile: profile, EmbeddingVersion: servingVersion}
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	languageContext := BuildLanguageContext(
		request.BrowserLanguage,
		LanguagePrior{ZH: profile.LanguageZHWeight, JA: profile.LanguageJAWeight, EN: profile.LanguageENWeight},
		profile.LanguageEvidence,
		languageConfig(service.config.Recommendation),
	)
	loadedAuthors := make(map[uint]struct{})
	freshSet, err := buildCandidateSet(
		ctx, service.dependencies.Candidates, servingVersion, request.Viewer.UserID,
		profile, served, request.Now, service.config.Recommendation, false,
	)
	if err != nil {
		return result, err
	}
	result.RecallSets = append(result.RecallSets, freshSet)
	service.recordRecallMetrics(freshSet, observe)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	freshHydrated, err := service.dependencies.Candidates.HydrateCandidates(ctx, servingVersion, freshSet.Candidates, request.Now)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := loadMaterializedCandidateAuthorContext(ctx, service.dependencies.Profiles, request.Viewer.UserID, &profile, freshHydrated, loadedAuthors, service.config.Recommendation); err != nil {
		return result, err
	}
	rankedFresh := RankCandidates(profileFeatures(profile), freshHydrated, request.Now, rankingConfig(service.config.Recommendation), languageContext)
	selected := SelectCandidates(SelectionInput{
		Candidates: rankedFresh, Limit: request.Limit, Now: request.Now, Phase: SelectionPhaseFresh,
		RequestID: request.RequestID, Config: selectionConfig(service.config.Recommendation),
	})
	finalSet := freshSet
	if len(selected) < request.Limit {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		softSet, softErr := buildCandidateSet(
			ctx, service.dependencies.Candidates, servingVersion, request.Viewer.UserID,
			profile, served, request.Now, service.config.Recommendation, true,
		)
		if softErr != nil {
			return result, softErr
		}
		result.RecallSets = append(result.RecallSets, softSet)
		service.recordRecallMetrics(softSet, observe)
		softHydrated, softErr := service.dependencies.Candidates.HydrateCandidates(ctx, servingVersion, softSet.Candidates, request.Now)
		if softErr != nil {
			return result, softErr
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := loadMaterializedCandidateAuthorContext(ctx, service.dependencies.Profiles, request.Viewer.UserID, &profile, softHydrated, loadedAuthors, service.config.Recommendation); err != nil {
			return result, err
		}
		rankedSoft := RankCandidates(profileFeatures(profile), softHydrated, request.Now, rankingConfig(service.config.Recommendation), languageContext)
		selected = SelectCandidates(SelectionInput{
			Candidates: rankedSoft, Initial: selected, Limit: request.Limit, Now: request.Now, Phase: SelectionPhaseSoft,
			RequestID: request.RequestID, Config: selectionConfig(service.config.Recommendation),
		})
		finalSet = mergeCandidateSets(freshSet, softSet, candidateCaps(profile, service.config.Recommendation).Merged)
	}
	result.Profile = profile
	result.LanguageContext = languageContext
	result.CandidateSummary = finalSet
	result.FreshCandidateSummary = freshSet
	result.Selected = selected
	return result, nil
}

func (service *RecommendationService) executeGuest(ctx context.Context, request ServeRequest, servingVersion string, served ServedHistory, observe bool) (ServeResult, error) {
	result := ServeResult{Profile: Profile{}, EmbeddingVersion: servingVersion}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	cfg := service.config.Recommendation
	languageContext := BuildLanguageContext(request.BrowserLanguage, LanguagePrior{}, 0, languageConfig(cfg))
	freshSet, err := buildPublicCandidateSet(ctx, service.dependencies.Candidates, request.Now, cfg, freshGuestExclusions(served))
	if err != nil {
		return result, err
	}
	result.RecallSets = append(result.RecallSets, freshSet)
	service.recordRecallMetrics(freshSet, observe)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	hydrated, err := service.dependencies.Candidates.HydrateCandidates(ctx, servingVersion, freshSet.Candidates, request.Now)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	ranked := RankCandidates(ProfileFeatures{}, hydrated, request.Now, rankingConfig(cfg), languageContext)
	diversified := DiversifyPublicCandidates(ranked, request.Limit, request.RequestID)
	selected := SelectCandidates(SelectionInput{
		Candidates: diversified, Limit: request.Limit, Now: request.Now, Phase: SelectionPhaseFresh,
		RequestID: request.RequestID, Config: selectionConfig(cfg),
	})
	finalSet := freshSet
	if len(selected) < request.Limit {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		fallbackSet, fallbackErr := buildPublicCandidateSet(ctx, service.dependencies.Candidates, request.Now, cfg, fallbackGuestExclusions(served, selected))
		if fallbackErr != nil {
			return result, fallbackErr
		}
		result.RecallSets = append(result.RecallSets, fallbackSet)
		service.recordRecallMetrics(fallbackSet, observe)
		fallbackHydrated, fallbackErr := service.dependencies.Candidates.HydrateCandidates(ctx, servingVersion, fallbackSet.Candidates, request.Now)
		if fallbackErr != nil {
			return result, fallbackErr
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		annotateGuestFallbackServedState(fallbackHydrated, served)
		rankedFallback := RankCandidates(ProfileFeatures{}, fallbackHydrated, request.Now, rankingConfig(cfg), languageContext)
		selected = SelectCandidates(SelectionInput{
			Candidates: rankedFallback, Initial: selected, Limit: request.Limit, Now: request.Now, Phase: SelectionPhaseFresh,
			RequestID: request.RequestID, Config: selectionConfig(cfg),
		})
		if len(selected) < request.Limit {
			selected = SelectCandidates(SelectionInput{
				Candidates: rankedFallback, Initial: selected, Limit: request.Limit, Now: request.Now, Phase: SelectionPhaseSoft,
				RequestID: request.RequestID, Config: selectionConfig(cfg),
			})
		}
		finalSet = mergeCandidateSets(freshSet, fallbackSet, cfg.Candidates.ColdStart.Merged)
	}
	result.LanguageContext = languageContext
	result.CandidateSummary = finalSet
	result.FreshCandidateSummary = freshSet
	result.Selected = selected
	return result, nil
}

func (service *RecommendationService) loadProfile(ctx context.Context, userID uint, servingVersion string, now time.Time, observe bool) (Profile, error) {
	repository := service.dependencies.Profiles
	if repository == nil {
		if observe {
			service.metrics.RecordProfileLoad("error")
		}
		return Profile{}, errors.New("recommendation profile repository is nil")
	}
	cfg := service.config.Recommendation
	loaded, err := repository.Load(ctx, ProfileLoadQuery{
		UserID: userID, EmbeddingVersion: servingVersion,
		ExpectedProfileVersion:    MaterializedProfileVersion,
		ExpectedProfileConfigHash: ProfileConfigHash(cfg, servingVersion),
		Now:                       now, NegativeConfidenceHalfLifeDays: cfg.SignalHalfLifeDays,
		NegativeConfidenceSaturation: cfg.NegativeConfidenceSaturationScale,
	})
	if observe {
		status := loaded.Profile.ProfileStatus
		if err != nil {
			status = "error"
		}
		service.metrics.RecordProfileLoad(status)
		if loaded.RecoveryError != nil {
			log.Printf("[RecommendationProfile] queue recovery user=%d reason=%s: %v", userID, loaded.RecoveryReason, loaded.RecoveryError)
			service.metrics.RecordProfileLoad("error")
		}
		if err == nil && (loaded.Profile.ProfileStatus == ProfileStatusHit || loaded.Profile.ProfileStatus == ProfileStatusStale) {
			service.metrics.ObserveProfileAge(time.Duration(loaded.Profile.ProfileAgeMS) * time.Millisecond)
		}
	}
	return loaded.Profile, err
}

func (service *RecommendationService) loadHistory(ctx context.Context, request ServeRequest) (ServedHistory, error) {
	history := make(ServedHistory)
	store := service.dependencies.History
	switch request.Viewer.Kind {
	case ViewerAuthenticated:
		if store == nil {
			service.metrics.RecordHistoryLoadFailure(ViewerAuthenticated)
			log.Printf("[Recommendation] served history store is unavailable for user %d", request.Viewer.UserID)
			return history, nil
		}
		loaded, err := store.LoadUserHistory(ctx, request.Viewer.UserID, userHistoryWindow(request.Now, service.config.Recommendation))
		if err != nil {
			if ctx.Err() != nil {
				return history, ctx.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return history, err
			}
			service.metrics.RecordHistoryLoadFailure(ViewerAuthenticated)
			log.Printf("[Recommendation] served history for user %d: %v", request.Viewer.UserID, err)
			return history, nil
		}
		return loaded, nil
	case ViewerGuest:
		if request.Viewer.GuestSessionID == "" || store == nil {
			return history, nil
		}
		loaded, err := store.LoadGuestHistory(ctx, request.Viewer.GuestSessionID, guestHistoryWindow(request.Now, service.config.Recommendation))
		if err != nil {
			if ctx.Err() != nil {
				return history, ctx.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return history, err
			}
			log.Printf("[Recommendation] guest served-history load failed: %v", err)
			return history, nil
		}
		return loaded, nil
	default:
		return history, nil
	}
}

func (service *RecommendationService) recordHistory(ctx context.Context, request ServeRequest, selected []SelectedCandidate) {
	postIDs := selectedPostIDs(selected)
	if len(postIDs) == 0 || service.dependencies.History == nil {
		return
	}
	var err error
	switch request.Viewer.Kind {
	case ViewerAuthenticated:
		err = service.dependencies.History.RecordUserServed(ctx, request.Viewer.UserID, postIDs, userHistoryWindow(request.Now, service.config.Recommendation))
		if err != nil {
			log.Printf("[Recommendation] user served-history persist failed for user %d: %v", request.Viewer.UserID, err)
		}
	case ViewerGuest:
		if request.Viewer.GuestSessionID == "" {
			return
		}
		err = service.dependencies.History.RecordGuestServed(ctx, request.Viewer.GuestSessionID, postIDs, guestHistoryWindow(request.Now, service.config.Recommendation))
		if err != nil {
			log.Printf("[Recommendation] guest served-history persist failed: %v", err)
		}
	}
}

func (service *RecommendationService) persistTrace(ctx context.Context, request ServeRequest, result ServeResult, started time.Time) {
	requestRecord := buildRecommendationRequest(request, result, started, service.config.Recommendation)
	traces := buildResultTraces(requestRecord, result.Selected, request.Now, service.config.Recommendation)
	if service.dependencies.Traces == nil {
		service.metrics.RecordTracePersistFailure()
		log.Printf("[RecommendationTelemetry] persist serving trace %s: recommendation trace repository is nil", request.RequestID)
		return
	}
	traceCtx, cancel := context.WithTimeout(ctx, service.config.TracePersistTimeout)
	defer cancel()
	if err := service.dependencies.Traces.PersistServing(traceCtx, requestRecord, traces); err != nil {
		service.metrics.RecordTracePersistFailure()
		log.Printf("[RecommendationTelemetry] persist serving trace %s: %v", request.RequestID, err)
	}
}

func (service *RecommendationService) recordRecallMetrics(set CandidateSetSummary, observe bool) {
	if !observe {
		return
	}
	service.metrics.AddRecallCandidates("semantic", set.SemanticCount)
	service.metrics.AddRecallCandidates("following", set.FollowingCount)
	service.metrics.AddRecallCandidates("recent", set.RecentCount)
	service.metrics.AddRecallCandidates("trending", set.TrendingCount)
	service.metrics.AddRecallCandidates("merged", len(set.Candidates))
}

func (service *RecommendationService) recordResultMetrics(selected []SelectedCandidate) {
	for _, item := range selected {
		if item.Candidate.FromSemantic {
			service.metrics.AddResultsBySource("semantic", 1)
		}
		if item.Candidate.FromFollowing {
			service.metrics.AddResultsBySource("following", 1)
		}
		if item.Candidate.FromRecent {
			service.metrics.AddResultsBySource("recent", 1)
		}
		if item.Candidate.FromTrending {
			service.metrics.AddResultsBySource("trending", 1)
		}
		if item.IsInNetwork {
			service.metrics.AddResultsByClass("in_network", 1)
		} else {
			service.metrics.AddResultsByClass("out_of_network", 1)
		}
		if item.IsNovelAuthor {
			service.metrics.AddResultsByClass("novel_author", 1)
		}
		if item.Candidate.WasSoftServed {
			service.metrics.AddResultsByClass("soft_served_fallback", 1)
		}
		if item.SelectionMode == SelectionModeExploration {
			service.metrics.AddResultsBySelection("exploration", string(item.ExplorationReason), 1)
		} else {
			service.metrics.AddResultsBySelection("ranked", "none", 1)
		}
	}
}

func (service *RecommendationService) recordFailedRequest(viewer Viewer, err error, profile Profile) {
	service.metrics.RecordRequest(contextOutcome(err), serviceStrategyFor(viewer, profile))
}

func contextOutcome(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "error"
}

func serviceStrategyFor(viewer Viewer, profile Profile) string {
	if viewer.Kind == ViewerGuest || (len(profile.PositiveVector) == 0 && profile.ProfileStatus == ProfileStatusMiss) {
		return RecommendationColdStartStrategyID
	}
	return RecommendationPersonalizedStrategyID
}

func personalizationMode(profile Profile, followingCount int) string {
	if len(profile.PositiveVector) > 0 {
		return "semantic_social"
	}
	if followingCount > 0 {
		return "social_only"
	}
	return "cold_start"
}

func fallbackReason(signalCount, resultCount, requestedLimit int) string {
	if signalCount == 0 {
		return "no_positive_profile"
	}
	if resultCount < requestedLimit {
		return "insufficient_fresh_candidates"
	}
	return ""
}

func selectedPostIDs(selected []SelectedCandidate) []uint {
	ids := make([]uint, 0, len(selected))
	for _, item := range selected {
		if item.Post.ID != 0 {
			ids = append(ids, item.Post.ID)
		}
	}
	return ids
}

func normalizeRecommendationLimit(limit int) int {
	if limit <= 0 {
		return defaultRecommendationLimit
	}
	if limit > maxRecommendationLimit {
		return maxRecommendationLimit
	}
	return limit
}

func userHistoryWindow(now time.Time, cfg config.RecommendationConfig) HistoryWindow {
	hardMinutes := cfg.ServedHardExclusionMinutes
	if hardMinutes <= 0 {
		hardMinutes = 30
	}
	softDays := cfg.ServedSoftLookbackDays
	if softDays <= 0 {
		softDays = 7
	}
	limit := cfg.ServedHistoryLimit
	if limit <= 0 {
		limit = 1000
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return HistoryWindow{
		Now: now, HardStart: now.Add(-time.Duration(hardMinutes) * time.Minute),
		SoftStart: now.AddDate(0, 0, -softDays), Limit: limit,
		TTL: time.Duration(softDays+1) * 24 * time.Hour,
	}
}

func guestHistoryWindow(now time.Time, cfg config.RecommendationConfig) HistoryWindow {
	hardMinutes := cfg.ServedHardExclusionMinutes
	if hardMinutes <= 0 {
		hardMinutes = 30
	}
	softDays := cfg.ServedSoftLookbackDays
	if softDays <= 0 {
		softDays = 7
	}
	limit := cfg.GuestServedHistoryLimit
	if limit <= 0 {
		limit = 2000
	}
	ttlHours := cfg.GuestServedHistoryTTLHours
	if ttlHours <= 0 {
		ttlHours = 24
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return HistoryWindow{
		Now: now, HardStart: now.Add(-time.Duration(hardMinutes) * time.Minute),
		SoftStart: now.AddDate(0, 0, -softDays), Limit: limit, TTL: time.Duration(ttlHours) * time.Hour,
	}
}
