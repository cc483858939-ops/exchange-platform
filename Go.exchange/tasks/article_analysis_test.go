package tasks

import (
	"testing"
	"time"

	"Go.exchange/config"
)

func TestArticleAnalysisRetryDelayUsesExponentialBackoffAndCaps(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{7, time.Hour},
		{99, time.Hour},
	}
	for _, testCase := range cases {
		if got := articleAnalysisRetryDelay(testCase.attempt); got != testCase.want {
			t.Fatalf("attempt %d: got %s want %s", testCase.attempt, got, testCase.want)
		}
	}
}

func TestJobLeaseDurationUsesConfiguredValueAndDefault(t *testing.T) {
	original := config.AppConfig
	defer func() { config.AppConfig = original }()

	config.AppConfig = &config.Config{}
	if got := jobLeaseDuration(); got != 120*time.Second {
		t.Fatalf("default lease=%s", got)
	}
	config.AppConfig.Kafka.JobLeaseSeconds = 15
	if got := jobLeaseDuration(); got != 15*time.Second {
		t.Fatalf("configured lease=%s", got)
	}
}
