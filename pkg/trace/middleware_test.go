package trace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	assert.Contains(attrs, "mcp.session_id", "mcp.session_id attribute should be set")
	assert.Equal("test-client", attrs["mcp.client.name"], "mcp.client.name should be captured from initialize handshake")
	assert.Equal("v0.0.1", attrs["mcp.client.version"], "mcp.client.version should be captured from initialize handshake")
	assert.Equal("ping", attrs["mcp.tool_name"], "mcp.tool_name should be set for tools/call requests")
}

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
	assert.NotEmpty(attrs["mcp.session_id"], "mcp.session_id should be non-empty over HTTP transport")
	assert.Equal("test-client", attrs["mcp.client.name"], "mcp.client.name should be captured from initialize handshake")
	assert.Equal("v0.0.1", attrs["mcp.client.version"], "mcp.client.version should be captured from initialize handshake")
	assert.Equal("ping", attrs["mcp.tool_name"], "mcp.tool_name should be set for tools/call requests")
}
