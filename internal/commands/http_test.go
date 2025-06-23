package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/go-buildkite/v4"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// MockHTTPServer interface that both SSE and Streamable servers implement
type MockHTTPServer interface {
	Start(addr string) error
}

// MockSSEServer simulates the SSE server for testing
type MockSSEServer struct {
	StartFunc  func(addr string) error
	StartCalls []string // Track calls made to Start
}

func (m *MockSSEServer) Start(addr string) error {
	m.StartCalls = append(m.StartCalls, addr)
	if m.StartFunc != nil {
		return m.StartFunc(addr)
	}
	return nil
}

// MockStreamableServer simulates the streamable HTTP server for testing
type MockStreamableServer struct {
	StartFunc  func(addr string) error
	StartCalls []string // Track calls made to Start
}

func (m *MockStreamableServer) Start(addr string) error {
	m.StartCalls = append(m.StartCalls, addr)
	if m.StartFunc != nil {
		return m.StartFunc(addr)
	}
	return nil
}

// ServerFactory interface for creating servers (used for dependency injection in tests)
type ServerFactory interface {
	NewSSEServer(server *mcpserver.MCPServer) MockHTTPServer
	NewStreamableHTTPServer(server *mcpserver.MCPServer) MockHTTPServer
}

// MockServerFactory implements ServerFactory for testing
type MockServerFactory struct {
	SSEServer        *MockSSEServer
	StreamableServer *MockStreamableServer
}

func (f *MockServerFactory) NewSSEServer(server *mcpserver.MCPServer) MockHTTPServer {
	return f.SSEServer
}

func (f *MockServerFactory) NewStreamableHTTPServer(server *mcpserver.MCPServer) MockHTTPServer {
	return f.StreamableServer
}

// TestableHTTPCmd extends HTTPCmd to allow dependency injection for testing
type TestableHTTPCmd struct {
	HTTPCmd
	ServerFactory ServerFactory
}

func (c *TestableHTTPCmd) Run(ctx context.Context, globals *Globals) error {
	mcpServer := server.NewMCPServer(globals.Version, globals.Client, globals.BuildkiteLogsClient)

	if c.UseSSE {
		httpServer := c.ServerFactory.NewSSEServer(mcpServer)
		return httpServer.Start(c.Listen)
	} else {
		httpServer := c.ServerFactory.NewStreamableHTTPServer(mcpServer)
		return httpServer.Start(c.Listen)
	}
}

func TestHTTPCmd_Run_SSEServer(t *testing.T) {
	tests := []struct {
		name          string
		listen        string
		sse           bool
		startError    error
		expectedError string
		expectedCalls []string
	}{
		{
			name:          "SSE server starts successfully",
			listen:        "localhost:3000",
			sse:           true,
			startError:    nil,
			expectedError: "",
			expectedCalls: []string{"localhost:3000"},
		},
		{
			name:          "SSE server with custom port",
			listen:        "localhost:8080",
			sse:           true,
			startError:    nil,
			expectedError: "",
			expectedCalls: []string{"localhost:8080"},
		},
		{
			name:          "SSE server start fails",
			listen:        "localhost:3000",
			sse:           true,
			startError:    errors.New("failed to bind to port"),
			expectedError: "failed to bind to port",
			expectedCalls: []string{"localhost:3000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)

			// Create mock servers
			mockSSEServer := &MockSSEServer{
				StartFunc: func(addr string) error {
					return tt.startError
				},
				StartCalls: []string{},
			}
			mockStreamableServer := &MockStreamableServer{
				StartCalls: []string{},
			}

			// Create mock factory
			factory := &MockServerFactory{
				SSEServer:        mockSSEServer,
				StreamableServer: mockStreamableServer,
			}

			// Create testable command
			cmd := &TestableHTTPCmd{
				HTTPCmd: HTTPCmd{
					Listen: tt.listen,
					UseSSE: tt.sse,
				},
				ServerFactory: factory,
			}

			// Create test context and globals
			ctx := context.Background()
			globals := createTestGlobals()

			// Run the command
			err := cmd.Run(ctx, globals)

			// Verify error handling
			if tt.expectedError != "" {
				assert.Error(err)
				assert.Contains(err.Error(), tt.expectedError)
			} else {
				assert.NoError(err)
			}

			// Verify the correct server type was used and called with correct parameters
			assert.Equal(tt.expectedCalls, mockSSEServer.StartCalls)
			assert.Empty(mockStreamableServer.StartCalls) // Streamable server should not be called
		})
	}
}

func TestHTTPCmd_Run_StreamableServer(t *testing.T) {
	tests := []struct {
		name          string
		listen        string
		sse           bool
		startError    error
		expectedError string
		expectedCalls []string
	}{
		{
			name:          "Streamable server starts successfully",
			listen:        "localhost:3000",
			sse:           false,
			startError:    nil,
			expectedError: "",
			expectedCalls: []string{"localhost:3000"},
		},
		{
			name:          "Streamable server with custom port",
			listen:        "0.0.0.0:9000",
			sse:           false,
			startError:    nil,
			expectedError: "",
			expectedCalls: []string{"0.0.0.0:9000"},
		},
		{
			name:          "Streamable server start fails",
			listen:        "localhost:3000",
			sse:           false,
			startError:    errors.New("permission denied"),
			expectedError: "permission denied",
			expectedCalls: []string{"localhost:3000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)

			// Create mock servers
			mockSSEServer := &MockSSEServer{
				StartCalls: []string{},
			}
			mockStreamableServer := &MockStreamableServer{
				StartFunc: func(addr string) error {
					return tt.startError
				},
				StartCalls: []string{},
			}

			// Create mock factory
			factory := &MockServerFactory{
				SSEServer:        mockSSEServer,
				StreamableServer: mockStreamableServer,
			}

			// Create testable command
			cmd := &TestableHTTPCmd{
				HTTPCmd: HTTPCmd{
					Listen: tt.listen,
					UseSSE: tt.sse,
				},
				ServerFactory: factory,
			}

			// Create test context and globals
			ctx := context.Background()
			globals := createTestGlobals()

			// Run the command
			err := cmd.Run(ctx, globals)

			// Verify error handling
			if tt.expectedError != "" {
				assert.Error(err)
				assert.Contains(err.Error(), tt.expectedError)
			} else {
				assert.NoError(err)
			}

			// Verify the correct server type was used and called with correct parameters
			assert.Equal(tt.expectedCalls, mockStreamableServer.StartCalls)
			assert.Empty(mockSSEServer.StartCalls) // SSE server should not be called
		})
	}
}

func TestHTTPCmd_ServerTypeSelection(t *testing.T) {
	tests := []struct {
		name                    string
		sse                     bool
		expectedSSECalls        int
		expectedStreamableCalls int
	}{
		{
			name:                    "Default behavior uses HTTP server",
			sse:                     false,
			expectedSSECalls:        0,
			expectedStreamableCalls: 1,
		},
		{
			name:                    "--use-sse flag uses SSE server",
			sse:                     true,
			expectedSSECalls:        1,
			expectedStreamableCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)

			// Create mock servers
			mockSSEServer := &MockSSEServer{StartCalls: []string{}}
			mockStreamableServer := &MockStreamableServer{StartCalls: []string{}}

			// Create mock factory
			factory := &MockServerFactory{
				SSEServer:        mockSSEServer,
				StreamableServer: mockStreamableServer,
			}

			// Create testable command
			cmd := &TestableHTTPCmd{
				HTTPCmd: HTTPCmd{
					Listen: "localhost:3000",
					UseSSE: tt.sse,
				},
				ServerFactory: factory,
			}

			// Create test context and globals
			ctx := context.Background()
			globals := createTestGlobals()

			// Run the command
			err := cmd.Run(ctx, globals)
			assert.NoError(err)

			// Verify the correct server type was selected
			assert.Len(mockSSEServer.StartCalls, tt.expectedSSECalls)
			assert.Len(mockStreamableServer.StartCalls, tt.expectedStreamableCalls)
		})
	}
}

// Helper function to create test globals
func createTestGlobals() *Globals {
	logger := zerolog.Nop()

	// Create a minimal buildkite client for testing
	client, _ := buildkite.NewOpts(
		buildkite.WithTokenAuth("test-token"),
	)

	return &Globals{
		Version: "test-version",
		Client:  client,
		Logger:  logger,
	}
}
