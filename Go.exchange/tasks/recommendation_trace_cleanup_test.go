package tasks

import (
	"testing"

	"Go.exchange/config"
)

func TestRecommendationTraceCleanupConfig(t *testing.T) {
	tests := []struct {
		name          string
		appConfig     *config.Config
		resultDays    int
		requestDays   int
		intervalHours int
		batchSize     int
	}{
		{
			name:          "nil app config uses defaults",
			resultDays:    30,
			requestDays:   90,
			intervalHours: 6,
			batchSize:     5000,
		},
		{
			name: "request override is preserved",
			appConfig: &config.Config{
				Recommendation: config.RecommendationConfig{
					Trace: config.RecommendationTraceConfig{
						ResultRetentionDays:  30,
						RequestRetentionDays: 120,
					},
				},
			},
			resultDays:    30,
			requestDays:   120,
			intervalHours: 6,
			batchSize:     5000,
		},
		{
			name: "equal result and request retention",
			appConfig: &config.Config{
				Recommendation: config.RecommendationConfig{
					Trace: config.RecommendationTraceConfig{
						ResultRetentionDays:  120,
						RequestRetentionDays: 120,
					},
				},
			},
			resultDays:    120,
			requestDays:   120,
			intervalHours: 6,
			batchSize:     5000,
		},
		{
			name: "request retention is raised to result retention",
			appConfig: &config.Config{
				Recommendation: config.RecommendationConfig{
					Trace: config.RecommendationTraceConfig{
						ResultRetentionDays:  120,
						RequestRetentionDays: 90,
					},
				},
			},
			resultDays:    120,
			requestDays:   120,
			intervalHours: 6,
			batchSize:     5000,
		},
		{
			name: "zero request retention is replaced by effective result retention",
			appConfig: &config.Config{
				Recommendation: config.RecommendationConfig{
					Trace: config.RecommendationTraceConfig{
						ResultRetentionDays: 120,
					},
				},
			},
			resultDays:    120,
			requestDays:   120,
			intervalHours: 6,
			batchSize:     5000,
		},
		{
			name: "request floor is preserved",
			appConfig: &config.Config{
				Recommendation: config.RecommendationConfig{
					Trace: config.RecommendationTraceConfig{
						ResultRetentionDays:  30,
						RequestRetentionDays: 20,
					},
				},
			},
			resultDays:    30,
			requestDays:   90,
			intervalHours: 6,
			batchSize:     5000,
		},
		{
			name: "cleanup overrides are preserved",
			appConfig: &config.Config{
				Recommendation: config.RecommendationConfig{
					Trace: config.RecommendationTraceConfig{
						ResultRetentionDays:  30,
						RequestRetentionDays: 120,
						CleanupIntervalHours: 12,
						CleanupBatchSize:     250,
					},
				},
			},
			resultDays:    30,
			requestDays:   120,
			intervalHours: 12,
			batchSize:     250,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalConfig := config.AppConfig
			t.Cleanup(func() {
				config.AppConfig = originalConfig
			})
			config.AppConfig = test.appConfig

			got := recommendationTraceCleanupConfig()
			if got.ResultRetentionDays != test.resultDays {
				t.Fatalf("result retention days=%d, want %d", got.ResultRetentionDays, test.resultDays)
			}
			if got.RequestRetentionDays != test.requestDays {
				t.Fatalf("request retention days=%d, want %d", got.RequestRetentionDays, test.requestDays)
			}
			if got.CleanupIntervalHours != test.intervalHours {
				t.Fatalf("cleanup interval hours=%d, want %d", got.CleanupIntervalHours, test.intervalHours)
			}
			if got.CleanupBatchSize != test.batchSize {
				t.Fatalf("cleanup batch size=%d, want %d", got.CleanupBatchSize, test.batchSize)
			}
		})
	}
}
