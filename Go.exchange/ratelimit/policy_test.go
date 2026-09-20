package ratelimit

import (
	"errors"
	"testing"
	"time"
)

func TestPoliciesUseExpectedInitialRules(t *testing.T) {
	want := map[Action][]Rule{
		ActionPostCreate:      {{5, time.Minute}, {50, time.Hour}, {300, 24 * time.Hour}},
		ActionFollowMutation:  {{15, time.Minute}, {100, time.Hour}, {500, 24 * time.Hour}},
		ActionMediaUpload:     {{10, time.Minute}, {100, 24 * time.Hour}},
		ActionTranslation:     {{20, time.Minute}, {200, time.Hour}},
		ActionRecommendations: {{120, time.Minute}},
	}
	if err := ValidatePolicies(); err != nil {
		t.Fatal(err)
	}
	for action, wantRules := range want {
		policy, err := PolicyFor(action)
		if err != nil {
			t.Fatalf("PolicyFor(%q): %v", action, err)
		}
		if len(policy.Rules) != len(wantRules) {
			t.Fatalf("PolicyFor(%q) returned %d rules, want %d", action, len(policy.Rules), len(wantRules))
		}
		for index, wantRule := range wantRules {
			if policy.Rules[index] != wantRule {
				t.Errorf("PolicyFor(%q) rule %d = %#v, want %#v", action, index, policy.Rules[index], wantRule)
			}
			if wantRule.Limit <= 0 || wantRule.Window <= 0 {
				t.Errorf("PolicyFor(%q) rule %d is not positive", action, index)
			}
		}
	}
}

func TestPolicyForRejectsUnknownAction(t *testing.T) {
	if _, err := PolicyFor(Action("unknown")); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("PolicyFor returned error %v, want ErrUnknownAction", err)
	}
}

func TestPolicyForRejectsInvalidPolicy(t *testing.T) {
	action := Action("test_invalid_policy")
	previous, existed := Policies[action]
	Policies[action] = Policy{Action: action, Rules: []Rule{{Limit: 0, Window: time.Minute}}}
	t.Cleanup(func() {
		if existed {
			Policies[action] = previous
		} else {
			delete(Policies, action)
		}
	})
	if _, err := PolicyFor(action); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("PolicyFor returned error %v, want ErrInvalidPolicy", err)
	}
}
