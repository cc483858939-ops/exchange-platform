package main

import "testing"

func TestParseDateRange(t *testing.T) {
	from, to, err := parseDateRange("2026-07-01", "2026-07-30")
	if err != nil {
		t.Fatal(err)
	}
	if from.Format(dateLayout) != "2026-07-01" || to.Format(dateLayout) != "2026-07-30" {
		t.Fatalf("unexpected range: %s %s", from, to)
	}
	if _, _, err := parseDateRange("2026-07-31", "2026-07-30"); err == nil {
		t.Fatal("expected reversed date range to fail")
	}
}
