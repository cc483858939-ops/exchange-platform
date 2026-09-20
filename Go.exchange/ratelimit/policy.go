package ratelimit

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnknownAction = errors.New("unknown rate-limit action")
	ErrInvalidPolicy = errors.New("invalid rate-limit policy")
)

type Rule struct {
	Limit  int64
	Window time.Duration
}

type Policy struct {
	Action Action
	Rules  []Rule
}

// Policies is the single source of truth for the initial application quotas.
// Keep the values deliberately loose until production traffic data is available.
var Policies = map[Action]Policy{
	ActionPostCreate: {
		Action: ActionPostCreate,
		Rules: []Rule{
			{Limit: 5, Window: time.Minute},
			{Limit: 50, Window: time.Hour},
			{Limit: 300, Window: 24 * time.Hour},
		},
	},
	ActionFollowMutation: {
		Action: ActionFollowMutation,
		Rules: []Rule{
			{Limit: 15, Window: time.Minute},
			{Limit: 100, Window: time.Hour},
			{Limit: 500, Window: 24 * time.Hour},
		},
	},
	ActionMediaUpload: {
		Action: ActionMediaUpload,
		Rules: []Rule{
			{Limit: 10, Window: time.Minute},
			{Limit: 100, Window: 24 * time.Hour},
		},
	},
	ActionTranslation: {
		Action: ActionTranslation,
		Rules: []Rule{
			{Limit: 20, Window: time.Minute},
			{Limit: 200, Window: time.Hour},
		},
	},
	ActionRecommendations: {
		Action: ActionRecommendations,
		Rules: []Rule{
			{Limit: 120, Window: time.Minute},
		},
	},
}

func PolicyFor(action Action) (Policy, error) {
	policy, ok := Policies[action]
	if !ok {
		return Policy{}, fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	if err := validatePolicy(policy); err != nil {
		return Policy{}, err
	}
	policy.Rules = append([]Rule(nil), policy.Rules...)
	return policy, nil
}

func ValidatePolicies() error {
	for action, policy := range Policies {
		if policy.Action == "" {
			policy.Action = action
		}
		if err := validatePolicy(policy); err != nil {
			return err
		}
	}
	return nil
}

func validatePolicy(policy Policy) error {
	if policy.Action == "" || len(policy.Rules) == 0 {
		return fmt.Errorf("%w: action %q must define at least one rule", ErrInvalidPolicy, policy.Action)
	}
	for index, rule := range policy.Rules {
		if rule.Limit <= 0 || rule.Window <= 0 {
			return fmt.Errorf("%w: action %q rule %d must have positive limit and window", ErrInvalidPolicy, policy.Action, index)
		}
		if rule.Window%time.Second != 0 {
			return fmt.Errorf("%w: action %q rule %d window must use whole seconds", ErrInvalidPolicy, policy.Action, index)
		}
	}
	return nil
}
