package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gamzabox/humble-ai-cli/internal/config"
	mcpkg "github.com/gamzabox/humble-ai-cli/internal/mcp"
	"github.com/gamzabox/humble-ai-cli/internal/workflow"
)

// ExecuteWorkflow runs the CLI in workflow mode using the provided definition.
func ExecuteWorkflow(ctx context.Context, opts Options, def workflow.Definition) error {
	home := opts.HomeDir
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determine home dir: %w", err)
		}
		home = dir
	}

	if opts.HistoryRootDir == "" {
		opts.HistoryRootDir = filepath.Join(home, ".humble-ai-cli", "sessions")
	}
	if opts.Store == nil {
		opts.Store = config.NewFileStore(home)
	}
	if def.BasicConfig != nil {
		opts.Store = config.NewStaticStore(*def.BasicConfig)
	}
	if def.MCPServers != nil {
		manager, err := mcpkg.NewManagerWithServers(home, def.MCPServers)
		if err != nil {
			return fmt.Errorf("MCP Servers: %w", err)
		}
		opts.MCP = manager
	}
	if def.UserRules != nil {
		opts.UserRules = def.UserRules
	}

	opts.HomeDir = home

	instance, err := New(opts)
	if err != nil {
		return err
	}
	defer func() {
		if instance != nil && instance.mcp != nil {
			_ = instance.mcp.Close()
		}
	}()

	return instance.runWorkflow(ctx, def.Steps)
}

func (a *App) runWorkflow(ctx context.Context, steps []workflow.Step) error {
	if len(steps) == 0 {
		return errors.New("workflow has no steps to run")
	}

	cfg := a.currentConfig()
	if err := validateWorkflowConfig(cfg); err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	display := responseDisplay{
		showWaiting:      false,
		showThinking:     false,
		showToolMessages: a.toolCallMode() == config.ToolCallModeManual,
	}

	for _, step := range steps {
		if _, err := a.handleUserMessageWithDisplay(ctx, step.Prompt, display); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowConfig(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	active, ok := cfg.ActiveModel()
	if !ok {
		return errors.New("workflow requires an active model")
	}

	provider := strings.ToLower(strings.TrimSpace(active.Provider))
	switch provider {
	case "openai":
		if strings.TrimSpace(active.APIKey) == "" {
			return errors.New("workflow active model (openai) requires apiKey")
		}
	case "ollama":
		if strings.TrimSpace(active.BaseURL) == "" {
			return errors.New("workflow active model (ollama) requires baseUrl")
		}
	default:
		return fmt.Errorf("workflow active model uses unsupported provider %q", active.Provider)
	}

	return nil
}

func (a *App) currentConfig() config.Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}
