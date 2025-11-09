package middleware

import "net/http"

// Middleware is a function that wraps an http.Handler
type Middleware func(http.Handler) http.Handler

// Chain is a builder for composing HTTP middleware
type Chain struct {
	middlewares []Middleware
}

// NewChain creates a new middleware chain
func NewChain() *Chain {
	return &Chain{
		middlewares: make([]Middleware, 0),
	}
}

// Use adds a middleware to the chain
func (c *Chain) Use(middleware Middleware) *Chain {
	c.middlewares = append(c.middlewares, middleware)
	return c
}

// UseIf conditionally adds a middleware to the chain
func (c *Chain) UseIf(condition bool, middleware Middleware) *Chain {
	if condition {
		c.middlewares = append(c.middlewares, middleware)
	}
	return c
}

// Then applies all middlewares to the final handler and returns the wrapped handler
// Middlewares are applied in reverse order so they execute in the order they were added
func (c *Chain) Then(handler http.Handler) http.Handler {
	// Apply middlewares in reverse order so they execute in the order added
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		handler = c.middlewares[i](handler)
	}
	return handler
}
