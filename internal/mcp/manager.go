package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"gamzabox.com/humble-ai-cli/internal/llm"
)

// Server describes an enabled MCP server.
type Server struct {
	Name        string
	Description string
}

// Function describes a tool exposed by an MCP server.
type Function struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type serverConfig struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Enabled     bool     `json:"enabled"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
}

type sessionDialer func(context.Context, serverConfig) (*sessionHolder, error)

// Manager loads server configurations and executes MCP tool calls.
type Manager struct {
	home    string
	mu      sync.Mutex
	servers map[string]serverConfig
	sessions map[string]*sessionHolder
	connect  sessionDialer
}

// NewManager creates a Manager rooted at the provided home directory.
func NewManager(home string) (*Manager, error) {
	servers, err := loadServerConfigs(home)
	if err != nil {
		return nil, err
	}
	return &Manager{
		home:     home,
		servers:  servers,
		sessions: make(map[string]*sessionHolder),
		connect:  defaultSessionDialer,
	}, nil
}

// EnabledServers returns the metadata for enabled servers.
func (m *Manager) EnabledServers() []Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	servers := make([]Server, 0, len(m.servers))
	for _, cfg := range m.servers {
		if !cfg.Enabled {
			continue
		}
		servers = append(servers, Server{
			Name:        cfg.Name,
			Description: cfg.Description,
		})
	}
	return servers
}

// Describe returns metadata for a given server.
func (m *Manager) Describe(name string) (Server, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.servers[name]
	if !ok || !cfg.Enabled {
		return Server{}, false
	}
	return Server{
		Name:        cfg.Name,
		Description: cfg.Description,
	}, true
}

// Call executes the given tool on the specified server.
func (m *Manager) Call(ctx context.Context, server, method string, arguments map[string]any) (llm.ToolResult, error) {
	params := &sdk.CallToolParams{
		Name:      method,
		Arguments: arguments,
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		holder, err := m.ensureSession(ctx, server)
		if err != nil {
			return llm.ToolResult{}, err
		}

		result, err := holder.session.CallTool(ctx, params)
		if err == nil {
			return convertResult(result)
		}

		lastErr = err
		if !m.handleSessionError(server, holder, err) {
			return llm.ToolResult{}, fmt.Errorf("call tool %q on server %q: %w", method, server, err)
		}
	}

	return llm.ToolResult{}, fmt.Errorf("call tool %q on server %q: %w", method, server, lastErr)
}

// Tools lists functions provided by the specified server.
func (m *Manager) Tools(ctx context.Context, server string) ([]Function, error) {
	var (
		lastErr error
	)
	for attempt := 0; attempt < 2; attempt++ {
		holder, err := m.ensureSession(ctx, server)
		if err != nil {
			return nil, err
		}

		tools, err := m.fetchTools(ctx, holder.session)
		if err == nil {
			return tools, nil
		}

		lastErr = err
		if !m.handleSessionError(server, holder, err) {
			return nil, fmt.Errorf("list tools on server %q: %w", server, err)
		}
	}
	return nil, fmt.Errorf("list tools on server %q: %w", server, lastErr)
}

// Close shuts down all cached MCP sessions.
func (m *Manager) Close() error {
	m.mu.Lock()
	sessions := make([]*sessionHolder, 0, len(m.sessions))
	for name, holder := range m.sessions {
		if holder != nil {
			sessions = append(sessions, holder)
		}
		delete(m.sessions, name)
	}
	m.mu.Unlock()

	var errs []error
	for _, holder := range sessions {
		if err := holder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (m *Manager) ensureSession(ctx context.Context, name string) (*sessionHolder, error) {
	m.mu.Lock()
	cfg, ok := m.servers[name]
	if !ok || !cfg.Enabled {
		m.mu.Unlock()
		return nil, fmt.Errorf("unknown MCP server %q", name)
	}
	if strings.TrimSpace(cfg.Command) == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("mcp server %q has no command configured", name)
	}

	holder := m.sessions[name]
	var stale *sessionHolder
	if holder != nil && holder.alive() {
		m.mu.Unlock()
		return holder, nil
	}
	if holder != nil {
		delete(m.sessions, name)
		stale = holder
	}
	dial := m.connect
	m.mu.Unlock()

	if stale != nil {
		_ = stale.Close()
	}

	newHolder, err := dial(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect MCP server %q: %w", name, err)
	}

	m.mu.Lock()
	if existing := m.sessions[name]; existing != nil && existing.alive() {
		m.mu.Unlock()
		_ = newHolder.Close()
		return existing, nil
	}
	m.sessions[name] = newHolder
	m.mu.Unlock()
	return newHolder, nil
}

func (m *Manager) handleSessionError(server string, holder *sessionHolder, err error) bool {
	if !errors.Is(err, sdk.ErrConnectionClosed) {
		return false
	}
	_ = holder.Close()
	m.removeSession(server, holder)
	return true
}

func (m *Manager) removeSession(server string, holder *sessionHolder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.sessions[server]; ok && current == holder {
		delete(m.sessions, server)
	}
}

func (m *Manager) fetchTools(ctx context.Context, session *sdk.ClientSession) ([]Function, error) {
	params := &sdk.ListToolsParams{}
	var out []Function
	for {
		res, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, tool := range res.Tools {
			if tool == nil {
				continue
			}
			out = append(out, Function{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  normalizeSchema(tool.InputSchema),
			})
		}
		if res.NextCursor == "" {
			break
		}
		params = &sdk.ListToolsParams{Cursor: res.NextCursor}
	}
	return out, nil
}

func defaultSessionDialer(ctx context.Context, cfg serverConfig) (*sessionHolder, error) {
	client := sdk.NewClient(&sdk.Implementation{
		Name:    "humble-ai-cli",
		Version: "0.1.0",
	}, nil)

	cmd := exec.Command(cfg.Command, cfg.Args...)
	transport := &sdk.CommandTransport{Command: cmd}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		return nil, err
	}
	return newSessionHolder(session, nil), nil
}

type sessionHolder struct {
	session    *sdk.ClientSession
	extraClose func() error

	once sync.Once
	done chan struct{}

	mu  sync.Mutex
	err error
}

func newSessionHolder(session *sdk.ClientSession, extraClose func() error) *sessionHolder {
	holder := &sessionHolder{
		session:    session,
		extraClose: extraClose,
		done:       make(chan struct{}),
	}

	go func() {
		err := session.Wait()
		holder.recordClose(err)
	}()

	return holder
}

func (h *sessionHolder) Close() error {
	err := h.session.Close()
	h.recordClose(err)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *sessionHolder) alive() bool {
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

func (h *sessionHolder) recordClose(sessionErr error) {
	var extraErr error
	h.once.Do(func() {
		if h.extraClose != nil {
			extraErr = h.extraClose()
		}
		close(h.done)
	})
	combined := errors.Join(sessionErr, extraErr)
	if combined != nil {
		h.mu.Lock()
		if h.err == nil {
			h.err = combined
		}
		h.mu.Unlock()
	}
}

func normalizeSchema(input any) map[string]any {
	if input == nil {
		return defaultSchema()
	}

	switch v := input.(type) {
	case map[string]any:
		return cloneMap(v)
	case json.RawMessage:
		if len(v) == 0 {
			return defaultSchema()
		}
		var out map[string]any
		if err := json.Unmarshal(v, &out); err == nil {
			return out
		}
	default:
		data, err := json.Marshal(v)
		if err == nil {
			var out map[string]any
			if err := json.Unmarshal(data, &out); err == nil {
				return out
			}
		}
	}

	return defaultSchema()
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func defaultSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": true,
	}
}

func loadServerConfigs(home string) (map[string]serverConfig, error) {
	dir := filepath.Join(home, ".humble-ai-cli", "mcp_servers")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]serverConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list MCP servers: %w", err)
	}

	servers := make(map[string]serverConfig)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read MCP server config %q: %w", entry.Name(), err)
		}

		var cfg serverConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("parse MCP server config %q: %w", entry.Name(), err)
		}

		if strings.TrimSpace(cfg.Name) == "" {
			cfg.Name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		if _, exists := servers[cfg.Name]; exists {
			return nil, fmt.Errorf("duplicate MCP server name %q", cfg.Name)
		}
		servers[cfg.Name] = cfg
	}
	return servers, nil
}

func convertResult(res *sdk.CallToolResult) (llm.ToolResult, error) {
	if res == nil {
		return llm.ToolResult{}, errors.New("nil result returned from MCP server")
	}

	var builder strings.Builder
	for _, content := range res.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			builder.WriteString(text.Text)
		}
	}

	if builder.Len() == 0 && res.StructuredContent != nil {
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return llm.ToolResult{}, fmt.Errorf("marshal structured MCP result: %w", err)
		}
		builder.Write(data)
	}

	return llm.ToolResult{
		Content: builder.String(),
		IsError: res.IsError,
	}, nil
}
