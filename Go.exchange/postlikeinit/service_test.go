package postlikeinit

import (
	"testing"
	"time"
)

func TestRetryDelayIsExponentialAndBounded(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: time.Second},
		{attempts: 1, want: time.Second},
		{attempts: 2, want: 2 * time.Second},
		{attempts: 3, want: 4 * time.Second},
		{attempts: 10, want: maxRetryDelay},
		{attempts: 1000, want: maxRetryDelay},
	}
	for _, test := range tests {
		if got := RetryDelay(test.attempts); got != test.want {
			t.Errorf("RetryDelay(%d)=%s want=%s", test.attempts, got, test.want)
		}
	}
}
