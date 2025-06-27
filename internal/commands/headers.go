package commands

import (
	"strings"

	"github.com/rs/zerolog"
)

// ParseHeaders takes a slice of header strings in the format "Key: Value"
// and returns a map of headers. This is used to parse additional HTTP headers
// that can be sent with every request to the Buildkite API.
func ParseHeaders(headerStrings []string, logger zerolog.Logger) map[string]string {
	headers := make(map[string]string)
	for _, h := range headerStrings {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			headers[key] = value
			logger.Debug().Str("key", key).Str("value", value).Msg("parsed header")
		} else {
			logger.Warn().Str("header", h).Msg("invalid header format, expected 'Key: Value'")
		}
	}
	return headers
}
