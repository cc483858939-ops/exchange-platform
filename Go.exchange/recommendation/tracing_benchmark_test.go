package recommendation

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type recommendationTracingBenchmarkExporter struct{}

func (recommendationTracingBenchmarkExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}
func (recommendationTracingBenchmarkExporter) Shutdown(context.Context) error { return nil }

// BenchmarkRecommendationSpanOverhead measures the no-op helper path and the
// sampled child-span path separately from HTTP middleware and dependencies.
func BenchmarkRecommendationSpanOverhead(b *testing.B) {
	benchmarks := []struct {
		name  string
		ratio *float64
	}{
		{name: "A_NoTracing"},
		{name: "B_Disabled"},
	}
	for _, ratio := range []float64{.05, 1} {
		name := "C_Enabled5Percent"
		if ratio == 1 {
			name = "D_Enabled100Percent"
		}
		benchmarks = append(benchmarks, struct {
			name  string
			ratio *float64
		}{name: name, ratio: &ratio})
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			previous := TracingEnabled()
			SetTracingEnabled(benchmark.ratio != nil)
			b.Cleanup(func() { SetTracingEnabled(previous) })

			var provider *sdktrace.TracerProvider
			if benchmark.ratio != nil {
				provider = sdktrace.NewTracerProvider(
					sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(*benchmark.ratio))),
					sdktrace.WithBatcher(recommendationTracingBenchmarkExporter{}),
				)
				b.Cleanup(func() {
					if err := provider.Shutdown(context.Background()); err != nil {
						b.Errorf("shutdown benchmark provider: %v", err)
					}
				})
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if benchmark.name == "A_NoTracing" {
					continue
				}
				ctx := context.Background()
				var rootSpan trace.Span
				if provider != nil {
					ctx, rootSpan = provider.Tracer("benchmark/http").Start(ctx, "GET /api/public/recommendations/posts")
				}
				spanCtx, span := startRecommendationSpan(ctx, "recommendation.rank",
					attribute.String("recommendation.phase", "fresh"),
					attribute.Int("recommendation.candidate_count", 42),
				)
				_ = spanCtx
				span.End()
				if rootSpan != nil {
					rootSpan.End()
				}
			}
		})
	}
}
