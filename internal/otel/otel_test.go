package otel

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestInitTracerNoop(t *testing.T) {
	shutdown, err := InitTracer("")
	require.NoError(t, err)
	assert.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}

func TestInitTracerStdout(t *testing.T) {
	// stdout exporter writes to stdout; we just verify it doesn't error.
	shutdown, err := InitTracer("stdout")
	require.NoError(t, err)
	assert.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}

func TestInitTracerOTLPHTTP(t *testing.T) {
	shutdown, err := InitTracer("http://localhost:4318")
	require.NoError(t, err)
	assert.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}

func TestInitTracerOTLPHTTPS(t *testing.T) {
	shutdown, err := InitTracer("https://collector.example.com:4318/v1/traces")
	require.NoError(t, err)
	assert.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}

func TestInitTracerOTLPHostPort(t *testing.T) {
	shutdown, err := InitTracer("localhost:4318")
	require.NoError(t, err)
	assert.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}

func TestSamplerDefault(t *testing.T) {
	os.Unsetenv("OTEL_TRACE_SAMPLE_RATIO")
	s := sampler()
	assert.NotNil(t, s)
}

func TestSamplerCustomRatio(t *testing.T) {
	t.Setenv("OTEL_TRACE_SAMPLE_RATIO", "1.0")
	s := sampler()
	assert.NotNil(t, s)
}

func TestSamplerInvalidRatio(t *testing.T) {
	t.Setenv("OTEL_TRACE_SAMPLE_RATIO", "not-a-number")
	s := sampler()
	assert.NotNil(t, s)
}

func TestSamplerOutOfRange(t *testing.T) {
	t.Setenv("OTEL_TRACE_SAMPLE_RATIO", "2.5")
	s := sampler()
	assert.NotNil(t, s)
}

func TestParseFloat(t *testing.T) {
	f, err := parseFloat("3.14")
	require.NoError(t, err)
	assert.InDelta(t, 3.14, f, 1e-9)
}

func TestParseFloatInvalid(t *testing.T) {
	_, err := parseFloat("abc")
	assert.Error(t, err)
}

func TestTracer(t *testing.T) {
	shutdown, _ := InitTracer("")
	defer func() { _ = shutdown(context.Background()) }()
	tr := Tracer()
	assert.NotNil(t, tr)
}

func TestRecordError(t *testing.T) {
	shutdown, _ := InitTracer("")
	defer func() { _ = shutdown(context.Background()) }()
	// Should not panic even without an active span.
	RecordError(context.Background(), assert.AnError)
}

func TestShutdownNonSDKProvider(t *testing.T) {
	// After a noop init + shutdown, calling Shutdown again should not error.
	shutdown, _ := InitTracer("")
	_ = shutdown(context.Background())
	// Shutdown with the global provider — should be nil or already shut down.
	err := Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestOtlpHTTPOptionsEmpty(t *testing.T) {
	_, err := otlpHTTPOptions("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty endpoint")
}

func TestOtlpHTTPOptionsWhitespace(t *testing.T) {
	_, err := otlpHTTPOptions("   ")
	require.Error(t, err)
}

func TestOtlpHTTPOptionsHTTP(t *testing.T) {
	opts, err := otlpHTTPOptions("http://localhost:4318")
	require.NoError(t, err)
	assert.NotEmpty(t, opts)
}

func TestOtlpHTTPOptionsHTTPSWithPath(t *testing.T) {
	opts, err := otlpHTTPOptions("https://collector.example.com:4318/v1/traces")
	require.NoError(t, err)
	assert.NotEmpty(t, opts)
}

func TestOtlpHTTPOptionsBareHost(t *testing.T) {
	opts, err := otlpHTTPOptions("localhost:4318")
	require.NoError(t, err)
	assert.NotEmpty(t, opts)
}

func TestOtlpHTTPOptionsInvalidURL(t *testing.T) {
	_, err := otlpHTTPOptions("://invalid")
	require.Error(t, err)
}

// TestShutdownWithoutSDKProvider covers the branch in Shutdown where the
// global TracerProvider is not an *sdktrace.TracerProvider (e.g. the default
// no-op provider installed before InitTracer is ever called). Shutdown should
// return nil without error.
func TestShutdownWithoutSDKProvider(t *testing.T) {
	// Force the global provider back to a no-op tracer provider so the type
	// assertion in Shutdown fails and the !ok branch is exercised.
	otel.SetTracerProvider(noop.NewTracerProvider())
	err := Shutdown(context.Background())
	assert.NoError(t, err)
}

// --- error-path tests using injectable exporter variables ---

// TestInitTracerStdoutExporterError covers the stdout exporter creation
// failure path in InitTracer (otel.go ~62-64).
func TestInitTracerStdoutExporterError(t *testing.T) {
	orig := newStdoutExporter
	newStdoutExporter = func(opts ...stdouttrace.Option) (sdktrace.SpanExporter, error) {
		return nil, errors.New("stdout exporter failed")
	}
	t.Cleanup(func() { newStdoutExporter = orig })

	_, err := InitTracer("stdout")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdout exporter")
}

// TestInitTracerOTLPExporterError covers the OTLP/HTTP exporter creation
// failure path in InitTracer (otel.go ~70-72).
func TestInitTracerOTLPExporterError(t *testing.T) {
	orig := newOTLPHTTPExporter
	newOTLPHTTPExporter = func(ctx context.Context, opts ...otlptracehttp.Option) (sdktrace.SpanExporter, error) {
		return nil, errors.New("otlp exporter failed")
	}
	t.Cleanup(func() { newOTLPHTTPExporter = orig })

	_, err := InitTracer("http://localhost:4318")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "otlp exporter")
}

// TestInitTracerResourceMergeError covers the resource.Merge failure path in
// InitTracer (otel.go ~83-84).
func TestInitTracerResourceMergeError(t *testing.T) {
	orig := resourceMergeFunc
	resourceMergeFunc = func(a, b *resource.Resource) (*resource.Resource, error) {
		return nil, errors.New("resource merge failed")
	}
	t.Cleanup(func() { resourceMergeFunc = orig })

	_, err := InitTracer("stdout")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource")
}

// TestInitTracerOTLPEndpointError covers the otlpHTTPOptions failure path in
// InitTracer (otel.go ~67-68). An endpoint with an invalid URL scheme causes
// url.Parse to fail inside otlpHTTPOptions.
func TestInitTracerOTLPEndpointError(t *testing.T) {
	_, err := InitTracer("://invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "otlp endpoint")
}
