package dlq

import (
	"errors"
	"fmt"
	"strings"

	"Go.exchange/config"
	"Go.exchange/eventing"
)

type ReplaySafetyMode string

const (
	ReplaySafetyInbox     ReplaySafetyMode = "consumer_inbox"
	ReplaySafetyVersioned ReplaySafetyMode = "versioned"
	ReplaySafetyReconcile ReplaySafetyMode = "state_reconciliation"
)

type ReplayPolicy struct {
	Consumer    string
	SourceTopic string
	SafetyMode  ReplaySafetyMode
}

type ReplayPolicyRegistry struct {
	dlqTopic string
	byPair   map[string]ReplayPolicy
}

func NewReplayPolicyRegistry(cfg config.KafkaConfig) (ReplayPolicyRegistry, error) {
	if strings.TrimSpace(cfg.ConsumerDLQTopic) == "" {
		return ReplayPolicyRegistry{}, errors.New("consumer DLQ topic is required")
	}
	policies := []ReplayPolicy{
		{Consumer: "like_snapshot_projection", SourceTopic: cfg.LikeSnapshotTopic, SafetyMode: ReplaySafetyVersioned},
		{Consumer: "user_behavior_projection", SourceTopic: cfg.UserBehaviorTopic, SafetyMode: ReplaySafetyInbox},
		{Consumer: "recommendation_metrics", SourceTopic: cfg.RecommendationEventsTopic, SafetyMode: ReplaySafetyInbox},
		{Consumer: "post_embedding", SourceTopic: cfg.PostEmbeddingTopic, SafetyMode: ReplaySafetyReconcile},
		{Consumer: "notification_projection", SourceTopic: cfg.ActivityEventsTopic, SafetyMode: ReplaySafetyInbox},
	}
	registry := ReplayPolicyRegistry{dlqTopic: strings.TrimSpace(cfg.ConsumerDLQTopic), byPair: make(map[string]ReplayPolicy, len(policies))}
	for _, policy := range policies {
		policy.Consumer = strings.TrimSpace(policy.Consumer)
		policy.SourceTopic = strings.TrimSpace(policy.SourceTopic)
		if policy.Consumer == "" || policy.SourceTopic == "" {
			return ReplayPolicyRegistry{}, errors.New("replay policy consumer and source topic are required")
		}
		if policy.SourceTopic == registry.dlqTopic {
			return ReplayPolicyRegistry{}, errors.New("consumer DLQ topic cannot be a replay source topic")
		}
		if !validReplaySafetyMode(policy.SafetyMode) {
			return ReplayPolicyRegistry{}, fmt.Errorf("replay policy for %q has invalid safety mode %q", policy.Consumer, policy.SafetyMode)
		}
		key := replayPolicyKey(policy.Consumer, policy.SourceTopic)
		if _, exists := registry.byPair[key]; exists {
			return ReplayPolicyRegistry{}, fmt.Errorf("duplicate replay policy for consumer %q and source topic %q", policy.Consumer, policy.SourceTopic)
		}
		registry.byPair[key] = policy
	}
	return registry, nil
}

func (r ReplayPolicyRegistry) Policies() []ReplayPolicy {
	policies := make([]ReplayPolicy, 0, len(r.byPair))
	for _, policy := range r.byPair {
		policies = append(policies, policy)
	}
	return policies
}

func (r ReplayPolicyRegistry) Validate(record eventing.DeadLetterRecord) (ReplayPolicy, error) {
	if err := eventing.ValidateDeadLetterRecord(record); err != nil {
		return ReplayPolicy{}, err
	}
	consumer := strings.TrimSpace(record.Consumer)
	topic := strings.TrimSpace(record.Source.Topic)
	if topic == "" {
		return ReplayPolicy{}, errors.New("dead letter source topic is empty")
	}
	if topic == r.dlqTopic {
		return ReplayPolicy{}, errors.New("consumer DLQ topic cannot be replayed as a source topic")
	}
	policy, ok := r.byPair[replayPolicyKey(consumer, topic)]
	if !ok {
		for _, known := range r.byPair {
			if known.Consumer == consumer {
				return ReplayPolicy{}, fmt.Errorf("source topic %q does not match replay policy for consumer %q", topic, consumer)
			}
		}
		return ReplayPolicy{}, fmt.Errorf("unknown replay consumer %q", consumer)
	}
	if !validReplaySafetyMode(policy.SafetyMode) {
		return ReplayPolicy{}, fmt.Errorf("replay safety mode %q is not supported", policy.SafetyMode)
	}
	return policy, nil
}

func replayPolicyKey(consumer, topic string) string { return consumer + "\x00" + topic }

func validReplaySafetyMode(mode ReplaySafetyMode) bool {
	switch mode {
	case ReplaySafetyInbox, ReplaySafetyVersioned, ReplaySafetyReconcile:
		return true
	default:
		return false
	}
}
