package buildkite

// formatJSONRecordsPerLine reformats a compact JSON document so that each
// element of an array of objects starts on its own line. The output is the
// same single JSON document — newlines are inserted only between tokens, so
// it parses identically to the input.
//
// Why: tool results are delivered as one JSON payload. When a payload exceeds
// an MCP client's size cap, clients like Claude Code offload it to a file for
// the model to read back. A compact payload is one giant line, which defeats
// line-oriented recovery (grep, line-ranged reads). Placing one record per
// line makes the offloaded file recoverable at near-zero size cost (one byte
// per record).
//
// Contract:
//   - Input must be a single valid JSON value in compact form (as produced by
//     json.Marshal): no whitespace between tokens. Arrays whose first element
//     is an object are treated as record arrays; other arrays are left inline.
//   - Output length = input length + number of newlines inserted.
//   - Idempotent: formatting already-formatted output changes nothing.
func formatJSONRecordsPerLine(in []byte) []byte {
	out := make([]byte, 0, len(in)+len(in)/64)
	// Container stack: '{' object, '[' inline array, 'R' record array
	// (an array of objects, whose element boundaries get newlines).
	var stack []byte
	inString := false
	escaped := false

	for i := 0; i < len(in); i++ {
		c := in[i]

		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
			out = append(out, c)
		case '{':
			stack = append(stack, '{')
			out = append(out, c)
		case '[':
			// Compact input puts the first element immediately after '['.
			if i+1 < len(in) && in[i+1] == '{' {
				stack = append(stack, 'R')
				out = append(out, '[', '\n')
			} else {
				stack = append(stack, '[')
				out = append(out, c)
			}
		case '}', ']':
			var top byte
			if len(stack) > 0 {
				top = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			}
			if c == ']' && top == 'R' {
				out = append(out, '\n')
			}
			out = append(out, c)
		case ',':
			out = append(out, c)
			if len(stack) > 0 && stack[len(stack)-1] == 'R' {
				out = append(out, '\n')
			}
		default:
			out = append(out, c)
		}
	}
	return out
}
