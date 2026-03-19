package trace

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

// set a default tracer name
var tracerName = "buildkite-mcp-server"

func NewProvider(ctx context.Context, exporter, name, version string) (*sdktrace.TracerProvider, error) {
	exp, err := newExporter(ctx, exporter)
	if err != nil {
		return nil, fmt.Errorf("failed to create exporter: %w", err)
	}

	res, err := newResource(ctx, name, version)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	tracerName = name

	return tp, nil
}

func Start(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.GetTracerProvider().Tracer(tracerName).Start(ctx, name)
}

func NewError(span trace.Span, msg string, args ...any) error {
	if span == nil {
		return fmt.Errorf("span is nil: %w", fmt.Errorf(msg, args...))
	}

	span.RecordError(fmt.Errorf(msg, args...))
	span.SetStatus(codes.Error, msg)

	return fmt.Errorf(msg, args...)
}

func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
}

// NewHTTPClientWithHeaders returns an http.Client that injects the provided headers into every request.
func NewHTTPClientWithHeaders(headers map[string]string) *http.Client {
	return &http.Client{
		Transport: &headerInjector{
			headers: headers,
			wrapped: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

type headerInjector struct {
	headers map[string]string
	wrapped http.RoundTripper
}

func (h *headerInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	return h.wrapped.RoundTrip(req)
}

func newResource(cxt context.Context, name, version string) (*resource.Resource, error) {
	options := []resource.Option{
		resource.WithSchemaURL(semconv.SchemaURL),
	}
	options = append(options, resource.WithHost())
	options = append(options, resource.WithFromEnv())
	options = append(options, resource.WithAttributes(
		semconv.TelemetrySDKNameKey.String("otelconfig"),
		semconv.TelemetrySDKLanguageGo,
		semconv.TelemetrySDKVersionKey.String(version),
	))

	return resource.New(
		cxt,
		options...,
	)
}

func newExporter(ctx context.Context, exporter string) (sdktrace.SpanExporter, error) {
	switch exporter {
	case "http/protobuf":
		return otlptracehttp.New(ctx)
	case "grpc":
		return otlptracegrpc.New(ctx)
	default:
		return tracetest.NewNoopExporter(), nil
	}
}
