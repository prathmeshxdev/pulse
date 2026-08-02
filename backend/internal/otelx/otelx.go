// Package otelx wires OpenTelemetry traces, metrics, and logs for ClickStack.
// Opt-in via OTEL_EXPORTER_OTLP_ENDPOINT (e.g. http://localhost:4318). When unset,
// all helpers are no-ops.
package otelx

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/log"
	logglobal "go.opentelemetry.io/otel/log/global"
)

const serviceName = "pulse-concurrency-api"

var (
	mu       sync.RWMutex
	reqCount otelmetric.Int64Counter
	reqErrs  otelmetric.Int64Counter
	reqDur   otelmetric.Float64Histogram
	chDur    otelmetric.Float64Histogram
	logger   log.Logger
)

// Setup returns a tracer and a shutdown func. Enables traces + metrics + logs
// when OTEL_EXPORTER_OTLP_ENDPOINT is set.
func Setup(ctx context.Context) (trace.Tracer, func(context.Context) error, bool) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return otel.Tracer(serviceName), func(context.Context) error { return nil }, false
	}

	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(os.Getenv("PULSE_VERSION")),
			attribute.String("deployment.environment", envOr("OTEL_RESOURCE_ATTRIBUTES_DEPLOYMENT_ENVIRONMENT", "hackathon")),
		),
	)

	insecure := strings.HasPrefix(endpoint, "http://")
	var shutdowns []func(context.Context) error

	// --- traces ---
	tOpts := []otlptracehttp.Option{}
	if insecure {
		tOpts = append(tOpts, otlptracehttp.WithInsecure())
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	texp, err := otlptracehttp.New(dialCtx, tOpts...)
	cancel()
	if err != nil {
		return otel.Tracer(serviceName), func(context.Context) error { return nil }, false
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(texp, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	shutdowns = append(shutdowns, tp.Shutdown)

	// --- metrics ---
	mOpts := []otlpmetrichttp.Option{}
	if insecure {
		mOpts = append(mOpts, otlpmetrichttp.WithInsecure())
	}
	mexp, err := otlpmetrichttp.New(ctx, mOpts...)
	if err == nil {
		mp := metric.NewMeterProvider(
			metric.WithReader(metric.NewPeriodicReader(mexp, metric.WithInterval(10*time.Second))),
			metric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
		shutdowns = append(shutdowns, mp.Shutdown)
		initInstruments(mp.Meter(serviceName))
	}

	// --- logs ---
	lOpts := []otlploghttp.Option{}
	if insecure {
		lOpts = append(lOpts, otlploghttp.WithInsecure())
	}
	lexp, err := otlploghttp.New(ctx, lOpts...)
	if err == nil {
		lp := sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(lexp)),
			sdklog.WithResource(res),
		)
		logglobal.SetLoggerProvider(lp)
		shutdowns = append(shutdowns, lp.Shutdown)
		mu.Lock()
		logger = lp.Logger(serviceName)
		mu.Unlock()
	}

	return tp.Tracer(serviceName), func(ctx context.Context) error {
		var first error
		for i := len(shutdowns) - 1; i >= 0; i-- {
			if err := shutdowns[i](ctx); err != nil && first == nil {
				first = err
			}
		}
		return first
	}, true
}

func initInstruments(m otelmetric.Meter) {
	mu.Lock()
	defer mu.Unlock()
	reqCount, _ = m.Int64Counter("pulse.http.requests",
		otelmetric.WithDescription("Pulse API requests"),
		otelmetric.WithUnit("{request}"))
	reqErrs, _ = m.Int64Counter("pulse.http.errors",
		otelmetric.WithDescription("Pulse API errors"),
		otelmetric.WithUnit("{error}"))
	reqDur, _ = m.Float64Histogram("pulse.http.duration_ms",
		otelmetric.WithDescription("End-to-end handler latency"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(10, 25, 50, 100, 200, 400, 800, 1600, 3200, 6400))
	chDur, _ = m.Float64Histogram("pulse.clickhouse.duration_ms",
		otelmetric.WithDescription("ClickHouse query latency inside handlers"),
		otelmetric.WithUnit("ms"),
		otelmetric.WithExplicitBucketBoundaries(10, 25, 50, 100, 200, 400, 800, 1600, 3200, 6400))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// Attr helpers kept local so callers don't import attribute directly.
func StringAttr(k, v string) attribute.KeyValue { return attribute.String(k, v) }
func IntAttr(k string, v int) attribute.KeyValue { return attribute.Int(k, v) }
func BoolAttr(k string, v bool) attribute.KeyValue { return attribute.Bool(k, v) }
func Int64Attr(k string, v int64) attribute.KeyValue { return attribute.Int64(k, v) }
func FloatAttr(k string, v float64) attribute.KeyValue { return attribute.Float64(k, v) }

// InitCLI configures OTel for batch commands (loadraw, build_segments, pipeline).
func InitCLI(ctx context.Context) func(context.Context) error {
	_, shutdown, _ := Setup(ctx)
	return shutdown
}

// Start begins a span on the global tracer (no-op when OTel is disabled).
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(serviceName).Start(ctx, name, trace.WithAttributes(attrs...))
}

// ObserveRequest records latency/error metrics and a structured log line for one handler.
func ObserveRequest(ctx context.Context, span trace.Span, route string, started time.Time, err error, attrs ...attribute.KeyValue) {
	ms := float64(time.Since(started).Milliseconds())
	status := "ok"
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.AddEvent("error", trace.WithAttributes(
			attribute.String("error.message", err.Error()),
			attribute.String("http.route", route),
		))
		Error(ctx, "request failed", append(attrs,
			StringAttr("http.route", route),
			StringAttr("error.message", err.Error()),
			FloatAttr("duration_ms", ms),
		)...)
	} else {
		span.SetStatus(codes.Ok, "")
		Info(ctx, "request ok", append(attrs,
			StringAttr("http.route", route),
			FloatAttr("duration_ms", ms),
		)...)
	}
	span.SetAttributes(
		attribute.Float64("duration_ms", ms),
		attribute.String("http.route", route),
		attribute.String("status", status),
	)
	span.AddEvent("request.complete", trace.WithAttributes(
		attribute.Float64("duration_ms", ms),
		attribute.String("status", status),
	))

	mu.RLock()
	rc, re, rd := reqCount, reqErrs, reqDur
	mu.RUnlock()
	oattrs := otelmetric.WithAttributes(
		attribute.String("http.route", route),
		attribute.String("status", status),
	)
	if rc != nil {
		rc.Add(ctx, 1, oattrs)
	}
	if re != nil && err != nil {
		re.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("http.route", route)))
	}
	if rd != nil {
		rd.Record(ctx, ms, oattrs)
	}
}

// ObserveQuery records ClickHouse query timing on the active span + histogram.
func ObserveQuery(ctx context.Context, span trace.Span, label string, started time.Time, err error) {
	ms := float64(time.Since(started).Milliseconds())
	span.SetAttributes(attribute.Float64("db.duration_ms", ms))
	span.AddEvent("db.query", trace.WithAttributes(
		attribute.String("db.operation", label),
		attribute.Float64("duration_ms", ms),
		attribute.Bool("error", err != nil),
	))
	if err != nil {
		span.RecordError(err)
		Error(ctx, "clickhouse query failed",
			StringAttr("db.operation", label),
			StringAttr("error.message", err.Error()),
			FloatAttr("duration_ms", ms),
		)
	}
	mu.RLock()
	h := chDur
	mu.RUnlock()
	if h != nil {
		h.Record(ctx, ms, otelmetric.WithAttributes(
			attribute.String("db.operation", label),
			attribute.String("status", map[bool]string{true: "error", false: "ok"}[err != nil]),
		))
	}
}

// Info emits an OTLP log (and a span event when a span is present).
func Info(ctx context.Context, msg string, attrs ...attribute.KeyValue) {
	emit(ctx, log.SeverityInfo, "INFO", msg, attrs...)
}

// Error emits an OTLP error log (and a span event when a span is present).
func Error(ctx context.Context, msg string, attrs ...attribute.KeyValue) {
	emit(ctx, log.SeverityError, "ERROR", msg, attrs...)
}

func emit(ctx context.Context, sev log.Severity, sevText, msg string, attrs ...attribute.KeyValue) {
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.AddEvent("log", trace.WithAttributes(append([]attribute.KeyValue{
			attribute.String("log.severity", sevText),
			attribute.String("log.body", msg),
		}, attrs...)...))
	}
	mu.RLock()
	l := logger
	mu.RUnlock()
	if l == nil {
		return
	}
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetSeverity(sev)
	rec.SetSeverityText(sevText)
	rec.SetBody(log.StringValue(msg))
	kvs := make([]log.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		kvs = append(kvs, attrToLogKV(a))
	}
	rec.AddAttributes(kvs...)
	l.Emit(ctx, rec)
}

func attrToLogKV(a attribute.KeyValue) log.KeyValue {
	switch a.Value.Type() {
	case attribute.STRING:
		return log.String(string(a.Key), a.Value.AsString())
	case attribute.INT64:
		return log.Int64(string(a.Key), a.Value.AsInt64())
	case attribute.FLOAT64:
		return log.Float64(string(a.Key), a.Value.AsFloat64())
	case attribute.BOOL:
		return log.Bool(string(a.Key), a.Value.AsBool())
	default:
		return log.String(string(a.Key), fmt.Sprint(a.Value.AsInterface()))
	}
}
