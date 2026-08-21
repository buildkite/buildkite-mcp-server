package trace

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Keep user-controlled trace attributes within a predictable storage bound.
const telemetryContextMaxBytes = 512

func NewMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if telemetryContext := toolTelemetryContext(req); telemetryContext != "" {
				ctx = context.WithValue(ctx, telemetryContextKey{}, telemetryContext)
			}

			ctx, span := Start(ctx, fmt.Sprintf("mcp.%s", method))
			defer span.End()

			attrs := []attribute.KeyValue{
				attribute.String("mcp.method", method),
			}

			// Only legacy stateful Streamable HTTP sessions expose an ID.
			// Stateless HTTP, stdio, and in-memory transports return ""; use the
			// OTel trace/span IDs for correlation when no session ID is available.
			sessionID := req.GetSession().ID()
			if sessionID != "" {
				attrs = append(attrs, attribute.String("mcp.session_id", sessionID))
			}

			if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && params != nil {
				attrs = append(attrs, attribute.String("mcp.tool_name", params.Name))
			}

			var clientName, clientVersion string
			if ci := clientInfo(req); ci != nil && ci.Name != "" {
				clientName = ci.Name
				clientVersion = ci.Version
				attrs = append(attrs,
					attribute.String("mcp.client.name", clientName),
					attribute.String("mcp.client.version", clientVersion),
				)
			}

			span.SetAttributes(attrs...)

			logFields := func(e *zerolog.Event) *zerolog.Event {
				e = e.Str("mcp.method", method)
				if sessionID != "" {
					e = e.Str("mcp.session_id", sessionID)
				}
				if sc := span.SpanContext(); sc.IsValid() {
					e = e.Str("trace_id", sc.TraceID().String()).Str("span_id", sc.SpanID().String())
				}
				if clientName != "" {
					e = e.Str("mcp.client.name", clientName).Str("mcp.client.version", clientVersion)
				}
				return e
			}

			logFields(log.Debug()).Msg("Handling MCP request")

			res, err := next(ctx, method, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				logFields(log.Error().Err(err)).Msg("Error in MCP request")
			} else {
				span.SetStatus(codes.Ok, "OK")
				logFields(log.Debug()).Msg("Completed MCP request successfully")
			}

			return res, err
		}
	}
}

func toolTelemetryContext(req mcp.Request) string {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || params == nil || len(params.Arguments) == 0 {
		return ""
	}

	var args struct {
		Telemetry struct {
			Context string `json:"context"`
		} `json:"telemetry"`
	}
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return ""
	}

	if len(args.Telemetry.Context) > telemetryContextMaxBytes {
		return ""
	}

	return args.Telemetry.Context
}

// clientInfoRequest matches the SDK's typed ServerRequest.ClientInfo
// accessor, which is defined for every ServerRequest[P] instantiation but not
// exposed on the generic mcp.Request interface.
type clientInfoRequest interface {
	ClientInfo() *mcp.Implementation
}

// clientInfo returns the client identity for a request. Clients speaking
// protocol >= 2026-07-28 carry it in each request's _meta (there is no
// initialize handshake); older clients fall back to the session-level
// InitializeParams. The SDK accessor handles both, plus nil params.
func clientInfo(req mcp.Request) *mcp.Implementation {
	if r, ok := req.(clientInfoRequest); ok {
		return r.ClientInfo()
	}
	return nil
}
