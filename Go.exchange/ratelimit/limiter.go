package ratelimit

import (
	"context"
	"time"
)

type Input struct {
	Subject string
	Action  Action
}

type Decision struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	RetryAfter time.Duration
	ResetAt    time.Time
}

type Limiter interface {
	Allow(context.Context, Input) (Decision, error)
}
