package tasks

import (
	"errors"
	"testing"

	"Go.exchange/eventing"
)

func TestApplyRecommendationMetricsEventClassifiesInvalidPayload(t *testing.T) {
	err := applyRecommendationMetricsEvent(eventing.Envelope{Payload: []byte(`{"user_id":`)})
	if !errors.Is(err, errInvalidRecommendationMetricsEvent) {
		t.Fatalf("expected invalid payload error, got %v", err)
	}
}
