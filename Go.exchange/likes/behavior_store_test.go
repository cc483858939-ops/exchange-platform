package likes

import (
	"fmt"
	"testing"
	"time"
)

func TestParseBehaviorStateUsesUnixMicrosecondTimestamp(t *testing.T) {
	want := time.Date(2026, 10, 10, 12, 34, 56, 789123000, time.UTC)
	encoded := fmt.Sprintf("1|42|%d", want.UnixMicro())

	liked, version, got, err := parseBehaviorState(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !liked || version != 42 || !got.Equal(want) || got.UnixMicro() != want.UnixMicro() {
		t.Fatalf("parsed state=(%t,%d,%s) want=(true,42,%s)", liked, version, got, want)
	}
}

func TestParseBehaviorStateRejectsNonMicrosecondTimestamp(t *testing.T) {
	for _, encoded := range []string{
		"1|1|2026-10-10T12:34:56Z",
		"1|1|0",
		"1|1|9223372036854775808",
	} {
		if _, _, _, err := parseBehaviorState(encoded); err == nil {
			t.Errorf("parseBehaviorState(%q) succeeded", encoded)
		}
	}
}
