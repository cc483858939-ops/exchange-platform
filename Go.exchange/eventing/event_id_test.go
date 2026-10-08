package eventing

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
)

func TestDecodeEnvelopeEventIDContract(t *testing.T) {
	for _, tc := range []struct {
		name, id, want string
		invalid        bool
	}{
		{name: "UUID", id: "61aa0755-0605-4a15-802f-b5bc55cb8bbb", want: "61aa0755-0605-4a15-802f-b5bc55cb8bbb"},
		{name: "legacy opaque", id: "view-audit", want: "view-audit"},
		{name: "case preserved", id: "View-Audit", want: "View-Audit"},
		{name: "legacy Unicode key", id: strings.Repeat("😀", 36), want: strings.Repeat("😀", 36)},
		{name: "UTF-8 character bound", id: strings.Repeat("界", 128), want: strings.Repeat("界", 128)},
		{name: "UTF-8 over character bound", id: strings.Repeat("界", 129), invalid: true},
		{name: "long numeric like", id: "like-state:100000000:100000000:100000000", want: "like-state:100000000:100000000:100000000"},
		{name: "surrounding whitespace", id: " \tview-audit\r\n", want: "view-audit"},
		{name: "upper bound", id: strings.Repeat("a", 128), want: strings.Repeat("a", 128)},
		{name: "overlong", id: strings.Repeat("a", 129), invalid: true},
		{name: "empty", id: " \t\n", invalid: true},
		{name: "PostgreSQL NUL", id: "event\x00id", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(Envelope{ID: tc.id, Type: EventTypePostViewed})
			if err != nil {
				t.Fatal(err)
			}
			event, err := DecodeEnvelope(raw)
			if tc.invalid {
				if err == nil {
					t.Fatalf("invalid ID %q accepted", tc.id)
				}
				return
			}
			if err != nil || event.ID != tc.want {
				t.Fatalf("decoded ID=%q err=%v want=%q", event.ID, err, tc.want)
			}
		})
	}
}

func TestLikeBehaviorBuilderEventIDContract(t *testing.T) {
	now := time.Now().UTC()
	event, err := NewLikeBehaviorEnvelope(" \tlike-state:7:42:5\n", 7, 42, "like", 5, now)
	if err != nil || event.ID != "like-state:7:42:5" {
		t.Fatalf("builder ID=%q err=%v", event.ID, err)
	}
	for _, id := range []string{strings.Repeat("a", 129), "event\x00id"} {
		if _, err := NewLikeBehaviorEnvelope(id, 7, 42, "like", 5, now); err == nil {
			t.Fatal("builder accepted an ID outside the storage contract")
		}
	}
}

func TestLikeSnapshotMaximumNumericEventIDFitsContract(t *testing.T) {
	event, err := NewLikeSnapshotEnvelope(^uint(0), math.MaxInt64, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if len(event.ID) > 128 {
		t.Fatalf("maximum numeric snapshot ID is %d bytes", len(event.ID))
	}
	if _, err := DecodeEnvelope(mustEventIDJSON(t, event)); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("like-state:%d:%d:%d", ^uint(0), ^uint(0), int64(math.MaxInt64))
	behavior, err := NewLikeBehaviorEnvelope(id, ^uint(0), ^uint(0), "like", math.MaxInt64, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEnvelope(mustEventIDJSON(t, behavior))
	if err != nil || decoded.ID != id {
		t.Fatalf("maximum numeric Like ID=%q err=%v", decoded.ID, err)
	}
}

func TestOutboxMaterializesCanonicalEventID(t *testing.T) {
	id := uuid.NewString()
	event, err := NewPostViewedEnvelope(id, 7, 42, time.Now().UTC(), "feed")
	if err != nil {
		t.Fatal(err)
	}
	event.ID = " " + id + "\n"
	row, err := NewOutboxEvent(config.KafkaConfig{UserBehaviorTopic: "user-behavior"}, event)
	if err != nil {
		t.Fatal(err)
	}
	var stored Envelope
	if err := json.Unmarshal([]byte(row.Message), &stored); err != nil {
		t.Fatal(err)
	}
	if row.ID != id || stored.ID != id {
		t.Fatalf("row ID=%q message ID=%q want=%q", row.ID, stored.ID, id)
	}
}

func mustEventIDJSON(t *testing.T, event Envelope) []byte {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
