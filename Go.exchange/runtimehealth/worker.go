package runtimehealth

import "time"

type WorkerPipelineSnapshot struct {
	ExpectedWorkers       int       `json:"expected_workers"`
	ActiveWorkers         int       `json:"active_workers"`
	StartedAt             time.Time `json:"started_at,omitempty"`
	LastSuccessAt         time.Time `json:"last_success_at,omitempty"`
	LastFailureAt         time.Time `json:"last_failure_at,omitempty"`
	ConsecutiveFailures   int       `json:"consecutive_failures"`
	LastCommittedOffsetAt time.Time `json:"last_committed_offset_at,omitempty"`
	BacklogSince          time.Time `json:"backlog_since,omitempty"`
	LastProgressAt        time.Time `json:"last_progress_at,omitempty"`
	Backlog               int64     `json:"backlog"`
	State                 string    `json:"state"`
	ReasonCode            string    `json:"reason_code,omitempty"`
}

type WorkerReadinessSnapshot struct {
	Status      string                            `json:"status"`
	Role        string                            `json:"role"`
	Checks      map[string]string                 `json:"checks"`
	Pipelines   map[string]WorkerPipelineSnapshot `json:"pipelines"`
	ReasonCodes []string                          `json:"reason_codes,omitempty"`
	EvaluatedAt time.Time                         `json:"-"`
}
