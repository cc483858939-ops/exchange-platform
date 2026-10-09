package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type tracingBenchmarkExporter struct{}

func (tracingBenchmarkExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}
func (tracingBenchmarkExporter) Shutdown(context.Context) error { return nil }

// BenchmarkHTTPTracing compares the same Gin route with tracing omitted and
// with SDK sampling at 5% and 100%.
// It intentionally measures middleware and HTTP span costs only; it does not
// model recommendation dependencies or database/cache work.
func BenchmarkHTTPTracing(b *testing.B) {
	previousMode := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	b.Cleanup(func() { gin.SetMode(previousMode) })

	benchmarks := []struct {
		name     string
		provider trace.TracerProvider
		tracing  bool
	}{
		{name: "A_NoTracing"},
		{name: "B_Disabled"},
		{name: "C_Enabled5Percent", provider: newTracingBenchmarkProvider(.05), tracing: true},
		{name: "D_Enabled100Percent", provider: newTracingBenchmarkProvider(1), tracing: true},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			if provider, ok := benchmark.provider.(*sdktrace.TracerProvider); ok {
				b.Cleanup(func() {
					if err := provider.Shutdown(context.Background()); err != nil {
						b.Errorf("shutdown benchmark provider: %v", err)
					}
				})
			}

			engine := gin.New()
			if benchmark.tracing {
				engine.Use(httpTracingMiddleware(benchmark.provider))
			}
			engine.GET("/api/public/recommendations/posts", func(ctx *gin.Context) {
				ctx.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/api/public/recommendations/posts", nil)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				engine.ServeHTTP(httptest.NewRecorder(), request)
			}
		})
	}
}

func newTracingBenchmarkProvider(ratio float64) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
		sdktrace.WithBatcher(tracingBenchmarkExporter{}),
	)
}
