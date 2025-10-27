package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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
	writeServerConfig(t, home, `{"name":"test","enabled":true,"command":"ignored"}`)

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
	writeServerConfig(t, home, `{"name":"test","enabled":true,"command":"ignored"}`)

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
	writeServerConfig(t, home, `{"name":"test","enabled":true,"command":"ignored"}`)

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

func writeServerConfig(t *testing.T, home, jsonConfig string) {
	t.Helper()
	dir := filepath.Join(home, ".humble-ai-cli", "mcp_servers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "test.json")
	if err := os.WriteFile(path, []byte(jsonConfig), 0o644); err != nil {
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
