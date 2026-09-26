package recommendation

import (
	"fmt"
	"testing"
)

func TestSemanticRecallQuotaUsesReservedRecentAndEvergreenCapacity(t *testing.T) {
	tests := []struct {
		cap           int
		ratio         float64
		wantRecent    int
		wantEvergreen int
	}{
		{cap: 0, ratio: 0.8, wantRecent: 0, wantEvergreen: 0},
		{cap: 1, ratio: 0.8, wantRecent: 1, wantEvergreen: 0},
		{cap: 4, ratio: 0.75, wantRecent: 3, wantEvergreen: 1},
		{cap: 200, ratio: 0.8, wantRecent: 160, wantEvergreen: 40},
		{cap: 200, ratio: 0.85, wantRecent: 170, wantEvergreen: 30},
		{cap: 2, ratio: 0.01, wantRecent: 1, wantEvergreen: 1},
		{cap: 2, ratio: 0.99, wantRecent: 1, wantEvergreen: 1},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("cap_%d_ratio_%g", tc.cap, tc.ratio), func(t *testing.T) {
			recent, evergreen := SemanticRecallQuota(tc.cap, tc.ratio)
			if recent != tc.wantRecent || evergreen != tc.wantEvergreen || recent+evergreen != tc.cap {
				t.Fatalf("quota=(%d,%d), want (%d,%d)", recent, evergreen, tc.wantRecent, tc.wantEvergreen)
			}
		})
	}
}
