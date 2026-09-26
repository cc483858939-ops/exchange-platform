package controllers

import (
	"context"
	"errors"
	"time"

	"Go.exchange/models"
	"Go.exchange/recommendation"
)

type recommendationFeedbackEvent struct {
	EventID     string
	PostID      uint
	EventType   string
	OccurredAt  time.Time
	ReceivedAt  time.Time
	ReadOutcome *string
}

type recommendationFeedbackSignal struct {
	Event recommendationFeedbackEvent
}

type recommendationReactionState struct {
	Liked          bool
	StateChangedAt time.Time
}

type userPostSignal struct {
	SignalType string
	OccurredAt time.Time
}

type userPostOutcome struct {
	PostID          uint
	PositiveSignals []userPostSignal
	NegativeSignal  *userPostSignal
	PassiveSignal   *userPostSignal
}

const (
	recommendationFeedbackEventTypeClick         = models.RecommendationEventTypeClick
	recommendationFeedbackEventTypeReadEnd       = models.RecommendationEventTypeReadEnd
	recommendationFeedbackEventTypeNotInterested = models.RecommendationEventTypeNotInterested
)

var loadRecommendationBehaviorSignals = func(ctx context.Context, repository recommendation.SourceRepository, userID uint) ([]postBehaviorSignal, error) {
	if repository == nil {
		return nil, errors.New("recommendation source repository is nil")
	}
	sources, err := repository.LoadSourceSignals(ctx, userID, time.Time{})
	if err != nil {
		return nil, err
	}
	result := make([]postBehaviorSignal, 0, len(sources.Behaviors))
	for _, behavior := range sources.Behaviors {
		result = append(result, postBehaviorSignal{Behavior: behavior})
	}
	return result, nil
}

var loadRecommendationFeedbackSignals = func(ctx context.Context, repository recommendation.SourceRepository, userID uint, lookbackStart time.Time) ([]recommendationFeedbackSignal, error) {
	if repository == nil {
		return nil, errors.New("recommendation source repository is nil")
	}
	sources, err := repository.LoadSourceSignals(ctx, userID, lookbackStart)
	if err != nil {
		return nil, err
	}
	result := make([]recommendationFeedbackSignal, 0, len(sources.Feedback))
	for _, source := range sources.Feedback {
		result = append(result, recommendationFeedbackSignal{Event: recommendationFeedbackEvent{
			EventID: source.EventID, PostID: source.PostID, EventType: source.EventType,
			OccurredAt: source.OccurredAt, ReceivedAt: source.ReceivedAt, ReadOutcome: source.ReadOutcome,
		}})
	}
	return result, nil
}

var loadRecommendationReactionStates = func(ctx context.Context, repository recommendation.SourceRepository, userID uint) (map[uint]recommendationReactionState, error) {
	if repository == nil {
		return nil, errors.New("recommendation source repository is nil")
	}
	sources, err := repository.LoadSourceSignals(ctx, userID, time.Time{})
	if err != nil {
		return nil, err
	}
	states := make(map[uint]recommendationReactionState, len(sources.Reactions))
	for postID, reaction := range sources.Reactions {
		if postID != 0 {
			states[postID] = recommendationReactionState{Liked: reaction.Liked, StateChangedAt: reaction.StateChangedAt}
		}
	}
	return states, nil
}

func normalizeRecommendationFeedbackSignal(event recommendationFeedbackEvent) string {
	switch event.EventType {
	case recommendationFeedbackEventTypeClick:
		return "click"
	case recommendationFeedbackEventTypeNotInterested:
		return "not_interested"
	case recommendationFeedbackEventTypeReadEnd:
		if event.ReadOutcome == nil {
			return "neutral_read"
		}
		switch *event.ReadOutcome {
		case recommendationReadOutcomeQualified:
			return "qualified_read"
		case recommendationReadOutcomeQuickBounce:
			return "quick_bounce"
		default:
			return "neutral_read"
		}
	default:
		return ""
	}
}

func recommendationEventAfter(candidate, current recommendationFeedbackEvent) bool {
	if !candidate.OccurredAt.Equal(current.OccurredAt) {
		return candidate.OccurredAt.After(current.OccurredAt)
	}
	if !candidate.ReceivedAt.Equal(current.ReceivedAt) {
		return candidate.ReceivedAt.After(current.ReceivedAt)
	}
	return candidate.EventID > current.EventID
}

func setLatestRecommendationEvent(target **recommendationFeedbackEvent, candidate recommendationFeedbackEvent) {
	if *target == nil || recommendationEventAfter(candidate, **target) {
		candidateCopy := candidate
		*target = &candidateCopy
	}
}

type recommendationPostFeedbackState struct {
	Click         *recommendationFeedbackEvent
	ReadEnd       *recommendationFeedbackEvent
	NotInterested *recommendationFeedbackEvent
}

func resolveRecommendationPassiveOutcome(state *recommendationPostFeedbackState, view models.PostBehavior) (string, time.Time) {
	var click, readEnd *recommendationFeedbackEvent
	if state != nil {
		click, readEnd = state.Click, state.ReadEnd
	}
	if readEnd != nil {
		if click != nil && click.OccurredAt.After(readEnd.OccurredAt) {
			return "click", click.OccurredAt
		}
		if view.PostID != 0 && view.LastSeenAt.After(readEnd.OccurredAt) {
			return "view", view.LastSeenAt
		}
		return normalizeRecommendationFeedbackSignal(*readEnd), readEnd.OccurredAt
	}
	if click != nil {
		return "click", click.OccurredAt
	}
	if view.PostID != 0 {
		return "view", view.LastSeenAt
	}
	return "", time.Time{}
}

func canonicalizeRecommendationOutcomes(behaviors []postBehaviorSignal, feedback []recommendationFeedbackSignal, reactions map[uint]recommendationReactionState) []userPostOutcome {
	behaviorRows := make([]models.PostBehavior, 0, len(behaviors))
	for _, item := range behaviors {
		behaviorRows = append(behaviorRows, item.Behavior)
	}
	feedbackRows := make([]recommendation.FeedbackEvent, 0, len(feedback))
	for _, item := range feedback {
		feedbackRows = append(feedbackRows, recommendation.FeedbackEvent{
			EventID: item.Event.EventID, PostID: item.Event.PostID, EventType: item.Event.EventType,
			OccurredAt: item.Event.OccurredAt, ReceivedAt: item.Event.ReceivedAt, ReadOutcome: item.Event.ReadOutcome,
		})
	}
	reactionRows := make(map[uint]recommendation.ReactionState, len(reactions))
	for postID, reaction := range reactions {
		reactionRows[postID] = recommendation.ReactionState{Liked: reaction.Liked, StateChangedAt: reaction.StateChangedAt}
	}
	result := recommendation.CanonicalizeOutcomes(behaviorRows, feedbackRows, reactionRows)
	outcomes := make([]userPostOutcome, 0, len(result.Outcomes))
	for _, item := range result.Outcomes {
		outcome := userPostOutcome{PostID: item.PostID}
		for _, signal := range item.PositiveSignals {
			outcome.PositiveSignals = append(outcome.PositiveSignals, userPostSignal{SignalType: signal.SignalType, OccurredAt: signal.OccurredAt})
		}
		if item.NegativeSignal != nil {
			outcome.NegativeSignal = &userPostSignal{SignalType: item.NegativeSignal.SignalType, OccurredAt: item.NegativeSignal.OccurredAt}
		}
		if item.PassiveSignal != nil {
			outcome.PassiveSignal = &userPostSignal{SignalType: item.PassiveSignal.SignalType, OccurredAt: item.PassiveSignal.OccurredAt}
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}
