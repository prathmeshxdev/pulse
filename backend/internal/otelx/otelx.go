// Package otelx wires OpenTelemetry tracing for the ClickStack integration.
// It is opt-in: with no OTEL_EXPORTER_OTLP_ENDPOINT set it returns a no-op
// tracer and the server runs exactly as before. When set (e.g. the ClickStack
// / HyperDX OTLP endpoint), every concurrency query emits a span carrying grain,
// filter count, cache hit, and error — so query latency and error rate are
// observable on the real pipeline in HyperDX.
package otelx

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "pulse-concurrency-api"

// Setup returns a tracer and a shutdown func. If OTEL_EXPORTER_OTLP_ENDPOINT is
// unset the tracer is a no-op and shutdown is a no-op — zero runtime cost.
func Setup(ctx context.Context) (trace.Tracer, func(context.Context) error, bool) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return otel.Tracer(serviceName), func(context.Context) error { return nil }, false
	}

	// otlptracehttp reads OTEL_EXPORTER_OTLP_ENDPOINT / _HEADERS from the env;
	// WithInsecure covers local http:// ClickStack without TLS.
	opts := []otlptracehttp.Option{}
	if len(endpoint) > 7 && endpoint[:7] == "http://" {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	exp, err := otlptracehttp.New(dialCtx, opts...)
	if err != nil {
		// Never fail the server because observability is unavailable.
		return otel.Tracer(serviceName), func(context.Context) error { return nil }, false
	}
	res, _ := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(serviceName)))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Tracer(serviceName), tp.Shutdown, true
}

// Attr helpers kept local so callers don't import attribute directly.
func StringAttr(k, v string) attribute.KeyValue { return attribute.String(k, v) }
func IntAttr(k string, v int) attribute.KeyValue { return attribute.Int(k, v) }
func BoolAttr(k string, v bool) attribute.KeyValue { return attribute.Bool(k, v) }
