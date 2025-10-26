package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gamzabox.com/humble-ai-cli/internal/config"
)

func TestFileStoreLoadReadsConfigFromDefaultPath(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".humble-ai-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to prepare config directory: %v", err)
	}

	cfgPath := filepath.Join(configDir, "config.json")
	input := config.Config{
		Provider:    "openai",
		ActiveModel: "gpt-4o",
		LogLevel:    "debug",
		Models: []config.Model{
			{Name: "gpt-4o", Provider: "openai", APIKey: "sk-xxx"},
		},
	}
	raw, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal fixture: %v", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	store := config.NewFileStore(home)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.Provider != input.Provider || got.ActiveModel != input.ActiveModel {
		t.Fatalf("unexpected config: %+v", got)
	}
	if got.LogLevel != input.LogLevel {
		t.Fatalf("unexpected log level: %s", got.LogLevel)
	}
	if len(got.Models) != len(input.Models) {
		t.Fatalf("unexpected models size: %d", len(got.Models))
	}
	if got.Models[0].Name != input.Models[0].Name {
		t.Fatalf("unexpected model[0]: %+v", got.Models[0])
	}
}

func TestFileStoreSavePersistsConfig(t *testing.T) {
	home := t.TempDir()
	store := config.NewFileStore(home)

	cfg := config.Config{
		Provider:    "ollama",
		ActiveModel: "llama2",
		LogLevel:    "warn",
		Models: []config.Model{
			{Name: "gpt-4o", Provider: "openai", APIKey: "sk-xxx"},
			{Name: "llama2", Provider: "ollama", BaseURL: "http://localhost:11434"},
		},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfgPath := filepath.Join(home, ".humble-ai-cli", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read persisted config: %v", err)
	}
	var got config.Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("failed to unmarshal persisted config: %v", err)
	}
	if got.ActiveModel != cfg.ActiveModel {
		t.Fatalf("unexpected active model: %s", got.ActiveModel)
	}
	if len(got.Models) != len(cfg.Models) {
		t.Fatalf("unexpected model count in persisted config: %d", len(got.Models))
	}
	if got.LogLevel != cfg.LogLevel {
		t.Fatalf("unexpected log level in persisted config: %s", got.LogLevel)
	}
}

func TestConfigValidateRejectsInvalidLogLevel(t *testing.T) {
	cfg := config.Config{
		ActiveModel: "model",
		LogLevel:    "verbose",
		Models: []config.Model{
			{Name: "model", Provider: "openai"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for invalid log level")
	}
}
