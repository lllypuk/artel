// Package telemetry provides OpenTelemetry tracing setup.
package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Init sets the global tracer provider to noop (default when OTLP is not configured).
func Init() {
	otel.SetTracerProvider(noop.NewTracerProvider())
}

// InitOTLP creates an OTLP HTTP trace exporter and sets up the global tracer provider.
// Returns a shutdown function that should be called on program exit.
func InitOTLP(endpoint, serviceName string) (func(context.Context) error, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("OTLP endpoint is empty")
	}
	if serviceName == "" {
		serviceName = "agent-shatrov"
	}

	exp, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}

// InitStdout creates a stdout trace exporter for development/debugging.
func InitStdout() (func(context.Context) error, error) {
	exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("create stdout exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.Empty()),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// InitTest creates an in-memory tracer provider for tests.
// Returns a TracerProvider (which implements the interface needed for testing).
func InitTest(exp sdktrace.SpanExporter) *sdktrace.TracerProvider {
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
		sdktrace.WithResource(resource.Empty()),
	)
	otel.SetTracerProvider(tp)
	return tp
}

// Tracer returns a named tracer from the global provider.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// ShutdownNoop returns a no-op shutdown for use when tracing is disabled.
func ShutdownNoop() func(context.Context) error {
	return func(context.Context) error { return nil }
}

// ShutdownWithTimeout wraps a shutdown function with a timeout.
func ShutdownWithTimeout(shutdown func(context.Context) error, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		// OTel shutdown errors are benign (best-effort flush).
		_ = err
	}
}
