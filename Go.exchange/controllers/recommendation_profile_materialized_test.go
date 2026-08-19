package controllers

import (
	"math"
	"testing"
	"time"
)

func TestMaterializedNegativeConfidence(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	const evidence = 6.0
	const halfLifeDays = 14.0
	const saturationScale = 12.0

	tests := []struct {
		name              string
		computedAt        time.Time
		evidence          float64
		halfLifeDays      float64
		saturationScale   float64
		hasNegativeVector bool
		want              float64
	}{
		{
			name:              "zero elapsed time",
			computedAt:        now,
			evidence:          evidence,
			halfLifeDays:      halfLifeDays,
			saturationScale:   saturationScale,
			hasNegativeVector: true,
			want:              math.Tanh(evidence / saturationScale),
		},
		{
			name:              "one half-life",
			computedAt:        now.Add(-14 * 24 * time.Hour),
			evidence:          evidence,
			halfLifeDays:      halfLifeDays,
			saturationScale:   saturationScale,
			hasNegativeVector: true,
			want:              math.Tanh(3 / saturationScale),
		},
		{
			name:              "two half-lives",
			computedAt:        now.Add(-28 * 24 * time.Hour),
			evidence:          evidence,
			halfLifeDays:      halfLifeDays,
			saturationScale:   saturationScale,
			hasNegativeVector: true,
			want:              math.Tanh(1.5 / saturationScale),
		},
		{
			name:              "future computed at is clamped",
			computedAt:        now.Add(time.Hour),
			evidence:          evidence,
			halfLifeDays:      halfLifeDays,
			saturationScale:   saturationScale,
			hasNegativeVector: true,
			want:              math.Tanh(evidence / saturationScale),
		},
		{
			name:            "no negative vector",
			computedAt:      now,
			evidence:        evidence,
			halfLifeDays:    halfLifeDays,
			saturationScale: saturationScale,
			want:            0,
		},
		{
			name:              "zero evidence",
			computedAt:        now,
			halfLifeDays:      halfLifeDays,
			saturationScale:   saturationScale,
			hasNegativeVector: true,
			want:              0,
		},
		{
			name:              "negative evidence",
			computedAt:        now,
			evidence:          -1,
			halfLifeDays:      halfLifeDays,
			saturationScale:   saturationScale,
			hasNegativeVector: true,
			want:              0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := materializedNegativeConfidence(
				test.evidence,
				test.computedAt,
				now,
				test.halfLifeDays,
				test.saturationScale,
				test.hasNegativeVector,
			)
			if math.Abs(got-test.want) > 1e-12 {
				t.Fatalf("confidence=%g want=%g", got, test.want)
			}
			if got < 0 || got >= 1 || math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("confidence=%g is outside finite [0,1)", got)
			}
		})
	}
}

func TestMaterializedNegativeConfidenceRejectsInvalidNumbers(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name            string
		evidence        float64
		halfLifeDays    float64
		saturationScale float64
	}{
		{name: "nan evidence", evidence: math.NaN(), halfLifeDays: 14, saturationScale: 12},
		{name: "infinite evidence", evidence: math.Inf(1), halfLifeDays: 14, saturationScale: 12},
		{name: "nan half life", evidence: 6, halfLifeDays: math.NaN(), saturationScale: 12},
		{name: "infinite saturation", evidence: 6, halfLifeDays: 14, saturationScale: math.Inf(1)},
		{name: "zero half life", evidence: 6, halfLifeDays: 0, saturationScale: 12},
		{name: "zero saturation", evidence: 6, halfLifeDays: 14, saturationScale: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := materializedNegativeConfidence(test.evidence, now, now, test.halfLifeDays, test.saturationScale, true)
			if got != 0 || math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("confidence=%g want finite zero", got)
			}
		})
	}
}
