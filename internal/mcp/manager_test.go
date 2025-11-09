package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManagerReusesLiveSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	home := t.TempDir()
	writeServerConfig(t, home, map[string]map[string]any{
		"test": {
			"enabled": true,
			"command": "ignored",
		},
	})

	mgr, err := NewManager(home)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	dialer := newTestDialer(t)
	mgr.connect = dialer.connect

	arguments := map[string]any{"ping": true}

	res1, err := mgr.Call(ctx, "test", "echo", arguments)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if res1.Content != "call-1" {
		t.Fatalf("Call() content = %q, want %q", res1.Content, "call-1")
	}

	res2, err := mgr.Call(ctx, "test", "echo", arguments)
	if err != nil {
		t.Fatalf("Call() second error = %v", err)
	}
	if res2.Content != "call-2" {
		t.Fatalf("Call() second content = %q, want %q", res2.Content, "call-2")
	}

	if got := dialer.connectionCount(); got != 1 {
		t.Fatalf("expected single session, got %d connections", got)
	}
}

func TestManagerReconnectsClosedSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	home := t.TempDir()
	writeServerConfig(t, home, map[string]map[string]any{
		"test": {
			"enabled": true,
			"command": "ignored",
		},
	})

	mgr, err := NewManager(home)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	dialer := newTestDialer(t)
	mgr.connect = dialer.connect

	if _, err := mgr.Call(ctx, "test", "echo", nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	handle := dialer.firstHandle()
	if handle == nil {
		t.Fatalf("expected first session handle")
	}
	if err := dialer.closeFirstServer(); err != nil {
		t.Fatalf("closeFirstServer() error = %v", err)
	}

	waitFor(t, time.Second, func() bool { return !handle.alive() })

	res, err := mgr.Call(ctx, "test", "echo", nil)
	if err != nil {
		t.Fatalf("Call() after close error = %v", err)
	}
	if res.Content != "call-2" {
		t.Fatalf("Call() after close content = %q, want %q", res.Content, "call-2")
	}

	if got := dialer.connectionCount(); got != 2 {
		t.Fatalf("expected reconnection, got %d connections", got)
	}
}

func TestManagerCloseShutsDownSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	home := t.TempDir()
	writeServerConfig(t, home, map[string]map[string]any{
		"test": {
			"enabled": true,
			"command": "ignored",
		},
	})

	mgr, err := NewManager(home)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	dialer := newTestDialer(t)
	mgr.connect = dialer.connect

	if _, err := mgr.Call(ctx, "test", "echo", nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	handle := dialer.firstHandle()
	if handle == nil {
		t.Fatalf("expected first session handle")
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	waitFor(t, time.Second, func() bool { return !handle.alive() })
}

func TestDefaultSessionDialerConnectsSSEServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	server := sdk.NewServer(&sdk.Implementation{Name: "remote", Version: "0.0.1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "ping",
		Description: "Simple ping",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{Text: "pong"},
			},
		}, nil
	})

	handler := sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return server }, nil)
	var (
		headerMu sync.Mutex
		headers  []http.Header
	)
	capturingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerMu.Lock()
		headers = append(headers, r.Header.Clone())
		headerMu.Unlock()
		handler.ServeHTTP(w, r)
	})

	httpServer := httptest.NewServer(capturingHandler)
	defer httpServer.Close()

	cfg := serverConfig{
		Name:      "remote-sse",
		Enabled:   true,
		URL:       httpServer.URL,
		Transport: transportSSE,
		Env: map[string]string{
			"Authorization": "Bearer token",
		},
	}

	holder, err := defaultSessionDialer(ctx, cfg)
	if err != nil {
		t.Fatalf("defaultSessionDialer() error = %v", err)
	}
	defer holder.Close()

	res, err := holder.session.CallTool(ctx, &sdk.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.Content == nil || len(res.Content) == 0 {
		t.Fatalf("CallTool() returned empty content")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; text != "pong" {
		t.Fatalf("CallTool() content = %q, want %q", text, "pong")
	}

	headerMu.Lock()
	defer headerMu.Unlock()
	foundAuth := false
	for _, hdr := range headers {
		if hdr.Get("Authorization") == "Bearer token" {
			foundAuth = true
			break
		}
	}
	if !foundAuth {
		t.Fatalf("expected Authorization header to be forwarded to remote SSE server")
	}
}

func TestDefaultSessionDialerConnectsHTTPServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	server := sdk.NewServer(&sdk.Implementation{Name: "remote-http", Version: "0.0.1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "double",
		Description: "Doubles a number",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var input map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &input)
		value := input["value"].(float64)
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{Text: fmt.Sprintf("%.0f", value*2)},
			},
		}, nil
	})

	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	var (
		headerMu sync.Mutex
		headers  []http.Header
	)
	capturingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerMu.Lock()
		headers = append(headers, r.Header.Clone())
		headerMu.Unlock()
		handler.ServeHTTP(w, r)
	})

	httpServer := httptest.NewServer(capturingHandler)
	defer httpServer.Close()

	cfg := serverConfig{
		Name:      "remote-http",
		Enabled:   true,
		URL:       httpServer.URL,
		Transport: transportHTTP,
		Env: map[string]string{
			"X-Test-Header": "1",
		},
	}

	holder, err := defaultSessionDialer(ctx, cfg)
	if err != nil {
		t.Fatalf("defaultSessionDialer() error = %v", err)
	}
	defer holder.Close()

	res, err := holder.session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "double",
		Arguments: map[string]any{"value": 21},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.Content == nil || len(res.Content) == 0 {
		t.Fatalf("CallTool() returned empty content")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; text != "42" {
		t.Fatalf("CallTool() content = %q, want %q", text, "42")
	}

	headerMu.Lock()
	defer headerMu.Unlock()
	foundHeader := false
	for _, hdr := range headers {
		if hdr.Get("X-Test-Header") == "1" {
			foundHeader = true
			break
		}
	}
	if !foundHeader {
		t.Fatalf("expected X-Test-Header to be forwarded to remote HTTP server")
	}
}

// --- test helpers ---

type testDialer struct {
	t *testing.T

	mu            sync.Mutex
	connections   int
	handles       []*sessionHolder
	serverHandles []*sdk.ServerSession
	callCount     int
}

func newTestDialer(t *testing.T) *testDialer {
	return &testDialer{t: t}
}

func (d *testDialer) connect(ctx context.Context, cfg serverConfig) (*sessionHolder, error) {
	ct, st := sdk.NewInMemoryTransports()

	server := sdk.NewServer(&sdk.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "Echo tool for testing",
		InputSchema: map[string]any{"type": "object"},
	}, d.toolHandler)

	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, err
	}

	holder := newSessionHolder(session, func() error {
		return serverSession.Close()
	})

	d.mu.Lock()
	defer d.mu.Unlock()
	d.connections++
	d.handles = append(d.handles, holder)
	d.serverHandles = append(d.serverHandles, serverSession)
	return holder, nil
}

func (d *testDialer) toolHandler(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	d.mu.Lock()
	d.callCount++
	count := d.callCount
	d.mu.Unlock()

	var input map[string]any
	_ = json.Unmarshal(req.Params.Arguments, &input)

	return &sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.TextContent{Text: fmt.Sprintf("call-%d", count)},
		},
	}, nil
}

func (d *testDialer) connectionCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connections
}

func (d *testDialer) firstHandle() *sessionHolder {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.handles) == 0 {
		return nil
	}
	return d.handles[0]
}

func (d *testDialer) closeFirstServer() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.serverHandles) == 0 {
		return fmt.Errorf("no server sessions")
	}
	return d.serverHandles[0].Close()
}

func writeServerConfig(t *testing.T, home string, servers map[string]map[string]any) {
	t.Helper()
	configDir := filepath.Join(home, ".humble-ai-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", configDir, err)
	}
	payload := map[string]any{
		"mcpServers": servers,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(configDir, "mcp-servers.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
