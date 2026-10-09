package tracing

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestLoadConfigDefaults(t *testing.T) {
	setTracingEnvDefaults(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("tracing is enabled by default")
	}
	if cfg.ServiceName != "exchange-api" || cfg.OTLPEndpoint != "tempo:4317" || cfg.SampleRatio != 0.05 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.ExportTimeout != 3*time.Second || cfg.ShutdownTimeout != 5*time.Second {
		t.Fatalf("unexpected timeout defaults: %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	setTracingEnvDefaults(t)
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "enabled", key: "TRACING_ENABLED", value: "sometimes"},
		{name: "sample ratio text", key: "TRACING_SAMPLE_RATIO", value: "lots"},
		{name: "sample ratio too low", key: "TRACING_SAMPLE_RATIO", value: "-0.01"},
		{name: "sample ratio too high", key: "TRACING_SAMPLE_RATIO", value: "1.01"},
		{name: "sample ratio nan", key: "TRACING_SAMPLE_RATIO", value: "NaN"},
		{name: "endpoint", key: "TRACING_OTLP_ENDPOINT", value: "http://tempo:4317"},
		{name: "endpoint port", key: "TRACING_OTLP_ENDPOINT", value: "tempo:70000"},
		{name: "export timeout", key: "TRACING_EXPORT_TIMEOUT", value: "soon"},
		{name: "shutdown timeout", key: "TRACING_SHUTDOWN_TIMEOUT", value: "0s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("LoadConfig accepted %s=%q", test.key, test.value)
			}
		})
	}
}

func TestLoadConfigSupportsSecureEndpointAndSamplingBounds(t *testing.T) {
	setTracingEnvDefaults(t)
	t.Setenv("TRACING_ENABLED", "true")
	t.Setenv("TRACING_OTLP_ENDPOINT", "https://tempo.example.test:4317")
	t.Setenv("TRACING_SAMPLE_RATIO", "1")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.SampleRatio != 1 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if target, secure, err := parseEndpoint(cfg.OTLPEndpoint); err != nil || !secure || target != "tempo.example.test:4317" {
		t.Fatalf("parseEndpoint=(%q, %v, %v)", target, secure, err)
	}
	if target, secure, err := parseEndpoint("tempo:4317"); err != nil || secure || target != "tempo:4317" {
		t.Fatalf("parseEndpoint=(%q, %v, %v)", target, secure, err)
	}
}

func TestInitializeDisabledDoesNotConstructProvider(t *testing.T) {
	cfg := Config{
		ServiceName: "exchange-api", OTLPEndpoint: "tempo:4317", SampleRatio: 1,
		ExportTimeout: time.Second, ShutdownTimeout: time.Second,
	}
	provider, err := Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if provider != nil {
		t.Fatal("disabled tracing returned a provider")
	}
}

func TestTracerProviderSamplesAndExportsSpans(t *testing.T) {
	tests := []struct {
		name      string
		ratio     float64
		wantSpans int
	}{
		{name: "none", ratio: 0, wantSpans: 0},
		{name: "all", ratio: 1, wantSpans: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider, err := newTracerProvider(context.Background(), testConfig(test.ratio), exporter)
			if err != nil {
				t.Fatal(err)
			}
			_, span := provider.Tracer("test").Start(context.Background(), "recommendation.test")
			span.End()
			if err := provider.ForceFlush(context.Background()); err != nil {
				t.Fatal(err)
			}
			spans := exporter.GetSpans()
			if len(spans) != test.wantSpans {
				t.Fatalf("exported spans=%d want=%d", len(spans), test.wantSpans)
			}
			if test.wantSpans == 1 {
				if spans[0].Name != "recommendation.test" {
					t.Fatalf("span name=%q", spans[0].Name)
				}
				if got := spans[0].Resource.Attributes(); !attributeValue(got, "service.name", "exchange-api") {
					t.Fatalf("service.name missing from resource: %#v", got)
				}
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := provider.Shutdown(shutdownCtx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTracerProviderShutsDownAfterRequestCancellation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider, err := newTracerProvider(context.Background(), testConfig(1), exporter)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, span := provider.Tracer("test").Start(ctx, "recommendation.cancelled")
	span.End()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := provider.ForceFlush(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if got := exporter.GetSpans(); len(got) != 1 {
		t.Fatalf("request cancellation dropped an ended span: exported spans=%d", len(got))
	}
	if err := provider.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestBatchSpanProcessorDoesNotBlockRequestsWhenExporterIsUnavailable(t *testing.T) {
	exporter := &blockingSpanExporter{started: make(chan struct{}), release: make(chan struct{})}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(
			exporter,
			sdktrace.WithMaxExportBatchSize(1),
			sdktrace.WithBatchTimeout(time.Millisecond),
			sdktrace.WithExportTimeout(time.Second),
		),
	)
	t.Cleanup(func() {
		exporter.unblock()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := provider.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	_, first := provider.Tracer("test").Start(context.Background(), "recommendation.first")
	first.End()
	select {
	case <-exporter.started:
	case <-time.After(time.Second):
		t.Fatal("batch exporter did not start")
	}

	_, second := provider.Tracer("test").Start(context.Background(), "recommendation.second")
	ended := make(chan struct{})
	go func() {
		second.End()
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("ending a request span blocked behind the exporter")
	}

	exporter.unblock()
	flushCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.ForceFlush(flushCtx); err != nil {
		t.Fatal(err)
	}
}

type blockingSpanExporter struct {
	started   chan struct{}
	release   chan struct{}
	start     sync.Once
	unblocker sync.Once
}

func (exporter *blockingSpanExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	exporter.start.Do(func() { close(exporter.started) })
	select {
	case <-exporter.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (exporter *blockingSpanExporter) Shutdown(context.Context) error {
	exporter.unblock()
	return nil
}

func (exporter *blockingSpanExporter) unblock() {
	exporter.unblocker.Do(func() { close(exporter.release) })
}

func setTracingEnvDefaults(t *testing.T) {
	t.Helper()
	t.Setenv("TRACING_ENABLED", "false")
	t.Setenv("TRACING_SERVICE_NAME", "exchange-api")
	t.Setenv("TRACING_OTLP_ENDPOINT", "tempo:4317")
	t.Setenv("TRACING_SAMPLE_RATIO", "0.05")
	t.Setenv("TRACING_EXPORT_TIMEOUT", "3s")
	t.Setenv("TRACING_SHUTDOWN_TIMEOUT", "5s")
}

func testConfig(sampleRatio float64) Config {
	return Config{
		Enabled: true, ServiceName: "exchange-api", OTLPEndpoint: "tempo:4317", SampleRatio: sampleRatio,
		ExportTimeout: time.Second, ShutdownTimeout: time.Second,
	}
}

func attributeValue(attributes []attribute.KeyValue, key, want string) bool {
	for _, item := range attributes {
		if string(item.Key) == key && item.Value.AsString() == want {
			return true
		}
	}
	return false
}

var _ sdktrace.SpanExporter = (*tracetest.InMemoryExporter)(nil)

func TestValidateRejectsNonFiniteSampleRatios(t *testing.T) {
	cfg := testConfig(math.NaN())
	if err := cfg.Validate(); err == nil {
		t.Fatal("NaN sample ratio was accepted")
	}
}
