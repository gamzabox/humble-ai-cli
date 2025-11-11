package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gamzabox/humble-ai-cli/internal/llm"
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

// ConfiguredServer represents an MCP server entry in mcp-servers.json.
type ConfiguredServer struct {
	Key         string
	Name        string
	Description string
	Enabled     bool
}

type serverConfig struct {
	Name        string
	Description string
	Enabled     bool
	Command     string
	Args        []string
	Env         map[string]string
	URL         string
	Transport   string
}

const (
	transportCommand = "command"
	transportSSE     = "sse"
	transportHTTP    = "http"
)

func (cfg serverConfig) connectionKind() (string, error) {
	command := strings.TrimSpace(cfg.Command)
	url := strings.TrimSpace(cfg.URL)

	if command != "" && url != "" {
		return "", fmt.Errorf("mcp server %q must define either a command or url", cfg.Name)
	}
	if command == "" && url == "" {
		return "", fmt.Errorf("mcp server %q has no command or url configured", cfg.Name)
	}

	if command != "" {
		return transportCommand, nil
	}

	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transport == "" {
		transport = transportSSE
	}

	switch transport {
	case transportSSE:
		return transportSSE, nil
	case transportHTTP:
		return transportHTTP, nil
	default:
		return "", fmt.Errorf("mcp server %q has unsupported transport %q", cfg.Name, cfg.Transport)
	}
}

type sessionDialer func(context.Context, serverConfig) (*sessionHolder, error)

// Manager loads server configurations and executes MCP tool calls.
type Manager struct {
	home     string
	mu       sync.Mutex
	servers  map[string]serverConfig
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

// Reload refreshes server configurations from disk.
func (m *Manager) Reload() error {
	servers, err := loadServerConfigs(m.home)
	if err != nil {
		return err
	}

	m.mu.Lock()
	toClose := make([]*sessionHolder, 0)
	for name, holder := range m.sessions {
		cfg, ok := servers[name]
		if !ok || !cfg.Enabled {
			if holder != nil {
				toClose = append(toClose, holder)
			}
			delete(m.sessions, name)
		}
	}
	m.servers = servers
	m.mu.Unlock()

	for _, holder := range toClose {
		_ = holder.Close()
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
	if _, err := cfg.connectionKind(); err != nil {
		m.mu.Unlock()
		return nil, err
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

	kind, err := cfg.connectionKind()
	if err != nil {
		return nil, err
	}

	switch kind {
	case transportCommand:
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if env := envList(cfg.Env); len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
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

	case transportSSE:
		httpClient := httpClientWithHeaders(cfg.Env)
		transport := &sdk.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
		}
		session, err := client.Connect(ctx, transport, nil)
		if err != nil {
			return nil, err
		}
		return newSessionHolder(session, nil), nil

	case transportHTTP:
		httpClient := httpClientWithHeaders(cfg.Env)
		transport := &sdk.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
		}
		session, err := client.Connect(ctx, transport, nil)
		if err != nil {
			return nil, err
		}
		return newSessionHolder(session, nil), nil
	}

	return nil, fmt.Errorf("unsupported transport for server %q", cfg.Name)
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

func configFilePath(home string) string {
	return filepath.Join(home, ".humble-ai-cli", "mcp-servers.json")
}

func readConfigFile(home string) (mcpConfigFile, error) {
	path := configFilePath(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return mcpConfigFile{Servers: map[string]rawServerConfig{}}, nil
	}
	if err != nil {
		return mcpConfigFile{}, fmt.Errorf("read MCP server configs: %w", err)
	}

	var file mcpConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return mcpConfigFile{}, fmt.Errorf("parse MCP server config: %w", err)
	}
	if file.Servers == nil {
		file.Servers = map[string]rawServerConfig{}
	}
	return file, nil
}

func writeConfigFile(home string, file mcpConfigFile) error {
	path := configFilePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create MCP config dir: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal MCP server config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write MCP server config: %w", err)
	}
	return nil
}

func boolPtr(value bool) *bool {
	v := value
	return &v
}

func loadServerConfigs(home string) (map[string]serverConfig, error) {
	file, err := readConfigFile(home)
	if err != nil {
		return nil, err
	}
	if len(file.Servers) == 0 {
		return map[string]serverConfig{}, nil
	}

	servers := make(map[string]serverConfig, len(file.Servers))
	for key, raw := range file.Servers {
		cfg, err := buildServerConfig(key, raw)
		if err != nil {
			return nil, err
		}
		if _, exists := servers[cfg.Name]; exists {
			return nil, fmt.Errorf("duplicate MCP server name %q", cfg.Name)
		}
		servers[cfg.Name] = cfg
	}
	return servers, nil
}

// ListConfiguredServers returns all configured servers with their enabled state.
func ListConfiguredServers(home string) ([]ConfiguredServer, error) {
	file, err := readConfigFile(home)
	if err != nil {
		return nil, err
	}
	if len(file.Servers) == 0 {
		return nil, nil
	}

	type resolved struct {
		key  string
		raw  rawServerConfig
		name string
	}

	list := make([]resolved, 0, len(file.Servers))
	for key, raw := range file.Servers {
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = strings.TrimSpace(key)
		}
		if name == "" {
			continue
		}
		list = append(list, resolved{
			key:  key,
			raw:  raw,
			name: name,
		})
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].name == list[j].name {
			return list[i].key < list[j].key
		}
		return list[i].name < list[j].name
	})

	configured := make([]ConfiguredServer, 0, len(list))
	for _, entry := range list {
		enabled := true
		if entry.raw.Enabled != nil {
			enabled = *entry.raw.Enabled
		}
		configured = append(configured, ConfiguredServer{
			Key:         entry.key,
			Name:        entry.name,
			Description: strings.TrimSpace(entry.raw.Description),
			Enabled:     enabled,
		})
	}
	return configured, nil
}

// SetServerEnabled updates the enabled flag for the specified server key.
func SetServerEnabled(home, key string, enabled bool) (ConfiguredServer, error) {
	file, err := readConfigFile(home)
	if err != nil {
		return ConfiguredServer{}, err
	}
	raw, ok := file.Servers[key]
	if !ok {
		return ConfiguredServer{}, fmt.Errorf("unknown MCP server %q", key)
	}
	raw.Enabled = boolPtr(enabled)
	file.Servers[key] = raw
	if err := writeConfigFile(home, file); err != nil {
		return ConfiguredServer{}, err
	}

	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = strings.TrimSpace(key)
	}

	return ConfiguredServer{
		Key:         key,
		Name:        name,
		Description: strings.TrimSpace(raw.Description),
		Enabled:     enabled,
	}, nil
}

type mcpConfigFile struct {
	Servers map[string]rawServerConfig `json:"mcpServers"`
}

type rawServerConfig struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Transport   string            `json:"transport,omitempty"`
}

func buildServerConfig(key string, raw rawServerConfig) (serverConfig, error) {
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = strings.TrimSpace(key)
	}
	if name == "" {
		return serverConfig{}, fmt.Errorf("invalid MCP server entry %q: missing name", key)
	}

	cfg := serverConfig{
		Name:        name,
		Description: strings.TrimSpace(raw.Description),
		Enabled:     true,
		Command:     strings.TrimSpace(raw.Command),
		Args:        append([]string(nil), raw.Args...),
		Env:         cloneStringMap(raw.Env),
		URL:         strings.TrimSpace(raw.URL),
		Transport:   strings.ToLower(strings.TrimSpace(raw.Transport)),
	}
	if raw.Enabled != nil {
		cfg.Enabled = *raw.Enabled
	}
	if cfg.Transport == transportCommand {
		cfg.Transport = ""
	}

	if cfg.Command != "" && cfg.URL != "" {
		return serverConfig{}, fmt.Errorf("server %q must define either a command or url, not both", name)
	}
	if cfg.Command == "" && cfg.URL == "" {
		return serverConfig{}, fmt.Errorf("server %q must define either a command or url", name)
	}
	if cfg.URL != "" {
		if cfg.Transport == "" {
			cfg.Transport = transportSSE
		}
		switch cfg.Transport {
		case transportSSE, transportHTTP:
		default:
			return serverConfig{}, fmt.Errorf("server %q has unsupported transport %q", name, cfg.Transport)
		}
	}

	return cfg, nil
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		dst[k] = strings.TrimSpace(value)
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
}

func envList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for key, value := range env {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", k, value))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func httpClientWithHeaders(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return nil
	}
	httpHeaders := make(http.Header, len(headers))
	for key, value := range headers {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		httpHeaders.Set(k, value)
	}
	if len(httpHeaders) == 0 {
		return nil
	}
	base := http.DefaultTransport
	return &http.Client{
		Transport: &headerInjector{
			base:    base,
			headers: httpHeaders,
		},
	}
}

type headerInjector struct {
	base    http.RoundTripper
	headers http.Header
}

func (h *headerInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	for key, values := range h.headers {
		if len(values) == 0 {
			continue
		}
		req.Header.Set(key, values[len(values)-1])
	}
	return base.RoundTrip(req)
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
