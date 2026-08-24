package runtimehealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAPIReadinessKafkaDegradedDoesNotBlockReady(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	readiness := NewAPIReadiness(APIOptions{
		Role: "api", Now: func() time.Time { return now },
		DatabasePing: func(context.Context) error { return nil },
		SchemaCheck:  func(context.Context) error { return nil },
		RedisPing:    func(context.Context) error { return nil },
		KafkaPing:    func(context.Context) error { return errors.New("broker=secret.internal:9092") },
	})

	snapshot := readiness.EvaluateNow(context.Background())
	if snapshot.Status != "ready" {
		t.Fatalf("expected ready, got %#v", snapshot)
	}
	if snapshot.Checks["kafka"] != "degraded" {
		t.Fatalf("expected degraded Kafka check, got %#v", snapshot.Checks)
	}
	if len(snapshot.ReasonCodes) != 0 {
		t.Fatalf("Kafka degradation leaked into hard-gate reasons: %#v", snapshot.ReasonCodes)
	}

	snapshot.Checks["database"] = "tampered"
	if readiness.Snapshot().Checks["database"] != "ok" {
		t.Fatal("readiness snapshot was not immutable")
	}
}

func TestAPIReadinessRedactsCheckErrorsAndTimesOut(t *testing.T) {
	readiness := NewAPIReadiness(APIOptions{
		Role:         "api",
		DatabasePing: func(context.Context) error { return errors.New("password=super-secret host=db.internal") },
		SchemaCheck:  func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		RedisPing:    func(context.Context) error { return nil },
		KafkaPing:    func(context.Context) error { return nil },
	})

	snapshot := readiness.EvaluateNow(context.Background())
	if snapshot.Status != "not_ready" {
		t.Fatalf("expected not_ready, got %#v", snapshot)
	}
	joined := strings.Join(snapshot.ReasonCodes, " ")
	if strings.Contains(joined, "super-secret") || strings.Contains(joined, "db.internal") {
		t.Fatalf("raw database error leaked into reason codes: %q", joined)
	}
	if !strings.Contains(joined, "database_unavailable") && !strings.Contains(joined, "database_check_timeout") {
		t.Fatalf("missing stable database reason: %#v", snapshot.ReasonCodes)
	}
	if !strings.Contains(joined, "schema_check_timeout") {
		t.Fatalf("missing stable schema timeout reason: %#v", snapshot.ReasonCodes)
	}
}

func TestAPIReadinessSnapshotBecomesStale(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	readiness := NewAPIReadiness(APIOptions{
		Role: "api", Now: func() time.Time { return now },
		DatabasePing: func(context.Context) error { return nil },
		SchemaCheck:  func(context.Context) error { return nil },
		RedisPing:    func(context.Context) error { return nil },
		KafkaPing:    func(context.Context) error { return nil },
	})
	if readiness.EvaluateNow(context.Background()).Status != "ready" {
		t.Fatal("expected initial evaluation to be ready")
	}
	now = now.Add(6 * time.Second)
	snapshot := readiness.Snapshot()
	if snapshot.Status != "not_ready" {
		t.Fatalf("expected stale snapshot to be not_ready, got %#v", snapshot)
	}
	if !containsReason(snapshot.ReasonCodes, "readiness_snapshot_stale") {
		t.Fatalf("missing stale reason: %#v", snapshot.ReasonCodes)
	}
}

func containsReason(reasons []string, wanted string) bool {
	for _, reason := range reasons {
		if reason == wanted {
			return true
		}
	}
	return false
}
