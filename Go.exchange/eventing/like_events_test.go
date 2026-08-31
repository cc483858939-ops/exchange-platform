package eventing

import (
	"encoding/json"
	"testing"
	"time"

	"Go.exchange/config"
)

func TestLikeSnapshotEnvelopeHasStableIDAndTopic(t *testing.T) {
	first, err := NewLikeSnapshotEnvelope(7, 12, 9)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLikeSnapshotEnvelope(7, 99, 9)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "like-snapshot:7:9" || first.ID != second.ID {
		t.Fatalf("ids %q %q", first.ID, second.ID)
	}
	topic, err := TopicForEvent(config.KafkaConfig{LikeSnapshotTopic: "like-snapshot"}, first.Type)
	if err != nil || topic != "like-snapshot" {
		t.Fatalf("topic=%q err=%v", topic, err)
	}
}

func TestLikeBehaviorEnvelopeCarriesProjectionVersion(t *testing.T) {
	event, err := NewLikeBehaviorEnvelope("like-event:1-0", 3, 7, "unlike", 11, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	var payload UserBehaviorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if event.ID != "like-event:1-0" || event.Type != EventTypePostUnliked || payload.LikeVersion != 11 {
		t.Fatalf("event=%+v payload=%+v", event, payload)
	}
}
