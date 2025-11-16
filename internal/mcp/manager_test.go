package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestDefaultSessionDialerForwardsEnvToCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg := serverConfig{
		Name:    "command-env",
		Enabled: true,
		Command: os.Args[0],
		Args: []string{
			"-test.run=TestHelperCommandServer",
			"--",
		},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"TEST_TOKEN":             "secret-value",
		},
	}

	holder, err := defaultSessionDialer(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("defaultSessionDialer() error = %v", err)
	}
	defer holder.Close()

	res, err := holder.session.CallTool(ctx, &sdk.CallToolParams{Name: "env"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.Content == nil || len(res.Content) == 0 {
		t.Fatalf("CallTool() returned empty content")
	}
	got := res.Content[0].(*sdk.TextContent).Text
	if got != "secret-value" {
		t.Fatalf("CallTool() content = %q, want %q", got, "secret-value")
	}
}

func TestManagerReloadClosesSessionsWhenConfigChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	home := t.TempDir()
	writeServerConfig(t, home, map[string]map[string]any{
		"test": {
			"enabled": true,
			"command": "ignored",
			"env": map[string]any{
				"TEST_TOKEN": "one",
			},
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
		t.Fatalf("expected session handle")
	}

	writeServerConfig(t, home, map[string]map[string]any{
		"test": {
			"enabled": true,
			"command": "ignored",
			"env": map[string]any{
				"TEST_TOKEN": "two",
			},
		},
	})

	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	waitFor(t, time.Second, func() bool { return !handle.alive() })

	if _, err := mgr.Call(ctx, "test", "echo", nil); err != nil {
		t.Fatalf("Call() after reload error = %v", err)
	}
	if got := dialer.connectionCount(); got != 2 {
		t.Fatalf("expected new session after reload, got %d connections", got)
	}
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
		Type:      connectionTypeSSE,
		Env: map[string]string{
			"Authorization": "Bearer token",
		},
	}

	holder, err := defaultSessionDialer(ctx, cfg, nil)
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
		Type:      connectionTypeStreamableHTTP,
		Env: map[string]string{
			"X-Test-Header": "1",
		},
	}

	holder, err := defaultSessionDialer(ctx, cfg, nil)
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

func TestDefaultSessionDialerFallsBackFromSSEToHTTP(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	server := sdk.NewServer(&sdk.Implementation{Name: "fallback-http", Version: "0.0.1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "triple",
		Description: "Triples a value",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var input map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &input)
		value := input["value"].(float64)
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{Text: fmt.Sprintf("%.0f", value*3)},
			},
		}, nil
	})

	streamHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := strings.Join(r.Header.Values("Accept"), ",")
		if r.Method == http.MethodGet && !hasSessionHeader(r.Header) && strings.Contains(accept, "text/event-stream") {
			// Respond with a message event instead of the required endpoint event
			// to simulate HTTP transports that reject SSE connections.
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		streamHandler.ServeHTTP(w, r)
	})

	httpServer := httptest.NewServer(customHandler)
	defer httpServer.Close()

	cfg := serverConfig{
		Name:      "fallback-http",
		Enabled:   true,
		URL:       httpServer.URL,
		Type:      connectionTypeSSE,
	}

	holder, err := defaultSessionDialer(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("defaultSessionDialer() error = %v", err)
	}
	defer holder.Close()

	res, err := holder.session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "triple",
		Arguments: map[string]any{"value": 14},
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
}

func hasSessionHeader(header http.Header) bool {
	return strings.TrimSpace(header.Get("Mcp-Session-Id")) != ""
}

func TestServerConfigConnectionKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     serverConfig
		want    string
		wantErr string
	}{
		{
			name: "command defaults to stdio",
			cfg: serverConfig{
				Name:    "cmd",
				Command: "runner",
			},
			want: connectionTypeStdio,
		},
		{
			name: "command explicit stdio",
			cfg: serverConfig{
				Name:    "cmd-typed",
				Command: "runner",
				Type:    connectionTypeStdio,
			},
			want: connectionTypeStdio,
		},
		{
			name: "command rejects non-stdio type",
			cfg: serverConfig{
				Name:    "cmd-invalid",
				Command: "runner",
				Type:    connectionTypeSSE,
			},
			wantErr: "type must be stdio",
		},
		{
			name: "url requires type",
			cfg: serverConfig{
				Name: "url-missing-type",
				URL:  "https://example.com/mcp",
			},
			wantErr: "must define type",
		},
		{
			name: "url rejects stdio without command",
			cfg: serverConfig{
				Name: "url-stdio",
				URL:  "https://example.com/mcp",
				Type: connectionTypeStdio,
			},
			wantErr: "type stdio",
		},
		{
			name: "url accepts sse",
			cfg: serverConfig{
				Name: "url-sse",
				URL:  "https://example.com/mcp",
				Type: connectionTypeSSE,
			},
			want: connectionTypeSSE,
		},
		{
			name: "url accepts streamable http",
			cfg: serverConfig{
				Name: "url-http",
				URL:  "https://example.com/mcp",
				Type: connectionTypeStreamableHTTP,
			},
			want: connectionTypeStreamableHTTP,
		},
		{
			name: "command and url allow stdio",
			cfg: serverConfig{
				Name:    "both-stdio",
				Command: "runner",
				URL:     "https://example.com/mcp",
				Type:    connectionTypeStdio,
			},
			want: connectionTypeStdio,
		},
		{
			name: "command and url accept sse",
			cfg: serverConfig{
				Name:    "both-sse",
				Command: "runner",
				URL:     "https://example.com/mcp",
				Type:    connectionTypeSSE,
			},
			want: connectionTypeSSE,
		},
		{
			name: "unknown type rejected",
			cfg: serverConfig{
				Name:    "unknown",
				Command: "runner",
				URL:     "https://example.com/mcp",
				Type:    "invalid",
			},
			wantErr: "unsupported type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.connectionKind()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("connectionKind() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("connectionKind() unexpected error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("connectionKind() = %q, want %q", got, tt.want)
			}
		})
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

func (d *testDialer) connect(ctx context.Context, cfg serverConfig, _ DebugLogger) (*sessionHolder, error) {
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

func TestHelperCommandServer(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	server := sdk.NewServer(&sdk.Implementation{Name: "helper", Version: "0.0.1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "env",
		Description: "returns TEST_TOKEN env",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		value := os.Getenv("TEST_TOKEN")
		return &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{Text: value},
			},
		}, nil
	})

	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
