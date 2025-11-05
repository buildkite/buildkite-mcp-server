package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewChain(t *testing.T) {
	chain := NewChain()
	if chain == nil {
		t.Fatal("NewChain() returned nil")
	}
	if chain.middlewares == nil {
		t.Error("NewChain() should initialize middlewares slice")
	}
	if len(chain.middlewares) != 0 {
		t.Errorf("NewChain() should have empty middlewares, got %d", len(chain.middlewares))
	}
}

func TestChain_Use(t *testing.T) {
	chain := NewChain()

	// Create a test middleware
	testMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	// Add middleware
	result := chain.Use(testMiddleware)

	// Should return same chain for fluent API
	if result != chain {
		t.Error("Use() should return the same chain for fluent API")
	}

	// Should have added the middleware
	if len(chain.middlewares) != 1 {
		t.Errorf("Expected 1 middleware, got %d", len(chain.middlewares))
	}
}

func TestChain_UseIf_True(t *testing.T) {
	chain := NewChain()

	testMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	// Add middleware with true condition
	result := chain.UseIf(true, testMiddleware)

	// Should return same chain
	if result != chain {
		t.Error("UseIf() should return the same chain for fluent API")
	}

	// Should have added the middleware
	if len(chain.middlewares) != 1 {
		t.Errorf("Expected 1 middleware when condition is true, got %d", len(chain.middlewares))
	}
}

func TestChain_UseIf_False(t *testing.T) {
	chain := NewChain()

	testMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	// Add middleware with false condition
	result := chain.UseIf(false, testMiddleware)

	// Should return same chain
	if result != chain {
		t.Error("UseIf() should return the same chain for fluent API")
	}

	// Should NOT have added the middleware
	if len(chain.middlewares) != 0 {
		t.Errorf("Expected 0 middleware when condition is false, got %d", len(chain.middlewares))
	}
}

func TestChain_Then_ExecutionOrder(t *testing.T) {
	// Track execution order
	var executionOrder []string

	// Create middlewares that track their execution
	middleware1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "middleware1-before")
			next.ServeHTTP(w, r)
			executionOrder = append(executionOrder, "middleware1-after")
		})
	}

	middleware2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "middleware2-before")
			next.ServeHTTP(w, r)
			executionOrder = append(executionOrder, "middleware2-after")
		})
	}

	middleware3 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "middleware3-before")
			next.ServeHTTP(w, r)
			executionOrder = append(executionOrder, "middleware3-after")
		})
	}

	// Final handler
	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executionOrder = append(executionOrder, "handler")
		w.WriteHeader(http.StatusOK)
	})

	// Build chain
	chain := NewChain().
		Use(middleware1).
		Use(middleware2).
		Use(middleware3)

	handler := chain.Then(finalHandler)

	// Execute the chain
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Verify execution order
	expected := []string{
		"middleware1-before",
		"middleware2-before",
		"middleware3-before",
		"handler",
		"middleware3-after",
		"middleware2-after",
		"middleware1-after",
	}

	if len(executionOrder) != len(expected) {
		t.Fatalf("Expected %d execution steps, got %d", len(expected), len(executionOrder))
	}

	for i, step := range expected {
		if executionOrder[i] != step {
			t.Errorf("Step %d: expected %q, got %q", i, step, executionOrder[i])
		}
	}
}

func TestChain_Then_EmptyChain(t *testing.T) {
	// Handler that tracks if it was called
	var called bool
	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Create empty chain
	chain := NewChain()
	handler := chain.Then(finalHandler)

	// Execute
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should have called the handler
	if !called {
		t.Error("Empty chain should still call the final handler")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

func TestChain_FluentAPI(t *testing.T) {
	// Test that we can chain multiple calls fluently
	middleware1 := func(next http.Handler) http.Handler {
		return next
	}
	middleware2 := func(next http.Handler) http.Handler {
		return next
	}

	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// This should compile and work fluently
	handler := NewChain().
		Use(middleware1).
		UseIf(true, middleware2).
		Use(middleware1).
		UseIf(false, middleware2).
		Then(finalHandler)

	// Execute to ensure it works
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

func TestChain_ModifyRequest(t *testing.T) {
	// Middleware that adds a header
	addHeader := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Test", "modified")
			next.ServeHTTP(w, r)
		})
	}

	// Handler that checks the header
	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "modified" {
			t.Error("Middleware should have modified the request")
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := NewChain().Use(addHeader).Then(finalHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
}

func TestChain_ModifyResponse(t *testing.T) {
	// Middleware that adds a response header
	addResponseHeader := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Middleware", "present")
			next.ServeHTTP(w, r)
		})
	}

	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := NewChain().Use(addResponseHeader).Then(finalHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Check that middleware added the header
	if rr.Header().Get("X-Middleware") != "present" {
		t.Error("Middleware should have added response header")
	}
}

func TestChain_ShortCircuit(t *testing.T) {
	// Middleware that short-circuits (doesn't call next)
	shortCircuit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
			// Don't call next.ServeHTTP
		})
	}

	// This should never be called
	var handlerCalled bool
	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := NewChain().Use(shortCircuit).Then(finalHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Handler should not have been called
	if handlerCalled {
		t.Error("Final handler should not be called when middleware short-circuits")
	}

	// Should have unauthorized status
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}

	// Should have the body from middleware
	if rr.Body.String() != "unauthorized" {
		t.Errorf("Expected 'unauthorized', got %q", rr.Body.String())
	}
}

func TestChain_MultipleUseIf(t *testing.T) {
	var executionOrder []string

	middleware1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "m1")
			next.ServeHTTP(w, r)
		})
	}

	middleware2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "m2")
			next.ServeHTTP(w, r)
		})
	}

	middleware3 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "m3")
			next.ServeHTTP(w, r)
		})
	}

	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executionOrder = append(executionOrder, "handler")
		w.WriteHeader(http.StatusOK)
	})

	// Build chain with conditional middlewares
	handler := NewChain().
		UseIf(true, middleware1).  // Should be added
		UseIf(false, middleware2). // Should NOT be added
		UseIf(true, middleware3).  // Should be added
		Then(finalHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should have: m1, m3, handler (m2 was skipped)
	expected := []string{"m1", "m3", "handler"}
	if len(executionOrder) != len(expected) {
		t.Fatalf("Expected %d steps, got %d: %v", len(expected), len(executionOrder), executionOrder)
	}

	for i, step := range expected {
		if executionOrder[i] != step {
			t.Errorf("Step %d: expected %q, got %q", i, step, executionOrder[i])
		}
	}
}
