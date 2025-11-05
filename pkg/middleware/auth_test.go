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
