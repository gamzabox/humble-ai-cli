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

type serverConfig struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Enabled     bool     `json:"enabled"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
}

// Manager loads server configurations and executes MCP tool calls.
type Manager struct {
	home    string
	mu      sync.Mutex
	servers map[string]serverConfig
}

// NewManager creates a Manager rooted at the provided home directory.
func NewManager(home string) (*Manager, error) {
	servers, err := loadServerConfigs(home)
	if err != nil {
		return nil, err
	}
	return &Manager{
		home:    home,
		servers: servers,
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
	m.mu.Lock()
	cfg, ok := m.servers[server]
	m.mu.Unlock()
	if !ok || !cfg.Enabled {
		return llm.ToolResult{}, fmt.Errorf("unknown MCP server %q", server)
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return llm.ToolResult{}, fmt.Errorf("mcp server %q has no command configured", server)
	}

	client := sdk.NewClient(&sdk.Implementation{
		Name:    "humble-ai-cli",
		Version: "0.1.0",
	}, nil)

	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	transport := &sdk.CommandTransport{Command: cmd}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("connect MCP server %q: %w", server, err)
	}
	defer session.Close()

	params := &sdk.CallToolParams{
		Name:      method,
		Arguments: arguments,
	}
	result, err := session.CallTool(ctx, params)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("call tool %q on server %q: %w", method, server, err)
	}
	return convertResult(result)
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
