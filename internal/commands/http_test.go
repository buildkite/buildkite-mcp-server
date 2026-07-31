package commands

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/stretchr/testify/require"
)

// TestWriteTimeoutOutlastsSlowestTool pins the relationship the streamable HTTP
// transport depends on. Nothing else connects these two numbers, and getting it
// wrong is silent: the handler completes normally while the client sees only a
// closed connection.
func TestWriteTimeoutOutlastsSlowestTool(t *testing.T) {
	budget := buildkite.MaxWaitForBuildBudget()

	require.Greater(t, httpWriteTimeout, budget,
		"httpWriteTimeout (%s) must outlast the slowest tool call (%s) or its response is never delivered",
		httpWriteTimeout, budget)

	// Keep real headroom rather than squeaking past, so a modest bump to the
	// poll window does not quietly land on the boundary.
	require.GreaterOrEqual(t, httpWriteTimeout-budget, 20*time.Second,
		"leave at least 20s of headroom between httpWriteTimeout (%s) and the tool budget (%s)",
		httpWriteTimeout, budget)
}

// TestWriteTimeoutIsAppliedToServer guards the wiring, not just the constant.
func TestWriteTimeoutIsAppliedToServer(t *testing.T) {
	srv := newServerWithTimeouts(http.NewServeMux(), httpWriteTimeout)
	require.Equal(t, httpWriteTimeout, srv.WriteTimeout)
}

// TestHandlerOutlivingWriteTimeoutIsCutOff demonstrates the failure mode the
// constant exists to prevent: a handler slower than WriteTimeout delivers
// nothing at all, so the client cannot distinguish it from a network fault.
// Scaled down so the test stays fast.
func TestHandlerOutlivingWriteTimeoutIsCutOff(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{"finished":false}`))
	})

	srv := newServerWithTimeouts(mux, 100*time.Millisecond)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = srv.Close() }()

	go func() { _ = srv.Serve(listener) }()

	resp, err := http.Get("http://" + listener.Addr().String() + "/slow") //nolint:noctx // deliberate: exercising the server's own deadline
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}

	require.Error(t, err, "a handler outliving WriteTimeout must not deliver a response")
}
