package recommendation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const (
	ProfileRecentViewLimit = 200
	ProfileReplyLimit      = 500
	PostBehaviorView       = "view"
	PostBehaviorReply      = "reply"
)

type FeedbackEvent struct {
	EventID     string
	PostID      uint
	EventType   string
	OccurredAt  time.Time
	ReceivedAt  time.Time
	ReadOutcome *string
}

type ReactionState struct {
	Liked          bool
	StateChangedAt time.Time
}

type SourceSignals struct {
	Behaviors []models.PostBehavior
	Feedback  []FeedbackEvent
	Reactions map[uint]ReactionState
}

type GormSourceRepository struct {
	db *gorm.DB
}

func NewGormSourceRepository(db *gorm.DB) (*GormSourceRepository, error) {
	if db == nil {
		return nil, errors.New("recommendation source repository database is nil")
	}
	return &GormSourceRepository{db: db}, nil
}

// LoadSourceSignals reads exactly the bounded source windows used by the
// canonical profile builder. Not-interested history is intentionally not
// bounded by the feedback lookback.
func (r *GormSourceRepository) LoadSourceSignals(ctx context.Context, userID uint, lookbackStart time.Time) (SourceSignals, error) {
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return SourceSignals{}, fmt.Errorf("load recommendation source signals: %w", err)
	}
	var views []models.PostBehavior
	if err := db.Where("user_id = ? AND action = ?", userID, PostBehaviorView).
		Order("last_seen_at DESC, id DESC").Limit(ProfileRecentViewLimit).Find(&views).Error; err != nil {
		return SourceSignals{}, fmt.Errorf("load recommendation views: %w", err)
	}
	var replies []models.PostBehavior
	if err := db.Where("user_id = ? AND action = ?", userID, PostBehaviorReply).
		Order("last_seen_at DESC, id DESC").Limit(ProfileReplyLimit).Find(&replies).Error; err != nil {
		return SourceSignals{}, fmt.Errorf("load recommendation replies: %w", err)
	}
	behaviors := make([]models.PostBehavior, 0, len(views)+len(replies))
	for _, row := range views {
		if row.PostID != 0 {
			behaviors = append(behaviors, row)
		}
	}
	for _, row := range replies {
		if row.PostID != 0 {
			behaviors = append(behaviors, row)
		}
	}

	actions := []string{
		eventing.RecommendationBehaviorActionClick,
		eventing.RecommendationBehaviorActionReadQualified,
		eventing.RecommendationBehaviorActionReadQuickBounce,
		eventing.RecommendationBehaviorActionReadNeutral,
	}
	var feedbackRows []models.PostBehavior
	if err := db.Where("user_id = ? AND ((action IN ? AND last_seen_at >= ?) OR action = ?)",
		userID, actions, lookbackStart, eventing.RecommendationBehaviorActionNotInterested).
		Order("last_seen_at DESC, id DESC").Find(&feedbackRows).Error; err != nil {
		return SourceSignals{}, fmt.Errorf("load recommendation feedback: %w", err)
	}
	feedback := make([]FeedbackEvent, 0, len(feedbackRows))
	for _, row := range feedbackRows {
		if row.PostID == 0 {
			continue
		}
		event := FeedbackEvent{
			EventID: strconv.FormatUint(uint64(row.ID), 10), PostID: row.PostID,
			OccurredAt: row.LastSeenAt, ReceivedAt: row.UpdatedAt,
		}
		if event.ReceivedAt.IsZero() {
			event.ReceivedAt = event.OccurredAt
		}
		switch row.Action {
		case eventing.RecommendationBehaviorActionClick:
			event.EventType = eventing.EventTypeRecommendationClick
		case eventing.RecommendationBehaviorActionReadQualified,
			eventing.RecommendationBehaviorActionReadQuickBounce,
			eventing.RecommendationBehaviorActionReadNeutral:
			event.EventType = eventing.EventTypeRecommendationReadEnd
			outcome := "neutral"
			switch row.Action {
			case eventing.RecommendationBehaviorActionReadQualified:
				outcome = "qualified"
			case eventing.RecommendationBehaviorActionReadQuickBounce:
				outcome = "quick_bounce"
			}
			event.ReadOutcome = &outcome
		case eventing.RecommendationBehaviorActionNotInterested:
			event.EventType = eventing.EventTypeRecommendationNotInterested
		default:
			continue
		}
		feedback = append(feedback, event)
	}

	var reactionRows []models.PostReaction
	if err := db.Where("user_id = ?", userID).Order("post_id ASC").Find(&reactionRows).Error; err != nil {
		return SourceSignals{}, fmt.Errorf("load recommendation reactions: %w", err)
	}
	reactions := make(map[uint]ReactionState, len(reactionRows))
	for _, row := range reactionRows {
		if row.PostID != 0 {
			reactions[row.PostID] = ReactionState{Liked: row.Liked, StateChangedAt: row.StateChangedAt}
		}
	}
	return SourceSignals{Behaviors: behaviors, Feedback: feedback, Reactions: reactions}, nil
}

var _ SourceRepository = (*GormSourceRepository)(nil)
