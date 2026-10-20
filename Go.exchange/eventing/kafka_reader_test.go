package eventing

import (
	"testing"

	"Go.exchange/config"
)

func TestNewKafkaReaderUsesUniqueDescribableClientIDs(t *testing.T) {
	cfg := config.KafkaConfig{Brokers: []string{"localhost:9092"}}
	first, firstID, err := NewKafkaReader(cfg, "events", "projection")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, secondID, err := NewKafkaReader(cfg, "events", "projection")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("reader ClientIDs must be nonempty and unique: %q %q", firstID, secondID)
	}
	if first.Config().Dialer == nil || first.Config().Dialer.ClientID != firstID || second.Config().Dialer == nil || second.Config().Dialer.ClientID != secondID {
		t.Fatalf("reader config does not expose the returned ClientIDs: %q %q", firstID, secondID)
	}
}
