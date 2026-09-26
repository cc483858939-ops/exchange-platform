package controllers

import "Go.exchange/recommendation"

// These aliases keep serving and persistence tests focused on their controller
// boundaries while using the moved recommendation domain types.
type embeddingCandidate = recommendation.Candidate
type hydratedRecommendationCandidate = recommendation.RankedCandidate
type selectedRecommendation = recommendation.SelectedCandidate
type recommendationScoreBreakdown = recommendation.ScoreBreakdown
type recommendationLanguageContext = recommendation.LanguageContext
type recommendationLanguagePrior = recommendation.LanguagePrior
type recommendationRecallList = recommendation.CandidateSet
type recommendationRecallSource = recommendation.CandidateSource

const (
	recommendationSelectionFresh = recommendation.SelectionPhaseFresh
	recommendationSelectionSoft  = recommendation.SelectionPhaseSoft

	recommendationResultSelectionRanked              = recommendation.SelectionModeRanked
	recommendationResultSelectionExploration         = recommendation.SelectionModeExploration
	recommendationExplorationReasonRecent            = recommendation.ExplorationReasonRecent
	recommendationExplorationReasonNovelAuthor       = recommendation.ExplorationReasonNovelAuthor
	recommendationExplorationReasonRecentNovelAuthor = recommendation.ExplorationReasonRecentNovelAuthor

	recommendationRecallSourceSemantic   = recommendation.CandidateSourceSemantic
	recommendationRecallSourceFollowing  = recommendation.CandidateSourceFollowing
	recommendationRecallSourceRecent     = recommendation.CandidateSourceRecent
	recommendationRecallSourceTrending   = recommendation.CandidateSourceTrending
	recommendationLanguageUnd            = recommendation.LanguageUnd
	recommendationRankerVersion          = recommendation.RankerVersion
	recommendationSelectionPolicyVersion = recommendation.SelectionPolicyVersion
)
