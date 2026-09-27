package recommendation

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"gorm.io/gorm"
)

func testRecommendationTrackingClaims(now time.Time) TrackingClaims {
	return TrackingClaims{
		UserID: 7, RequestID: "550e8400-e29b-41d4-a716-446655440000", PostID: 11,
		Position: 2, Scene: recommendationScene, RankerVersion: RankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: RecommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: RecommendationReadPolicyVersion,
		SelectionMode: string(SelectionModeRanked),
	}
}

func TestRecommendationRankerConfigHashIncludesV6ServingSettings(t *testing.T) {
	base := serviceTestConfig()
	tests := []struct {
		name   string
		mutate func(*config.RecommendationConfig)
	}{
		{name: "semantic recent window", mutate: func(cfg *config.RecommendationConfig) { cfg.SemanticRecall.RecentWindowDays++ }},
		{name: "semantic recent ratio", mutate: func(cfg *config.RecommendationConfig) { cfg.SemanticRecall.RecentRatio = 0.75 }},
		{name: "behavior like weight", mutate: func(cfg *config.RecommendationConfig) { cfg.BehaviorWeights.Like++ }},
		{name: "trending weight", mutate: func(cfg *config.RecommendationConfig) { cfg.TrendingWeight++ }},
		{name: "trending max age", mutate: func(cfg *config.RecommendationConfig) { cfg.Trending.MaxAgeDays++ }},
		{name: "trending half life", mutate: func(cfg *config.RecommendationConfig) { cfg.Trending.HalfLifeHours++ }},
		{name: "trending reply factor", mutate: func(cfg *config.RecommendationConfig) { cfg.Trending.ReplyFactor++ }},
		{name: "personalized trending cap", mutate: func(cfg *config.RecommendationConfig) { cfg.Candidates.Personalized.Trending++ }},
		{name: "cold-start trending cap", mutate: func(cfg *config.RecommendationConfig) { cfg.Candidates.ColdStart.Trending++ }},
		{name: "fusion rank constant", mutate: func(cfg *config.RecommendationConfig) { cfg.Fusion.RankConstant++ }},
		{name: "exploration ratio", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.Ratio = 0.20 }},
		{name: "exploration max slots", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.MaxSlots++ }},
		{name: "exploration recent window", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.RecentWindowDays++ }},
		{name: "exploration novel age", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.NovelPostMaxAgeDays++ }},
		{name: "language enabled", mutate: func(cfg *config.RecommendationConfig) { cfg.LanguageAffinity.Enabled = !cfg.LanguageAffinity.Enabled }},
		{name: "language weight", mutate: func(cfg *config.RecommendationConfig) { cfg.LanguageAffinity.Weight += 0.1 }},
		{name: "language evidence scale", mutate: func(cfg *config.RecommendationConfig) { cfg.LanguageAffinity.EvidenceSaturationScale++ }},
		{name: "language max behavior share", mutate: func(cfg *config.RecommendationConfig) { cfg.LanguageAffinity.MaxBehaviorShare = 0.9 }},
	}

	baseHash := RankerConfigHash(base, "post_embedding_v1")
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			tc.mutate(&mutated)
			if got := RankerConfigHash(mutated, "post_embedding_v1"); got == baseHash {
				t.Fatalf("hash=%q unchanged from base %q", got, baseHash)
			}
		})
	}
}

func TestRecommendationRankerConfigHashExplorationDoesNotChangeProfileHash(t *testing.T) {
	base := serviceTestConfig()
	mutated := base
	mutated.Exploration.Ratio = 0.20
	mutated.Exploration.MaxSlots++
	mutated.Exploration.RecentWindowDays++
	mutated.Exploration.NovelPostMaxAgeDays++
	mutated.LanguageAffinity.Enabled = false
	mutated.LanguageAffinity.Weight = 0.1
	mutated.LanguageAffinity.EvidenceSaturationScale = 9
	mutated.LanguageAffinity.MaxBehaviorShare = 0.8
	if got, want := ProfileConfigHash(mutated, "post_embedding_v1"), ProfileConfigHash(base, "post_embedding_v1"); got != want {
		t.Fatalf("profile hash changed with exploration settings: got=%q want=%q", got, want)
	}
}

func TestRecommendationRankerConfigHashIncludesServingVersion(t *testing.T) {
	cfg := serviceTestConfig()
	v1 := RankerConfigHash(cfg, "post_embedding_v1")
	v2 := RankerConfigHash(cfg, "post_embedding_v2")
	if v1 == v2 {
		t.Fatalf("serving-version change did not change ranker hash: %q", v1)
	}
}

func tamperRecommendationTrackingClaim(token string, oldValue, newValue string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return token + "tampered"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return token + "tampered"
	}
	payload = bytes.Replace(payload, []byte(oldValue), []byte(newValue), 1)
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	return strings.Join(parts, ".")
}

func TestRecommendationTrackingTokenV3RoundTripAndClaimBinding(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	claims := testRecommendationTrackingClaims(now)
	token, err := SignTrackingClaims(claims, key)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := VerifyTrackingToken(token, key)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != claims {
		t.Fatalf("decoded claims=%#v want %#v", decoded, claims)
	}
	if _, err := VerifyTrackingToken(tamperRecommendationTrackingClaim(token, "\"estimated_read_time_ms\":3000", "\"estimated_read_time_ms\":4000"), key); err == nil {
		t.Fatal("estimated read time tampering should invalidate signature")
	}
	if _, err := VerifyTrackingToken(tamperRecommendationTrackingClaim(token, "\"read_policy_version\":\"read_v1\"", "\"read_policy_version\":\"read_v2\""), key); err == nil {
		t.Fatal("read policy tampering should invalidate signature")
	}
	v2 := strings.Replace(token, "v3.", "v2.", 1)
	if _, err := VerifyTrackingToken(v2, key); err == nil {
		t.Fatal("V2 token should be rejected")
	}
	missing := claims
	missing.EstimatedReadTimeMS = 0
	missingToken, err := SignTrackingClaims(missing, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrackingToken(missingToken, key); err == nil {
		t.Fatal("missing V3 estimated read time should be rejected")
	}
}

func TestRecommendationTrackingTokenV3RejectsInvalidProvenanceStates(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	for _, mutate := range []func(*TrackingClaims){
		func(claims *TrackingClaims) {
			claims.SelectionMode = string(SelectionModeExploration)
			claims.ExplorationReason = ExplorationReasonRecent
		},
		func(claims *TrackingClaims) {
			claims.ExplorationOpportunity = true
			claims.SelectionMode = string(SelectionModeRanked)
			claims.ExplorationReason = ExplorationReasonRecent
		},
		func(claims *TrackingClaims) {
			claims.ExplorationOpportunity = true
			claims.SelectionMode = string(SelectionModeExploration)
			claims.ExplorationReason = "unsupported"
		},
	} {
		claims := testRecommendationTrackingClaims(now)
		mutate(&claims)
		token, err := SignTrackingClaims(claims, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyTrackingToken(token, key); err == nil {
			t.Fatalf("invalid provenance state accepted: %#v", claims)
		}
	}
}

func TestRecommendationTrackingAcceptsRankedExplorationOpportunity(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	want := testRecommendationTrackingClaims(now)
	want.ExplorationOpportunity = true
	want.SelectionMode = string(SelectionModeRanked)
	want.ExplorationReason = ""
	token, err := SignTrackingClaims(want, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyTrackingToken(token, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded ranked opportunity claims=%#v want=%#v", got, want)
	}
}

func TestRecommendationTrackingTokenV3RejectsCompleteInvalidProvenanceMatrix(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	invalid := []struct {
		name   string
		mutate func(*TrackingClaims)
	}{
		{name: "false exploration recent", mutate: func(claims *TrackingClaims) {
			claims.SelectionMode = string(SelectionModeExploration)
			claims.ExplorationReason = ExplorationReasonRecent
		}},
		{name: "ranked recent", mutate: func(claims *TrackingClaims) {
			claims.ExplorationOpportunity = true
			claims.SelectionMode = string(SelectionModeRanked)
			claims.ExplorationReason = ExplorationReasonRecent
		}},
		{name: "exploration empty reason", mutate: func(claims *TrackingClaims) {
			claims.ExplorationOpportunity = true
			claims.SelectionMode = string(SelectionModeExploration)
		}},
		{name: "exploration unsupported reason", mutate: func(claims *TrackingClaims) {
			claims.ExplorationOpportunity = true
			claims.SelectionMode = string(SelectionModeExploration)
			claims.ExplorationReason = "unsupported"
		}},
		{name: "unknown mode", mutate: func(claims *TrackingClaims) {
			claims.SelectionMode = "unknown"
		}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			claims := testRecommendationTrackingClaims(now)
			tc.mutate(&claims)
			token, err := SignTrackingClaims(claims, key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyTrackingToken(token, key); err == nil {
				t.Fatalf("invalid provenance accepted: %#v", claims)
			}
		})
	}
}

func TestRecommendationServingVersionsRemainRRFV6Contract(t *testing.T) {
	if RankerVersion != "rules_v6" {
		t.Fatalf("ranker version=%q", RankerVersion)
	}
	if RecommendationPersonalizedStrategyID != "for_you_materialized_profile_v6" || RecommendationColdStartStrategyID != RecommendationPersonalizedStrategyID {
		t.Fatalf("strategy versions personalized=%q cold-start=%q", RecommendationPersonalizedStrategyID, RecommendationColdStartStrategyID)
	}
	if SelectionPolicyVersion != "network_balance_exploration_v2" {
		t.Fatalf("selection policy=%q", SelectionPolicyVersion)
	}
	if RecommendationTrackingTokenVersion != "v3" || eventing.RecommendationBehaviorSchemaVersion != 3 {
		t.Fatalf("tracking=%q behavior schema=%d", RecommendationTrackingTokenVersion, eventing.RecommendationBehaviorSchemaVersion)
	}
	if CandidateRetrievalVersion != "social_semantic_materialized_profile_rrf_v5" || MaterializedProfileVersion != "materialized_profile_v1" {
		t.Fatalf("retrieval contract changed; materialized profile=%q", MaterializedProfileVersion)
	}
}

func TestBuildTrackingFactsBindsFinalPositionsAndReadClaims(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	requestID := "550e8400-e29b-41d4-a716-446655440001"
	selected := []SelectedCandidate{
		{Post: models.Post{Model: gorm.Model{ID: 11}, Content: "tiny"}, SelectionMode: SelectionModeRanked},
		{Post: models.Post{Model: gorm.Model{ID: 12}, Content: strings.Repeat("word ", 400)}, ExplorationOpportunity: true, SelectionMode: SelectionModeExploration, ExplorationReason: ExplorationReasonRecent, ExplorationSemantic: .8},
	}
	trackingConfig := TrackingConfig{
		Enabled: true, RolloutPercent: 100,
		SigningKey: []byte("0123456789abcdef0123456789abcdef"), TokenTTL: 24 * time.Hour,
	}
	profile := Profile{ProfileStatus: ProfileStatusMiss}
	servingResult := ServeResult{
		Viewer:    Viewer{Kind: ViewerAuthenticated, UserID: 7},
		RequestID: requestID, Now: now, EmbeddingVersion: "post_embedding_v1",
		StrategyID:       StrategyID(profile),
		RankerConfigHash: RankerConfigHash(serviceTestConfig(), "post_embedding_v1"),
		Selected:         selected,
	}
	facts, err := BuildTrackingFacts(servingResult, trackingConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != len(selected) {
		t.Fatalf("tracking count=%d selected=%d", len(facts), len(selected))
	}
	wantRankerHash := RankerConfigHash(serviceTestConfig(), "post_embedding_v1")
	if facts[0].RankerConfigHash != wantRankerHash || facts[1].RankerConfigHash != wantRankerHash {
		t.Fatalf("tracking ranker hashes=%q/%q want=%q", facts[0].RankerConfigHash, facts[1].RankerConfigHash, wantRankerHash)
	}
	if facts[0].Position != 1 || facts[1].Position != 2 {
		t.Fatalf("unexpected positions: %#v %#v", facts[0], facts[1])
	}
	claims, err := VerifyTrackingToken(facts[1].Token, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if claims.PostID != 12 || claims.Position != 2 || claims.StrategyID != RecommendationColdStartStrategyID ||
		!claims.ExplorationOpportunity || claims.SelectionMode != string(SelectionModeExploration) || claims.ExplorationReason != ExplorationReasonRecent ||
		claims.EstimatedReadTimeMS <= 0 || claims.ReadPolicyVersion != RecommendationReadPolicyVersion || claims.RankerConfigHash != wantRankerHash {
		t.Fatalf("unexpected V3 claims: %#v", claims)
	}
}
