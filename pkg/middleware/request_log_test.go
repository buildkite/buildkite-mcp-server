package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestRequestLog(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		userAgent      string
		clientIP       string
		handlerStatus  int
		expectedStatus int
	}{
		{
			name:           "successful GET request",
			method:         http.MethodGet,
			path:           "/health",
			userAgent:      "test-agent/1.0",
			clientIP:       "192.168.1.1",
			handlerStatus:  http.StatusOK,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "POST request with 201 status",
			method:         http.MethodPost,
			path:           "/api/resource",
			userAgent:      "curl/7.68.0",
			clientIP:       "203.0.113.1",
			handlerStatus:  http.StatusCreated,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "request with 404 status",
			method:         http.MethodGet,
			path:           "/not-found",
			userAgent:      "Mozilla/5.0",
			clientIP:       "10.0.0.1",
			handlerStatus:  http.StatusNotFound,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "request with 500 status",
			method:         http.MethodPost,
			path:           "/api/error",
			userAgent:      "test-client",
			clientIP:       "172.16.0.1",
			handlerStatus:  http.StatusInternalServerError,
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "request with empty user agent",
			method:         http.MethodGet,
			path:           "/test",
			userAgent:      "",
			clientIP:       "192.168.1.1",
			handlerStatus:  http.StatusOK,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "request with empty client IP",
			method:         http.MethodGet,
			path:           "/test",
			userAgent:      "test-agent",
			clientIP:       "",
			handlerStatus:  http.StatusOK,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture log output
			var logBuf bytes.Buffer
			originalLogger := log.Logger
			log.Logger = zerolog.New(&logBuf).With().Timestamp().Logger()
			defer func() {
				log.Logger = originalLogger
			}()

			// Create test handler
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Simulate some processing time
				time.Sleep(1 * time.Millisecond)
				w.WriteHeader(tt.handlerStatus)
			})

			// Apply RequestLog middleware
			middleware := RequestLog()
			handler := middleware(testHandler)

			// Create request with client IP in context
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("User-Agent", tt.userAgent)
			ctx := context.WithValue(req.Context(), clientIPKey, tt.clientIP)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify status code
			if rr.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			// Parse log output
			var logEntry map[string]any
			logOutput := logBuf.String()
			if logOutput == "" {
				t.Fatal("No log output captured")
			}

			if err := json.Unmarshal([]byte(logOutput), &logEntry); err != nil {
				t.Fatalf("Failed to parse log output: %v\nLog output: %s", err, logOutput)
			}

			// Verify log fields
			if logEntry["level"] != "info" {
				t.Errorf("Expected log level 'info', got %v", logEntry["level"])
			}

			if logEntry["message"] != "HTTP request" {
				t.Errorf("Expected message 'HTTP request', got %v", logEntry["message"])
			}

			if logEntry["method"] != tt.method {
				t.Errorf("Expected method %s, got %v", tt.method, logEntry["method"])
			}

			if logEntry["path"] != tt.path {
				t.Errorf("Expected path %s, got %v", tt.path, logEntry["path"])
			}

			if statusFloat, ok := logEntry["status"].(float64); !ok || int(statusFloat) != tt.expectedStatus {
				t.Errorf("Expected status %d, got %v", tt.expectedStatus, logEntry["status"])
			}

			if logEntry["user_agent"] != tt.userAgent {
				t.Errorf("Expected user_agent %s, got %v", tt.userAgent, logEntry["user_agent"])
			}

			if logEntry["client_ip"] != tt.clientIP {
				t.Errorf("Expected client_ip %s, got %v", tt.clientIP, logEntry["client_ip"])
			}

			// Verify duration_ms is present and reasonable
			if durationFloat, ok := logEntry["duration_ms"].(float64); !ok || durationFloat < 0 {
				t.Errorf("Expected valid duration_ms, got %v", logEntry["duration_ms"])
			}
		})
	}
}

func TestRequestLog_WithoutStatusCall(t *testing.T) {
	// Capture log output
	var logBuf bytes.Buffer
	originalLogger := log.Logger
	log.Logger = zerolog.New(&logBuf).With().Timestamp().Logger()
	defer func() {
		log.Logger = originalLogger
	}()

	// Create handler that writes without calling WriteHeader
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write directly without calling WriteHeader
		// This should implicitly call WriteHeader(200)
		_, _ = w.Write([]byte("OK"))
	})

	middleware := RequestLog()
	handler := middleware(testHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), clientIPKey, "192.168.1.1")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Parse log output
	var logEntry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	// Should log status 200 (default when WriteHeader not called)
	if statusFloat, ok := logEntry["status"].(float64); !ok || int(statusFloat) != http.StatusOK {
		t.Errorf("Expected status 200, got %v", logEntry["status"])
	}
}

func TestRequestLog_MultipleWriteHeaders(t *testing.T) {
	// Capture log output
	var logBuf bytes.Buffer
	originalLogger := log.Logger
	log.Logger = zerolog.New(&logBuf).With().Timestamp().Logger()
	defer func() {
		log.Logger = originalLogger
	}()

	// Create handler that tries to call WriteHeader multiple times
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusInternalServerError) // Should be ignored
		_, _ = w.Write([]byte("test"))
	})

	middleware := RequestLog()
	handler := middleware(testHandler)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := context.WithValue(req.Context(), clientIPKey, "192.168.1.1")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Parse log output
	var logEntry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	// Should log the first status code (201), not the second
	if statusFloat, ok := logEntry["status"].(float64); !ok || int(statusFloat) != http.StatusCreated {
		t.Errorf("Expected status 201, got %v", logEntry["status"])
	}
}

func TestResponseWriter_StatusCode(t *testing.T) {
	tests := []struct {
		name           string
		writeHeader    bool
		statusCode     int
		expectedStatus int
	}{
		{
			name:           "explicit 200",
			writeHeader:    true,
			statusCode:     http.StatusOK,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "explicit 404",
			writeHeader:    true,
			statusCode:     http.StatusNotFound,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "implicit 200",
			writeHeader:    false,
			statusCode:     0,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "explicit 500",
			writeHeader:    true,
			statusCode:     http.StatusInternalServerError,
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			wrapped := newResponseWriter(rr)

			if tt.writeHeader {
				wrapped.WriteHeader(tt.statusCode)
			}

			// Write should trigger implicit WriteHeader(200) if not already called
			_, _ = wrapped.Write([]byte("test"))

			if wrapped.statusCode != tt.expectedStatus {
				t.Errorf("Expected status code %d, got %d", tt.expectedStatus, wrapped.statusCode)
			}
		})
	}
}

func TestRequestLog_Duration(t *testing.T) {
	// Capture log output
	var logBuf bytes.Buffer
	originalLogger := log.Logger
	log.Logger = zerolog.New(&logBuf).With().Timestamp().Logger()
	defer func() {
		log.Logger = originalLogger
	}()

	sleepDuration := 10 * time.Millisecond

	// Create handler that takes some time
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(sleepDuration)
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLog()
	handler := middleware(testHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), clientIPKey, "192.168.1.1")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Parse log output
	var logEntry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	// Verify duration is at least the sleep duration
	durationFloat, ok := logEntry["duration_ms"].(float64)
	if !ok {
		t.Fatal("duration_ms not found or not a number")
	}

	observedDuration := time.Duration(durationFloat) * time.Millisecond
	if observedDuration < sleepDuration {
		t.Errorf("Expected duration >= %v, got %v", sleepDuration, observedDuration)
	}
}
