package otel

import (
	"context"
	"time"
	
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func InitTracer(cfg config.Config) func() {
	if cfg.OtelEndpoint == "" {
		return func() {}
	}

	tp, err := InitTracer()
	if err != nil {
		return func() {}
	}

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Shutdown(ctx)
	}
}

func InitTracer() (trace.Tracer, error) {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	return tp.Tracer("restake-yield-ea"), nil
}

func Tracer() trace.Tracer {
	return otel.Tracer("restake-yield-ea")
}

func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

func Shutdown(ctx context.Context) error {
	if tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		return tp.Shutdown(ctx)
	}
	return nil
}