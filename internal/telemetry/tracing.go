package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/BonzTM/bloom/internal/config"
)

// TracerProvider owns the OpenTelemetry tracing pipeline for the process and a
// single Shutdown that flushes buffered spans. It wraps the SDK provider so the
// rest of the service depends on this package, not on the OTel SDK directly.
//
// Construction is config-gated, per the handbook's operations/observability.md:
// when no OTLP endpoint is configured, NewTracerProvider installs a
// never-sampling provider with no exporter so the service runs and tests pass
// offline while the instrumentation call sites (otelhttp) stay unconditional.
type TracerProvider struct {
	provider *sdktrace.TracerProvider
}

// NewTracerProvider builds the tracing pipeline and installs it as the OTel
// global, along with the W3C TraceContext + Baggage propagator so trace headers
// flow across service boundaries.
//
// When cfg.OTLPEndpoint is empty, the provider never samples and exports
// nowhere; the returned Shutdown is still safe to call. When set, spans are
// batched (a BatchSpanProcessor) and shipped to the OTLP/HTTP collector under a
// parent-based head sampler at cfg.TraceSampleRatio.
func NewTracerProvider(ctx context.Context, cfg config.TelemetryConfig, serviceName, serviceVersion string) (*TracerProvider, error) {
	// The propagator is installed regardless of sampling so inbound trace context
	// is honored and outbound calls carry it even in a never-export build.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build trace resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}

	if cfg.OTLPEndpoint == "" {
		// Offline / unconfigured: never sample, never export. The pipeline is
		// otherwise real so call sites do not branch on whether tracing is on.
		opts = append(opts, sdktrace.WithSampler(sdktrace.NeverSample()))
	} else {
		exp, expErr := newOTLPExporter(ctx, cfg)
		if expErr != nil {
			return nil, expErr
		}
		opts = append(opts,
			sdktrace.WithBatcher(exp),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.TraceSampleRatio))),
		)
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return &TracerProvider{provider: tp}, nil
}

func newOTLPExporter(ctx context.Context, cfg config.TelemetryConfig) (sdktrace.SpanExporter, error) {
	exporterOpts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		exporterOpts = append(exporterOpts, otlptracehttp.WithInsecure())
	}
	exp, err := otlptracehttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("build OTLP trace exporter: %w", err)
	}
	return exp, nil
}

// Shutdown flushes buffered spans and releases the exporter. It is wired into
// the ordered shutdown sequence as the final telemetry-flush step; pass a fresh
// (bounded) context, never the cancelled root context.
func (t *TracerProvider) Shutdown(ctx context.Context) error {
	if err := t.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown tracer provider: %w", err)
	}
	return nil
}
