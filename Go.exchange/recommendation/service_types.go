package recommendation

import (
	"context"
	"time"

	"Go.exchange/config"
)

type ViewerKind uint8

const (
	ViewerAuthenticated ViewerKind = iota + 1
	ViewerGuest
)

type Viewer struct {
	Kind           ViewerKind
	UserID         uint
	GuestSessionID string
}

type ServeRequest struct {
	Viewer          Viewer
	Limit           int
	RequestID       string
	Now             time.Time
	BrowserLanguage LanguageContext
}

type CandidateSetSummary struct {
	Candidates     []Candidate
	SemanticCount  int
	FollowingCount int
	RecentCount    int
	RecentPostIDs  []uint
	TrendingCount  int
}

type ServeResult struct {
	Viewer                Viewer
	RequestID             string
	Now                   time.Time
	Limit                 int
	EmbeddingVersion      string
	Profile               Profile
	LanguageContext       LanguageContext
	CandidateSummary      CandidateSetSummary
	FreshCandidateSummary CandidateSetSummary
	RecallSets            []CandidateSetSummary
	Selected              []SelectedCandidate
	StrategyID            string
	RankerConfigHash      string
	PersonalizationMode   string
	FallbackReason        string
	Depleted              bool
	Tracking              []TrackingFact
}

type Service interface {
	Serve(ctx context.Context, request ServeRequest) (ServeResult, error)
}

type VerificationRequest struct {
	UserID          uint
	Limit           int
	Now             time.Time
	BrowserLanguage LanguageContext
}

type VerificationResult struct {
	RecentCandidateCount int
	RecentPostIDs        []uint
	FinalPostIDs         []uint
}

type VerificationService interface {
	Service
	Verify(ctx context.Context, request VerificationRequest) (VerificationResult, error)
}

type ServingVersionProvider interface {
	LoadServingVersion(ctx context.Context) (string, error)
}

type ServiceConfig struct {
	Recommendation      config.RecommendationConfig
	TracePersistTimeout time.Duration
	Tracking            TrackingConfig
}

type Metrics interface {
	RecordRequest(outcome, strategyID string)
	ObserveCandidateCount(count int)
	ObserveResultCount(count int)
	ObserveGenerationDuration(strategyID string, duration time.Duration)
	AddRecallCandidates(source string, count int)
	AddResultsBySource(source string, count int)
	AddResultsByClass(class string, count int)
	AddResultsBySelection(mode, reason string, count int)
	AddTrackingResults(status string, count int)
	RecordHistoryLoadFailure(viewer ViewerKind)
	RecordTracePersistFailure()
	RecordProfileLoad(status string)
	ObserveProfileAge(age time.Duration)
}

type NoopMetrics struct{}

func (NoopMetrics) RecordRequest(string, string)                    {}
func (NoopMetrics) ObserveCandidateCount(int)                       {}
func (NoopMetrics) ObserveResultCount(int)                          {}
func (NoopMetrics) ObserveGenerationDuration(string, time.Duration) {}
func (NoopMetrics) AddRecallCandidates(string, int)                 {}
func (NoopMetrics) AddResultsBySource(string, int)                  {}
func (NoopMetrics) AddResultsByClass(string, int)                   {}
func (NoopMetrics) AddResultsBySelection(string, string, int)       {}
func (NoopMetrics) AddTrackingResults(string, int)                  {}
func (NoopMetrics) RecordHistoryLoadFailure(ViewerKind)             {}
func (NoopMetrics) RecordTracePersistFailure()                      {}
func (NoopMetrics) RecordProfileLoad(string)                        {}
func (NoopMetrics) ObserveProfileAge(time.Duration)                 {}
