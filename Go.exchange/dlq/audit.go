package dlq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const (
	ReplayStatusStarted   = "started"
	ReplayStatusSucceeded = "succeeded"
	ReplayStatusUnknown   = "unknown"
)

type AuditRepository interface {
	Start(context.Context, models.KafkaDLQReplay) error
	Complete(context.Context, string, string, time.Time, string) error
}

type GORMAuditRepository struct{ DB *gorm.DB }

func (r GORMAuditRepository) Start(ctx context.Context, replay models.KafkaDLQReplay) error {
	if ctx == nil {
		return errors.New("replay audit context is nil")
	}
	if r.DB == nil {
		return errors.New("replay audit database is not initialized")
	}
	if replay.ID == "" || replay.Status != ReplayStatusStarted {
		return errors.New("replay audit start requires an ID and started status")
	}
	return r.DB.WithContext(ctx).Create(&replay).Error
}

func (r GORMAuditRepository) Complete(ctx context.Context, replayID, status string, completedAt time.Time, errorMessage string) error {
	if ctx == nil {
		return errors.New("replay audit context is nil")
	}
	if r.DB == nil {
		return errors.New("replay audit database is not initialized")
	}
	if status != ReplayStatusSucceeded && status != ReplayStatusUnknown {
		return fmt.Errorf("invalid replay audit completion status %q", status)
	}
	if completedAt.IsZero() {
		return errors.New("replay audit completion time is required")
	}
	result := r.DB.WithContext(ctx).Model(&models.KafkaDLQReplay{}).
		Where("id = ? AND status = ?", replayID, ReplayStatusStarted).
		Updates(map[string]interface{}{"status": status, "completed_at": completedAt.UTC(), "error": errorMessage})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("replay audit row %q was not in started state", replayID)
	}
	return nil
}

type ExecutionResult struct {
	ReplayID         string
	Status           string
	PublisherInvoked bool
}

func ExecuteReplay(ctx context.Context, located LocatedRecord, plan ReplayPlan, replayID string, replayedAt time.Time, audit AuditRepository, publisher eventing.RawPublisher) (ExecutionResult, error) {
	result := ExecutionResult{ReplayID: strings.TrimSpace(replayID), Status: ReplayStatusStarted}
	if ctx == nil {
		return result, errors.New("replay context is nil")
	}
	if result.ReplayID == "" {
		return result, errors.New("replay ID is required")
	}
	if audit == nil {
		return result, errors.New("replay audit repository is required")
	}
	if publisher == nil {
		return result, errors.New("replay publisher is required")
	}
	message, err := BuildReplayMessage(located, result.ReplayID, replayedAt)
	if err != nil {
		return result, err
	}
	if plan.SourceTopic != located.Record.Source.Topic || plan.SourcePartition != located.Record.Source.Partition || plan.SourceOffset != located.Record.Source.Offset ||
		plan.Consumer != located.Record.Consumer || plan.EventID != located.Record.EventID ||
		plan.DLQTopic != located.DLQTopic || plan.DLQPartition != located.DLQPartition || plan.DLQOffset != located.DLQOffset ||
		plan.ErrorClass != located.Record.Failure.Class || plan.ErrorCode != located.Record.Failure.Code ||
		!plan.FailedAt.Equal(located.Record.Failure.FailedAt) || plan.KeySize != len(located.Record.Source.Key) ||
		plan.ValueSize != len(located.Record.Source.Value) || plan.HeaderCount != len(located.Record.Source.Headers) || !validReplaySafetyMode(plan.SafetyMode) {
		return result, errors.New("replay plan does not match the DLQ record")
	}
	auditRow := models.KafkaDLQReplay{
		ID: result.ReplayID, DLQTopic: plan.DLQTopic, DLQPartition: plan.DLQPartition, DLQOffset: plan.DLQOffset,
		Consumer: plan.Consumer, SourceTopic: plan.SourceTopic, SourcePartition: plan.SourcePartition, SourceOffset: plan.SourceOffset,
		EventID: plan.EventID, ErrorCode: plan.ErrorCode, Status: ReplayStatusStarted, StartedAt: replayedAt.UTC(),
	}
	if err := audit.Start(ctx, auditRow); err != nil {
		return result, fmt.Errorf("insert replay audit: %w", err)
	}
	result.PublisherInvoked = true
	if err := publisher.PublishRaw(ctx, plan.SourceTopic, message); err != nil {
		result.Status = ReplayStatusUnknown
		completeErr := audit.Complete(ctx, result.ReplayID, ReplayStatusUnknown, time.Now().UTC(), boundedAuditError(err))
		if completeErr != nil {
			return result, fmt.Errorf("replay publish outcome unknown: %v; record unknown audit status: %w", err, completeErr)
		}
		return result, fmt.Errorf("replay publish outcome unknown: %w", err)
	}
	if err := audit.Complete(ctx, result.ReplayID, ReplayStatusSucceeded, time.Now().UTC(), ""); err != nil {
		result.Status = ReplayStatusUnknown
		return result, fmt.Errorf("replay published but audit completion failed; audit row may remain started: %w", err)
	}
	result.Status = ReplayStatusSucceeded
	return result, nil
}

func boundedAuditError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > eventing.MaxDeadLetterReasonBytes {
		message = message[:eventing.MaxDeadLetterReasonBytes]
	}
	return message
}

var _ eventing.RawPublisher = (*eventing.KafkaPublisher)(nil)
var _ AuditRepository = GORMAuditRepository{}
