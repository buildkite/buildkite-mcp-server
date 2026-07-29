package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// setupMiddlewareServer creates a server with the trace middleware and a no-op ping tool,
// wiring an in-memory span recorder as the global tracer provider.
func setupMiddlewareServer(t *testing.T) (*mcp.Server, *tracetest.SpanRecorder) {
	t.Helper()
	ctx := context.Background()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	server.AddReceivingMiddleware(NewMiddleware())
	mcp.AddTool(server, &mcp.Tool{Name: "ping"}, func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})

	return server, sr
}

// spanAttrs flushes the provider and returns the attributes of the named span as a string map.
func spanAttrs(t *testing.T, tp *sdktrace.TracerProvider, sr *tracetest.SpanRecorder, spanName string) map[string]string {
	t.Helper()
	assert := require.New(t)

	assert.NoError(tp.ForceFlush(context.Background()))
	spans := sr.Ended()
	assert.NotEmpty(spans, "expected at least one span to be recorded")

	for _, s := range spans {
		if s.Name() == spanName {
			attrs := map[string]string{}
			for _, a := range s.Attributes() {
				attrs[string(a.Key)] = a.Value.AsString()
			}
			return attrs
		}
	}

	t.Fatalf("expected a span named %q", spanName)
	return nil
}

func TestNewMiddleware(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	server, sr := setupMiddlewareServer(t)

	t1, t2 := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	assert.NoError(err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	assert.NoError(err)
	defer session.Close()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	assert.NoError(err)

	tp := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	attrs := spanAttrs(t, tp, sr, "mcp.tools/call")
	assert.Equal("tools/call", attrs["mcp.method"], "mcp.method attribute should be set")
	assert.NotContains(attrs, "mcp.session_id", "mcp.session_id should be omitted for sessionless (2026-07-28) requests")
	assert.Equal("test-client", attrs["mcp.client.name"], "mcp.client.name should be captured from per-request _meta")
	assert.Equal("v0.0.1", attrs["mcp.client.version"], "mcp.client.version should be captured from per-request _meta")
	assert.Equal("ping", attrs["mcp.tool_name"], "mcp.tool_name should be set for tools/call requests")
}

// captureLogs redirects the global zerolog logger to a buffer for the
// duration of the test and returns it.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = orig })
	return &buf
}

// logLines decodes each JSON log line in buf whose mcp.method matches.
func logLines(t *testing.T, buf *bytes.Buffer, method string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &line), "log line should be valid JSON: %s", raw)
		if line["mcp.method"] == method {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestNewMiddlewareLogCorrelation verifies that on the sessionless
// (2026-07-28) path the request log lines omit mcp.session_id but carry
// matching trace_id/span_id fields so they can be correlated with each other
// and with the emitted span.
func TestNewMiddlewareLogCorrelation(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	buf := captureLogs(t)
	server, sr := setupMiddlewareServer(t)

	t1, t2 := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	assert.NoError(err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	assert.NoError(err)
	defer session.Close()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	assert.NoError(err)

	lines := logLines(t, buf, "tools/call")
	assert.Len(lines, 2, "expected a handling and a completed log line for tools/call")

	for _, line := range lines {
		assert.NotContains(line, "mcp.session_id", "mcp.session_id should be omitted for sessionless requests")
		assert.NotEmpty(line["trace_id"], "trace_id should be set for log correlation")
		assert.NotEmpty(line["span_id"], "span_id should be set for log correlation")
	}
	assert.Equal(lines[0]["trace_id"], lines[1]["trace_id"], "both log lines should share a trace_id")
	assert.Equal(lines[0]["span_id"], lines[1]["span_id"], "both log lines should share a span_id")

	// The logged trace_id must match the span emitted for the request.
	tp := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	assert.NoError(tp.ForceFlush(ctx))
	for _, s := range sr.Ended() {
		if s.Name() == "mcp.tools/call" {
			assert.Equal(s.SpanContext().TraceID().String(), lines[0]["trace_id"], "logged trace_id should match the span's trace ID")
			return
		}
	}
	t.Fatal("expected a span named mcp.tools/call")
}

// TestNewMiddlewareHTTP covers the legacy (pre-2026-07-28) protocol path: a
// stateful StreamableHTTP handler negotiates an initialize handshake and
// assigns a session ID via the Mcp-Session-Id header.
func TestNewMiddlewareHTTP(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	server, sr := setupMiddlewareServer(t)

	// Serve via StreamableHTTP so a real session ID is assigned via Mcp-Session-Id header.
	handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	assert.NoError(err)
	defer session.Close()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	assert.NoError(err)

	tp := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	attrs := spanAttrs(t, tp, sr, "mcp.tools/call")
	assert.Equal("tools/call", attrs["mcp.method"], "mcp.method attribute should be set")
	assert.NotEmpty(attrs["mcp.session_id"], "mcp.session_id should be non-empty over stateful HTTP transport")
	assert.Equal("test-client", attrs["mcp.client.name"], "mcp.client.name should be captured from initialize handshake")
	assert.Equal("v0.0.1", attrs["mcp.client.version"], "mcp.client.version should be captured from initialize handshake")
	assert.Equal("ping", attrs["mcp.tool_name"], "mcp.tool_name should be set for tools/call requests")
}

// TestNewMiddlewareHTTPStateless covers the 2026-07-28 protocol path used in
// production (internal/commands/http.go): a stateless StreamableHTTP handler
// with no sessions, where client identity travels in each request's _meta.
func TestNewMiddlewareHTTPStateless(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	server, sr := setupMiddlewareServer(t)

	handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	assert.NoError(err)
	defer session.Close()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	assert.NoError(err)

	tp := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	attrs := spanAttrs(t, tp, sr, "mcp.tools/call")
	assert.Equal("tools/call", attrs["mcp.method"], "mcp.method attribute should be set")
	assert.NotContains(attrs, "mcp.session_id", "mcp.session_id should be omitted over stateless HTTP transport")
	assert.Equal("test-client", attrs["mcp.client.name"], "mcp.client.name should be captured from per-request _meta")
	assert.Equal("v0.0.1", attrs["mcp.client.version"], "mcp.client.version should be captured from per-request _meta")
	assert.Equal("ping", attrs["mcp.tool_name"], "mcp.tool_name should be set for tools/call requests")
}
