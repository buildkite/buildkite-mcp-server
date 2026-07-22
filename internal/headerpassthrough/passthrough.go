package headerpassthrough

import (
	"context"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"
)

const authorizationHeader = "Authorization"

var unsupportedHeaderNames = map[string]struct{}{
	"Connection":          {},
	"Content-Length":      {},
	"Host":                {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// Config forwards selected headers from an inbound MCP request to API
// requests on the configured Buildkite origin.
type Config struct {
	headerNames       []string
	targetScheme      string
	targetHost        string
	usesAuthorization bool
}

func New(headerNames []string, fixedHeaders map[string]string, baseURL string) (*Config, error) {
	target, err := url.Parse(baseURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("buildkite base URL for HTTP header passthrough must be an absolute HTTP or HTTPS URL")
	}

	fixed := make(map[string]struct{}, len(fixedHeaders))
	for name := range fixedHeaders {
		fixed[textproto.CanonicalMIMEHeaderKey(name)] = struct{}{}
	}

	config := &Config{
		targetScheme: strings.ToLower(target.Scheme),
		targetHost:   strings.ToLower(target.Host),
	}
	seen := make(map[string]struct{}, len(headerNames))
	for _, name := range headerNames {
		name = strings.TrimSpace(name)
		if !httpguts.ValidHeaderFieldName(name) {
			return nil, fmt.Errorf("invalid passthrough HTTP header name %q", name)
		}

		name = textproto.CanonicalMIMEHeaderKey(name)
		if _, ok := unsupportedHeaderNames[name]; ok {
			return nil, fmt.Errorf("HTTP header %q cannot be passed through because it is managed by the HTTP transport", name)
		}
		if _, ok := fixed[name]; ok {
			return nil, fmt.Errorf("HTTP header %q cannot be both fixed and passed through", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}

		seen[name] = struct{}{}
		config.headerNames = append(config.headerNames, name)
		config.usesAuthorization = config.usesAuthorization || name == authorizationHeader
	}

	return config, nil
}

func (c *Config) UsesAuthorization() bool {
	return c.usesAuthorization
}

type contextKey struct{}

// WrapHandler captures allow-listed headers in the MCP request context.
func (c *Config) WrapHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.usesAuthorization {
			values := r.Header.Values(authorizationHeader)
			if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="buildkite"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		}

		headers := make(http.Header, len(c.headerNames))
		for _, name := range c.headerNames {
			headers[name] = append([]string(nil), r.Header.Values(name)...)
		}

		ctx := context.WithValue(r.Context(), contextKey{}, headers)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WrapTransport applies request-scoped headers on the Buildkite origin and
// removes them from other origins, including artifact redirects.
func (c *Config) WrapTransport(next http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		cloned := req.Clone(req.Context())
		cloned.Header = req.Header.Clone()
		if cloned.Header == nil {
			cloned.Header = make(http.Header)
		}

		for _, name := range c.headerNames {
			cloned.Header.Del(name)
		}

		if strings.EqualFold(cloned.URL.Scheme, c.targetScheme) && strings.EqualFold(cloned.URL.Host, c.targetHost) {
			headers, _ := cloned.Context().Value(contextKey{}).(http.Header)
			for _, name := range c.headerNames {
				cloned.Header[name] = append([]string(nil), headers.Values(name)...)
			}
		}

		return next.RoundTrip(cloned)
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
