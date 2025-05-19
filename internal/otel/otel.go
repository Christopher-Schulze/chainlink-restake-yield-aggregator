package otel

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
	"time"
)

func InitTracer(cfg config.Config) func() {
	if cfg.OtelEndpoint == "" {
		return func() {}
	}

	ctx := context.Background()
	client := otlptracehttp.NewClient(
		otlptracehttp.WithEndpoint(cfg.OtelEndpoint),
		otlptracehttp.WithInsecure(),
	)
	
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return func() {}
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("restake-yield-ea"),
		)),
	)
	otel.SetTracerProvider(tp)

	return func() {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(ctx)
	}
}

func Tracer() trace.Tracer {
	return otel.Tracer("restake-yield-ea")
}

func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}