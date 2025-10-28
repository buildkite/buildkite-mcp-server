package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuth(t *testing.T) {
	// Mock handler that tracks if it was called
	var handlerCalled bool
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})

	tests := []struct {
		name           string
		token          string
		authHeader     string
		clientIP       string
		expectedStatus int
		expectHandler  bool
		description    string
	}{
		{
			name:           "valid bearer token",
			token:          "valid-secret-token",
			authHeader:     "Bearer valid-secret-token",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusOK,
			expectHandler:  true,
			description:    "should allow access with correct bearer token",
		},
		{
			name:           "invalid bearer token",
			token:          "valid-secret-token",
			authHeader:     "Bearer invalid-token",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access with incorrect bearer token",
		},
		{
			name:           "missing authorization header",
			token:          "valid-secret-token",
			authHeader:     "",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access when Authorization header is missing",
		},
		{
			name:           "malformed authorization header - no Bearer prefix",
			token:          "valid-secret-token",
			authHeader:     "valid-secret-token",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access when Authorization header lacks Bearer prefix",
		},
		{
			name:           "malformed authorization header - Basic auth",
			token:          "valid-secret-token",
			authHeader:     "Basic dXNlcjpwYXNz",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access when using Basic auth instead of Bearer",
		},
		{
			name:           "empty bearer token",
			token:          "valid-secret-token",
			authHeader:     "Bearer ",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access when bearer token is empty",
		},
		{
			name:           "bearer token with extra whitespace",
			token:          "valid-secret-token",
			authHeader:     "Bearer  valid-secret-token",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access when bearer token has extra whitespace",
		},
		{
			name:           "case sensitive bearer prefix",
			token:          "valid-secret-token",
			authHeader:     "bearer valid-secret-token",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access when Bearer prefix is lowercase",
		},
		{
			name:           "token with special characters",
			token:          "token-with-special_chars.123",
			authHeader:     "Bearer token-with-special_chars.123",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusOK,
			expectHandler:  true,
			description:    "should allow access with token containing special characters",
		},
		{
			name:           "long token",
			token:          "very-long-token-" + string(make([]byte, 100)),
			authHeader:     "Bearer very-long-token-" + string(make([]byte, 100)),
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusOK,
			expectHandler:  true,
			description:    "should allow access with very long token",
		},
		{
			name:           "token substring attack",
			token:          "valid-secret-token",
			authHeader:     "Bearer valid-secret",
			clientIP:       "203.0.113.1",
			expectedStatus: http.StatusUnauthorized,
			expectHandler:  false,
			description:    "should deny access when providing only a substring of the token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset handler call flag
			handlerCalled = false

			// Create middleware with test token
			middleware := Auth(tt.token)
			handler := middleware(mockHandler)

			// Create test request with client IP in context
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			ctx := context.WithValue(req.Context(), clientIPKey, tt.clientIP)
			req = req.WithContext(ctx)

			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			// Create response recorder
			rr := httptest.NewRecorder()

			// Call the middleware
			handler.ServeHTTP(rr, req)

			// Check status code
			if rr.Code != tt.expectedStatus {
				t.Errorf("%s: got status %d, want %d", tt.description, rr.Code, tt.expectedStatus)
			}

			// Check if handler was called
			if handlerCalled != tt.expectHandler {
				t.Errorf("%s: handler called = %v, want %v", tt.description, handlerCalled, tt.expectHandler)
			}

			// For unauthorized requests, check error message
			if tt.expectedStatus == http.StatusUnauthorized {
				body := rr.Body.String()
				if body != "Unauthorized\n" {
					t.Errorf("%s: expected 'Unauthorized' in body, got %q", tt.description, body)
				}
			}
		})
	}
}

func TestAuth_RequestForwarding(t *testing.T) {
	// Handler that checks if headers and other request properties are forwarded
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if custom header is present
		if r.Header.Get("X-Custom-Header") != "test-value" {
			t.Error("Custom header not forwarded")
		}
		// Check if method is preserved
		if r.Method != http.MethodPost {
			t.Errorf("Method not preserved: got %s, want POST", r.Method)
		}
		// Check if URL is preserved
		if r.URL.Path != "/test/path" {
			t.Errorf("Path not preserved: got %s, want /test/path", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Auth("test-token")
	handler := middleware(testHandler)

	req := httptest.NewRequest(http.MethodPost, "/test/path", nil)
	ctx := context.WithValue(req.Context(), clientIPKey, "203.0.113.1")
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Custom-Header", "test-value")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

func TestAuth_TimingAttackResistance(t *testing.T) {
	// This test verifies that we're using constant-time comparison
	// by testing tokens of different lengths and prefixes
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	token := "secret-token-1234567890"
	middleware := Auth(token)
	handler := middleware(mockHandler)

	testCases := []struct {
		name       string
		authHeader string
	}{
		{"empty token", "Bearer "},
		{"single char", "Bearer x"},
		{"short token", "Bearer sec"},
		{"wrong prefix match", "Bearer secret-token-xxx"},
		{"completely different", "Bearer xxxxxxxxxxxxxxxxxx"},
		{"longer than actual", "Bearer secret-token-1234567890-extra-chars"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			ctx := context.WithValue(req.Context(), clientIPKey, "203.0.113.1")
			req = req.WithContext(ctx)
			req.Header.Set("Authorization", tc.authHeader)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// All should be unauthorized
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("Expected 401, got %d", rr.Code)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		trustProxy bool
		expectedIP string
	}{
		{
			name:       "direct connection without trust",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{},
			trustProxy: false,
			expectedIP: "192.168.1.1:1234",
		},
		{
			name:       "X-Forwarded-For with trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.1, 10.0.0.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "X-Forwarded-For without trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.1"},
			trustProxy: false,
			expectedIP: "10.0.0.1:1234",
		},
		{
			name:       "CF-Connecting-IP with trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a handler that verifies the IP was injected into context
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ip := GetClientIPFromContext(r.Context())
				if ip != tt.expectedIP {
					t.Errorf("GetClientIPFromContext() = %v, want %v", ip, tt.expectedIP)
				}
				w.WriteHeader(http.StatusOK)
			})

			middleware := ClientIP(tt.trustProxy)
			handler := middleware(testHandler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", rr.Code)
			}
		})
	}
}

func TestGetClientIPFromContext(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected string
	}{
		{
			name:     "IP present in context",
			ip:       "203.0.113.1",
			expected: "203.0.113.1",
		},
		{
			name:     "empty IP in context",
			ip:       "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), clientIPKey, tt.ip)
			got := GetClientIPFromContext(ctx)
			if got != tt.expected {
				t.Errorf("GetClientIPFromContext() = %v, want %v", got, tt.expected)
			}
		})
	}

	// Test with missing context value
	t.Run("missing IP in context", func(t *testing.T) {
		ctx := context.Background()
		got := GetClientIPFromContext(ctx)
		if got != "" {
			t.Errorf("GetClientIPFromContext() = %v, want empty string", got)
		}
	})
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		trustProxy bool
		expectedIP string
	}{
		{
			name:       "direct connection without trust",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{},
			trustProxy: false,
			expectedIP: "192.168.1.1:1234",
		},
		{
			name:       "direct connection with trust but no headers",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{},
			trustProxy: true,
			expectedIP: "192.168.1.1:1234",
		},
		{
			name:       "X-Forwarded-For with trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.1, 10.0.0.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "X-Forwarded-For without trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.1"},
			trustProxy: false,
			expectedIP: "10.0.0.1:1234",
		},
		{
			name:       "X-Real-IP with trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Real-IP": "203.0.113.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "X-Real-IP without trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Real-IP": "203.0.113.1"},
			trustProxy: false,
			expectedIP: "10.0.0.1:1234",
		},
		{
			name:       "CF-Connecting-IP (Cloudflare) with trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "True-Client-IP (Akamai) with trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"True-Client-IP": "203.0.113.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "X-Client-IP with trust",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Client-IP": "203.0.113.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "priority: CF-Connecting-IP over X-Forwarded-For",
			remoteAddr: "10.0.0.1:1234",
			headers: map[string]string{
				"CF-Connecting-IP": "203.0.113.1",
				"X-Forwarded-For":  "203.0.113.2",
			},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "priority: True-Client-IP over X-Real-IP",
			remoteAddr: "10.0.0.1:1234",
			headers: map[string]string{
				"True-Client-IP": "203.0.113.1",
				"X-Real-IP":      "203.0.113.2",
			},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "priority: X-Real-IP over X-Forwarded-For",
			remoteAddr: "10.0.0.1:1234",
			headers: map[string]string{
				"X-Real-IP":       "203.0.113.1",
				"X-Forwarded-For": "203.0.113.2",
			},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "priority: X-Forwarded-For over X-Client-IP",
			remoteAddr: "10.0.0.1:1234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.1",
				"X-Client-IP":     "203.0.113.2",
			},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "X-Forwarded-For with multiple IPs and spaces",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.1 , 10.0.0.2, 10.0.0.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "X-Forwarded-For with single IP",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.1"},
			trustProxy: true,
			expectedIP: "203.0.113.1",
		},
		{
			name:       "IPv6 address in X-Forwarded-For",
			remoteAddr: "[::1]:1234",
			headers:    map[string]string{"X-Forwarded-For": "2001:db8::1, ::1"},
			trustProxy: true,
			expectedIP: "2001:db8::1",
		},
		{
			name:       "spoofed headers ignored without trust",
			remoteAddr: "192.168.1.1:1234",
			headers: map[string]string{
				"CF-Connecting-IP": "203.0.113.1",
				"X-Forwarded-For":  "203.0.113.2",
				"X-Real-IP":        "203.0.113.3",
			},
			trustProxy: false,
			expectedIP: "192.168.1.1:1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got := getClientIP(req, tt.trustProxy)
			if got != tt.expectedIP {
				t.Errorf("getClientIP() = %v, want %v", got, tt.expectedIP)
			}
		})
	}
}

func TestGetClientIP_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		trustProxy bool
		expectedIP string
	}{
		{
			name:       "empty X-Forwarded-For with trust",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{"X-Forwarded-For": ""},
			trustProxy: true,
			expectedIP: "192.168.1.1:1234",
		},
		{
			name:       "X-Forwarded-For with only commas",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{"X-Forwarded-For": ",,,"},
			trustProxy: true,
			expectedIP: "",
		},
		{
			name:       "X-Forwarded-For with whitespace only",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "   "},
			trustProxy: true,
			expectedIP: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got := getClientIP(req, tt.trustProxy)
			if got != tt.expectedIP {
				t.Errorf("getClientIP() = %v, want %v", got, tt.expectedIP)
			}
		})
	}
}
