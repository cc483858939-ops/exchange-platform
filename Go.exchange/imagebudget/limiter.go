// Package imagebudget bounds decoded-image work across all upload endpoints.
package imagebudget

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrBusy = errors.New("image processing capacity is busy")

type Limiter struct {
	mu                                     sync.Mutex
	active, waiting, maxActive, maxWaiting int
	pixels, maxPixels                      int64
	maxWait                                time.Duration
	changed                                chan struct{}
}

func New(maxActive, maxWaiting int, maxPixels int64, maxWait time.Duration) *Limiter {
	return &Limiter{maxActive: maxActive, maxWaiting: maxWaiting, maxPixels: maxPixels, maxWait: maxWait, changed: make(chan struct{})}
}

// Acquire bounds active work by both slots and source pixels. Waiting owns no
// decoded image, has a finite queue/deadline, and never starts a goroutine.
func (l *Limiter) Acquire(ctx context.Context, pixels int64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pixels <= 0 || pixels > l.maxPixels {
		return nil, ErrBusy
	}
	waitCtx, cancel := context.WithTimeout(ctx, l.maxWait)
	defer cancel()
	l.mu.Lock()
	queued := false
	defer func() {
		if queued {
			l.waiting--
		}
		l.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := waitCtx.Err(); err != nil {
			return nil, ErrBusy
		}
		if l.active < l.maxActive && pixels <= l.maxPixels-l.pixels {
			l.active++
			l.pixels += pixels
			var once sync.Once
			return func() {
				once.Do(func() {
					l.mu.Lock()
					defer l.mu.Unlock()
					l.active--
					l.pixels -= pixels
					close(l.changed)
					l.changed = make(chan struct{})
				})
			}, nil
		}
		if !queued {
			if l.waiting >= l.maxWaiting {
				return nil, ErrBusy
			}
			l.waiting++
			queued = true
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-changed:
		case <-waitCtx.Done():
		}
		l.mu.Lock()
	}
}
