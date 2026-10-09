package recommendation

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"Go.exchange/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var recommendationTracingEnabled atomic.Bool
var disabledRecommendationSpan = trace.SpanFromContext(context.Background())

func init() {
	recommendationTracingEnabled.Store(true)
}

// SetTracingEnabled configures recommendation stage spans once during process
// startup. The default stays enabled for existing library callers.
func SetTracingEnabled(enabled bool) {
	recommendationTracingEnabled.Store(enabled)
}

func TracingEnabled() bool {
	return recommendationTracingEnabled.Load()
}

func startRecommendationSpan(ctx context.Context, name string, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	if !TracingEnabled() {
		return ctx, disabledRecommendationSpan
	}
	options := make([]trace.SpanStartOption, 0, 1)
	if len(attributes) > 0 {
		options = append(options, trace.WithAttributes(attributes...))
	}
	provider := otel.GetTracerProvider()
	parentSpan := trace.SpanFromContext(ctx)
	if parentSpan.SpanContext().IsValid() {
		provider = parentSpan.TracerProvider()
	}
	return provider.Tracer("Go.exchange/recommendation").Start(ctx, name, options...)
}

func finishRecommendationSpan(span trace.Span, err error) {
	if err == nil || span == nil {
		return
	}
	span.RecordError(errors.New("recommendation operation failed"), trace.WithAttributes(
		attribute.String("error.type", recommendationErrorType(err)),
	))
	span.SetStatus(codes.Error, "recommendation operation failed")
}

func recommendationErrorType(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	default:
		return fmt.Sprintf("%T", err)
	}
}

func traceRecommendationOperation[T any](ctx context.Context, name string, attributes []attribute.KeyValue, operation func(context.Context) (T, error)) (T, error) {
	if !TracingEnabled() {
		return operation(ctx)
	}
	spanCtx, span := startRecommendationSpan(ctx, name, attributes...)
	defer span.End()
	result, err := operation(spanCtx)
	finishRecommendationSpan(span, err)
	return result, err
}

func traceRecommendationResult[T any](ctx context.Context, name string, attributes []attribute.KeyValue, operation func(trace.Span) T) T {
	if !TracingEnabled() {
		return operation(disabledRecommendationSpan)
	}
	_, span := startRecommendationSpan(ctx, name, attributes...)
	defer span.End()
	return operation(span)
}

func traceHydrateRecommendationCandidates(ctx context.Context, repository CandidateRepository, servingVersion string, candidates []Candidate, now time.Time, phase string) ([]RankedCandidate, error) {
	if !TracingEnabled() {
		return repository.HydrateCandidates(ctx, servingVersion, candidates, now)
	}
	attributes := []attribute.KeyValue{attribute.String("recommendation.phase", phase)}
	spanCtx, span := startRecommendationSpan(ctx, "recommendation.hydrate", attributes...)
	defer span.End()
	hydrated, err := repository.HydrateCandidates(spanCtx, servingVersion, candidates, now)
	span.SetAttributes(attribute.Int("recommendation.candidate_count", len(hydrated)))
	finishRecommendationSpan(span, err)
	return hydrated, err
}

func loadRecommendationCandidates(ctx context.Context, name, source, phase string, load func(context.Context) ([]Candidate, error)) ([]Candidate, error) {
	if !TracingEnabled() {
		return load(ctx)
	}
	spanCtx, span := startRecommendationSpan(ctx, name,
		attribute.String("recommendation.source", source),
		attribute.String("recommendation.phase", phase),
	)
	defer span.End()
	candidates, err := load(spanCtx)
	span.SetAttributes(attribute.Int("recommendation.candidate_count", len(candidates)))
	finishRecommendationSpan(span, err)
	return candidates, err
}

func loadRecommendationAuthorContext(ctx context.Context, repository ProfileRepository, userID uint, profile *Profile, candidates []RankedCandidate, loadedAuthors map[uint]struct{}, cfg config.RecommendationConfig, phase string) error {
	if !TracingEnabled() {
		return loadMaterializedCandidateAuthorContext(ctx, repository, userID, profile, candidates, loadedAuthors, cfg)
	}
	spanCtx, span := startRecommendationSpan(ctx, "recommendation.author_context.load",
		attribute.String("recommendation.phase", phase),
		attribute.Int("recommendation.candidate_count", len(candidates)),
	)
	defer span.End()
	err := loadMaterializedCandidateAuthorContext(spanCtx, repository, userID, profile, candidates, loadedAuthors, cfg)
	finishRecommendationSpan(span, err)
	return err
}

func traceRankRecommendationCandidates(ctx context.Context, profile ProfileFeatures, candidates []RankedCandidate, now time.Time, cfg RankingConfig, languageContext LanguageContext, phase string) []RankedCandidate {
	if !TracingEnabled() {
		return RankCandidates(profile, candidates, now, cfg, languageContext)
	}
	return traceRecommendationResult(ctx, "recommendation.rank", []attribute.KeyValue{
		attribute.String("recommendation.phase", phase),
		attribute.Int("recommendation.candidate_count", len(candidates)),
	}, func(span trace.Span) []RankedCandidate {
		ranked := RankCandidates(profile, candidates, now, cfg, languageContext)
		span.SetAttributes(attribute.Int("recommendation.result_count", len(ranked)))
		return ranked
	})
}

func traceSelectRecommendationCandidates(ctx context.Context, input SelectionInput, phase string) []SelectedCandidate {
	if !TracingEnabled() {
		return SelectCandidates(input)
	}
	return traceRecommendationResult(ctx, "recommendation.select", []attribute.KeyValue{
		attribute.String("recommendation.phase", phase),
		attribute.String("recommendation.selection_phase", string(input.Phase)),
		attribute.Int("recommendation.candidate_count", len(input.Candidates)),
	}, func(span trace.Span) []SelectedCandidate {
		selected := SelectCandidates(input)
		span.SetAttributes(attribute.Int("recommendation.result_count", len(selected)))
		return selected
	})
}

func viewerKindName(kind ViewerKind) string {
	switch kind {
	case ViewerAuthenticated:
		return "authenticated"
	case ViewerGuest:
		return "guest"
	default:
		return "unknown"
	}
}

func phaseName(softOnly bool) string {
	if softOnly {
		return "soft"
	}
	return "fresh"
}
