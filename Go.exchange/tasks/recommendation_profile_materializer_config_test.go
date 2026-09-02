package tasks

import (
	"testing"

	"Go.exchange/config"
)

func TestRecommendationProfileMaterializerConfigDefaultsStaySynchronized(t *testing.T) {
	original := config.AppConfig
	config.AppConfig = nil
	t.Cleanup(func() { config.AppConfig = original })

	want := config.RecommendationBehaviorWeights{
		View: 0.25, Like: 4, Click: 1, QualifiedRead: 2.5,
		Reply: 5, QuickBounce: -2, NotInterested: -8,
	}
	if got := recommendationProfileMaterializerRecommendationConfig().BehaviorWeights; got != want {
		t.Fatalf("nil-AppConfig behavior weights=%#v, want %#v", got, want)
	}
	if got := recommendationProfileMaterializerRecommendationConfigWithoutApp().BehaviorWeights; got != want {
		t.Fatalf("without-AppConfig behavior weights=%#v, want %#v", got, want)
	}
}
