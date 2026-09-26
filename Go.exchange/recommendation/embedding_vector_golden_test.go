package recommendation

import (
	"math"
	"testing"
)

func TestValidEmbeddingVectorRejectsZeroAndNonFiniteVectors(t *testing.T) {
	tests := []struct {
		name   string
		vector []float32
		valid  bool
	}{
		{name: "nil", vector: nil},
		{name: "empty", vector: []float32{}},
		{name: "single zero", vector: []float32{0}},
		{name: "zero vector", vector: []float32{0, 0}},
		{name: "positive nonzero", vector: []float32{1, 0}, valid: true},
		{name: "negative nonzero", vector: []float32{-1, 0}, valid: true},
		{name: "tiny nonzero", vector: []float32{1e-20, 0}, valid: true},
		{name: "nan", vector: []float32{float32(math.NaN()), 0}},
		{name: "positive infinity", vector: []float32{float32(math.Inf(1)), 0}},
		{name: "negative infinity", vector: []float32{float32(math.Inf(-1)), 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidEmbeddingVector(tc.vector); got != tc.valid {
				t.Fatalf("ValidEmbeddingVector(%v)=%v, want %v", tc.vector, got, tc.valid)
			}
		})
	}
}
