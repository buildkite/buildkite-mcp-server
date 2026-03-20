package trace

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func NewMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx, span := Start(ctx, fmt.Sprintf("mcp.%s", method))
			defer span.End()

			sessionID := req.GetSession().ID()
			attrs := []attribute.KeyValue{
				attribute.String("mcp.method", method),
				attribute.String("mcp.session_id", sessionID),
			}

			if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && params != nil {
				attrs = append(attrs, attribute.String("mcp.tool_name", params.Name))
			}

			var clientName, clientVersion string
			if ss, ok := req.GetSession().(*mcp.ServerSession); ok {
				if ip := ss.InitializeParams(); ip != nil && ip.ClientInfo != nil && ip.ClientInfo.Name != "" {
					clientName = ip.ClientInfo.Name
					clientVersion = ip.ClientInfo.Version
					attrs = append(attrs,
						attribute.String("mcp.client.name", clientName),
						attribute.String("mcp.client.version", clientVersion),
					)
				}
			}

			span.SetAttributes(attrs...)

			baseLog := log.Debug().Str("mcp.method", method).Str("mcp.session_id", sessionID)
			if clientName != "" {
				baseLog = baseLog.Str("mcp.client.name", clientName).Str("mcp.client.version", clientVersion)
			}
			baseLog.Msg("Handling MCP request")

			res, err := next(ctx, method, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				errLog := log.Error().Err(err).Str("mcp.method", method).Str("mcp.session_id", sessionID)
				if clientName != "" {
					errLog = errLog.Str("mcp.client.name", clientName).Str("mcp.client.version", clientVersion)
				}
				errLog.Msg("Error in MCP request")
			} else {
				span.SetStatus(codes.Ok, "OK")
				completedLog := log.Debug().Str("mcp.method", method).Str("mcp.session_id", sessionID)
				if clientName != "" {
					completedLog = completedLog.Str("mcp.client.name", clientName).Str("mcp.client.version", clientVersion)
				}
				completedLog.Msg("Completed MCP request successfully")
			}

			return res, err
		}
	}
}
