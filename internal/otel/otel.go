// Package otel wires up OpenTelemetry tracing for the restake-yield-ea.
// When no OTLP endpoint is configured, tracing is a no-op so the adapter
// runs zero-dependency in development.
package otel

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

// Indirection variables allow tests to inject failures into the exporter
// creation functions, which are otherwise hard to make fail in normal
// operation. They default to the real implementations so production
// behaviour is unchanged.
var (
	// newStdoutExporter creates a stdout trace exporter. Tests can swap it
	// to return an error to exercise the stdout exporter failure path.
	newStdoutExporter = func(opts ...stdouttrace.Option) (sdktrace.SpanExporter, error) {
		return stdouttrace.New(opts...)
	}
	// newOTLPHTTPExporter creates an OTLP/HTTP trace exporter. Tests can
	// swap it to return an error to exercise the OTLP exporter failure path.
	newOTLPHTTPExporter = func(ctx context.Context, opts ...otlptracehttp.Option) (sdktrace.SpanExporter, error) {
		return otlptracehttp.New(ctx, opts...)
	}
	// resourceMergeFunc merges two resources. Tests can swap it to return
	// an error to exercise the resource merge failure path.
	resourceMergeFunc = resource.Merge
)

// InitTracer configures a global TracerProvider and returns a shutdown
// function the caller must defer. When endpoint is empty a no-op provider
// is installed. When endpoint is "stdout" traces are pretty-printed to
// stdout (useful for local debugging). Otherwise an OTLP/HTTP exporter is
// created targeting the given endpoint.
func InitTracer(endpoint string) (func(context.Context) error, error) {
	var (
		exp sdktrace.SpanExporter
		err error
	)
	switch {
	case endpoint == "":
		// No exporter: install a no-op provider.
		tp := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return tp.Shutdown, nil
	case endpoint == "stdout":
		exp, err = newStdoutExporter(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("stdout exporter: %w", err)
		}
	default:
		opts, err := otlpHTTPOptions(endpoint)
		if err != nil {
			return nil, fmt.Errorf("otlp endpoint: %w", err)
		}
		exp, err = newOTLPHTTPExporter(context.Background(), opts...)
		if err != nil {
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
	}

	res, err := resourceMergeFunc(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("restake-yield-ea"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler()),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// sampler returns a ratio-based sampler. The ratio is read from
// OTEL_TRACE_SAMPLE_RATIO (0.0–1.0); default is 0.1 (10%) which is a sane
// production default. Use 1.0 for full sampling in development.
func sampler() sdktrace.Sampler {
	ratio := 0.1
	if v := os.Getenv("OTEL_TRACE_SAMPLE_RATIO"); v != "" {
		if f, err := parseFloat(v); err == nil && f >= 0 && f <= 1 {
			ratio = f
		}
	}
	return sdktrace.TraceIDRatioBased(ratio)
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// Tracer returns the configured tracer for this service.
func Tracer() trace.Tracer {
	return otel.Tracer("restake-yield-ea")
}

// RecordError records an error on the active span (if any).
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

// Shutdown flushes and shuts down the global TracerProvider.
func Shutdown(ctx context.Context) error {
	tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	if !ok {
		return nil
	}
	return tp.Shutdown(ctx)
}

// otlpHTTPOptions converts an endpoint URL (e.g. "http://localhost:4318" or
// "https://collector.example.com:4318/v1/traces") into otlptracehttp.Option
// values. Plain "host:port" strings are treated as insecure HTTP endpoints.
func otlpHTTPOptions(endpoint string) ([]otlptracehttp.Option, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return nil, fmt.Errorf("empty endpoint")
	}

	var (
		host      string
		path      string
		insecure  bool
	)

	if strings.Contains(ep, "://") {
		u, err := url.Parse(ep)
		if err != nil {
			return nil, fmt.Errorf("parse endpoint: %w", err)
		}
		host = u.Host
		path = u.Path
		insecure = u.Scheme == "http"
	} else {
		host = ep
		insecure = true
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(host)}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if path != "" {
		opts = append(opts, otlptracehttp.WithURLPath(path))
	}
	return opts, nil
}
