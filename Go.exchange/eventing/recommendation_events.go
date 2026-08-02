package eventing

import (
	"errors"
	"strconv"
	"time"

	"Go.exchange/models"
)

type RecommendationEventFact struct {
	EventID          string    `json:"event_id"`
	UserID           uint      `json:"user_id"`
	RequestID        string    `json:"request_id"`
	ArticleID        uint      `json:"article_id"`
	EventType        string    `json:"event_type"`
	Scene            string    `json:"scene"`
	Position         int       `json:"position"`
	RankerVersion    string    `json:"ranker_version"`
	RankerConfigHash string    `json:"ranker_config_hash"`
	StrategyID       string    `json:"strategy_id"`
	OccurredAt       time.Time `json:"occurred_at"`
	ReceivedAt       time.Time `json:"received_at"`
}

type RecommendationEventsRecordedPayload struct {
	UserID uint                      `json:"user_id"`
	Events []RecommendationEventFact `json:"events"`
}

func NewRecommendationEventsRecorded(userID uint, events []models.RecommendationEvent) (models.OutboxEvent, error) {
	if userID == 0 || len(events) == 0 {
		return models.OutboxEvent{}, errors.New("recommendation event batch requires user and events")
	}
	facts := make([]RecommendationEventFact, 0, len(events))
	for _, event := range events {
		facts = append(facts, RecommendationEventFact{
			EventID: event.EventID, UserID: event.UserID, RequestID: event.RequestID,
			ArticleID: event.ArticleID, EventType: event.EventType, Scene: event.Scene,
			Position: event.Position, RankerVersion: event.RankerVersion,
			RankerConfigHash: event.RankerConfigHash, StrategyID: event.StrategyID,
			OccurredAt: event.OccurredAt, ReceivedAt: event.ReceivedAt,
		})
	}
	return NewOutboxEvent(
		EventTypeRecommendationEventsRecorded,
		"recommendation-telemetry-batch",
		strconv.FormatUint(uint64(userID), 10),
		RecommendationEventsRecordedPayload{UserID: userID, Events: facts},
	)
}
