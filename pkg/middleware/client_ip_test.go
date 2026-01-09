package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
			name:       "X-Forwarded-For with only commas falls back to RemoteAddr",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{"X-Forwarded-For": ",,,"},
			trustProxy: true,
			expectedIP: "192.168.1.1:1234",
		},
		{
			name:       "X-Forwarded-For with whitespace only falls back to RemoteAddr",
			remoteAddr: "192.168.1.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "   "},
			trustProxy: true,
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
