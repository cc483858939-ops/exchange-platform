package recommendation

import (
	"context"
	"errors"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func TestRecommendationRepositoryConstructorsRejectNilDependencies(t *testing.T) {
	constructors := []struct {
		name string
		call func() error
	}{
		{"candidate", func() error { _, err := NewGormCandidateRepository(nil); return err }},
		{"profile", func() error { _, err := NewGormProfileRepository(nil); return err }},
		{"dirty profile", func() error { _, err := NewGormDirtyProfileRepository(nil); return err }},
		{"source", func() error { _, err := NewGormSourceRepository(nil); return err }},
		{"trace", func() error { _, err := NewGormTraceRepository(nil); return err }},
		{"history", func() error { _, err := NewRedisHistoryStore(nil); return err }},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			if err := constructor.call(); err == nil {
				t.Fatal("constructor accepted a nil dependency")
			}
		})
	}
}

func TestRecommendationRepositoryIOHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	db := &gorm.DB{}
	candidate, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := NewGormProfileRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := NewGormDirtyProfileRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewGormSourceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := NewGormTraceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = redisClient.Close() })
	history, err := NewRedisHistoryStore(redisClient)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{"candidate query", func() error {
			_, err := candidate.LoadRecentCandidates(ctx, CandidateQuery{UserID: 1, Limit: 1})
			return err
		}},
		{"candidate hydration", func() error {
			_, err := candidate.HydrateCandidates(ctx, "v1", []Candidate{{PostID: 1}}, time.Now())
			return err
		}},
		{"post embeddings", func() error {
			_, err := candidate.LoadPostEmbeddings(ctx, []uint{1}, "v1")
			return err
		}},
		{"profile", func() error {
			_, err := profile.Load(ctx, ProfileLoadQuery{UserID: 1})
			return err
		}},
		{"author context", func() error {
			_, err := profile.LoadAuthorContext(ctx, AuthorContextQuery{UserID: 1, AuthorIDs: []uint{2}, LoadAffinity: true})
			return err
		}},
		{"post affinity inputs", func() error {
			_, err := profile.LoadPostAffinityInputs(ctx, []uint{1})
			return err
		}},
		{"dirty invalidation", func() error { return dirty.InvalidateProfiles(ctx, []uint{1}, "test", time.Now()) }},
		{"dirty queue", func() error { return dirty.EnsureProfilesQueued(ctx, []uint{1}, "test", time.Now()) }},
		{"dirty listing", func() error {
			_, err := dirty.ListDue(ctx, time.Time{}, time.Time{}, 1)
			return err
		}},
		{"dirty load", func() error { _, err := dirty.Load(ctx, 1); return err }},
		{"dirty delete", func() error { _, err := dirty.DeleteClaim(ctx, 1, 1); return err }},
		{"dirty retry", func() error {
			return dirty.RetryClaim(ctx, DirtyProfile{UserID: 1, DirtyVersion: 1}, errors.New("retry"), time.Now())
		}},
		{"source signals", func() error { _, err := source.LoadSourceSignals(ctx, 1, time.Time{}); return err }},
		{"trace persistence", func() error {
			return trace.PersistServing(ctx, models.RecommendationRequest{}, nil)
		}},
		{"user history", func() error {
			_, err := history.LoadUserHistory(ctx, 1, HistoryWindow{Limit: 1})
			return err
		}},
		{"guest history", func() error {
			_, err := history.LoadGuestHistory(ctx, "session", HistoryWindow{Limit: 1})
			return err
		}},
		{"record user history", func() error {
			return history.RecordUserServed(ctx, 1, []uint{2}, HistoryWindow{Limit: 1, TTL: time.Hour, Now: time.Now()})
		}},
		{"record guest history", func() error {
			return history.RecordGuestServed(ctx, "session", []uint{2}, HistoryWindow{Limit: 1, TTL: time.Hour, Now: time.Now()})
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v want context.Canceled", err)
			}
		})
	}
}
