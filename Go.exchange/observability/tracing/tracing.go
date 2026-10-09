package tracing

import (
	"context"
	"fmt"
	"math/rand"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxSpanQueueSize = 2048
	maxSpanBatchSize = 512
)

// Initialize installs a process-wide tracer provider. A disabled configuration
// intentionally returns without constructing an exporter or changing globals.
func Initialize(ctx context.Context, cfg Config) (*sdktrace.TracerProvider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}
	if ctx == nil {
		return nil, fmt.Errorf("initialize tracing: context is nil")
	}

	target, secure, err := parseEndpoint(cfg.OTLPEndpoint)
	if err != nil {
		return nil, fmt.Errorf("initialize tracing: %w", err)
	}
	exporterOptions := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(target),
		otlptracegrpc.WithTimeout(cfg.ExportTimeout),
	}
	if !secure {
		exporterOptions = append(exporterOptions, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, exporterOptions...)
	if err != nil {
		return nil, fmt.Errorf("initialize OTLP gRPC trace exporter: %w", err)
	}

	provider, err := newTracerProvider(ctx, cfg, exporter)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ExportTimeout)
		defer cancel()
		_ = exporter.Shutdown(shutdownCtx)
		return nil, err
	}
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))
	return provider, nil
}

func newTracerProvider(ctx context.Context, cfg Config, exporter sdktrace.SpanExporter) (*sdktrace.TracerProvider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if exporter == nil {
		return nil, fmt.Errorf("initialize tracing: span exporter is nil")
	}
	resource, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(attribute.String("service.name", cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize tracing resource: %w", err)
	}
	// Public HTTP traceparent headers are untrusted. Use a local random decision
	// for either remote sampled flag so a caller cannot choose trace IDs that
	// force TraceIDRatioBased to record every request.
	rootSampler := sdktrace.TraceIDRatioBased(cfg.SampleRatio)
	remoteSampler := untrustedRemoteSampler{ratio: cfg.SampleRatio}
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource),
		sdktrace.WithSampler(sdktrace.ParentBased(
			rootSampler,
			sdktrace.WithRemoteParentSampled(remoteSampler),
			sdktrace.WithRemoteParentNotSampled(remoteSampler),
		)),
		sdktrace.WithBatcher(
			exporter,
			sdktrace.WithExportTimeout(cfg.ExportTimeout),
			sdktrace.WithMaxQueueSize(maxSpanQueueSize),
			sdktrace.WithMaxExportBatchSize(maxSpanBatchSize),
		),
	), nil
}

type untrustedRemoteSampler struct {
	ratio float64
}

func (sampler untrustedRemoteSampler) ShouldSample(parameters sdktrace.SamplingParameters) sdktrace.SamplingResult {
	decision := sdktrace.Drop
	if rand.Float64() < sampler.ratio {
		decision = sdktrace.RecordAndSample
	}
	return sdktrace.SamplingResult{
		Decision:   decision,
		Tracestate: trace.SpanContextFromContext(parameters.ParentContext).TraceState(),
	}
}

func (sampler untrustedRemoteSampler) Description() string {
	return fmt.Sprintf("UntrustedRemoteRatioBased{%g}", sampler.ratio)
}
