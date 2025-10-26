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
		LogLevel: "debug",
		Models: []config.Model{
			{Name: "gpt-4o", Provider: "openai", APIKey: "sk-xxx", Active: true},
			{Name: "llama2", Provider: "ollama", BaseURL: "http://localhost:11434"},
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

	if got.LogLevel != input.LogLevel {
		t.Fatalf("unexpected log level: %s", got.LogLevel)
	}
	if len(got.Models) != len(input.Models) {
		t.Fatalf("unexpected models size: %d", len(got.Models))
	}
	if got.Models[0].Name != input.Models[0].Name {
		t.Fatalf("unexpected model[0]: %+v", got.Models[0])
	}
	active, ok := got.ActiveModel()
	if !ok {
		t.Fatalf("expected active model to be present")
	}
	if active.Name != "gpt-4o" {
		t.Fatalf("unexpected active model: %+v", active)
	}
}

func TestFileStoreSavePersistsConfig(t *testing.T) {
	home := t.TempDir()
	store := config.NewFileStore(home)

	cfg := config.Config{
		LogLevel: "warn",
		Models: []config.Model{
			{Name: "gpt-4o", Provider: "openai", APIKey: "sk-xxx"},
			{Name: "llama2", Provider: "ollama", BaseURL: "http://localhost:11434", Active: true},
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
	if len(got.Models) != len(cfg.Models) {
		t.Fatalf("unexpected model count in persisted config: %d", len(got.Models))
	}
	if got.LogLevel != cfg.LogLevel {
		t.Fatalf("unexpected log level in persisted config: %s", got.LogLevel)
	}
	active, ok := got.ActiveModel()
	if !ok {
		t.Fatalf("expected active model in persisted config")
	}
	if active.Name != "llama2" {
		t.Fatalf("unexpected active model after save: %s", active.Name)
	}
}

func TestConfigValidateRejectsInvalidLogLevel(t *testing.T) {
	cfg := config.Config{
		LogLevel: "verbose",
		Models: []config.Model{
			{Name: "model", Provider: "openai", Active: true},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for invalid log level")
	}
}

func TestConfigValidateRejectsMultipleActiveModels(t *testing.T) {
	cfg := config.Config{
		Models: []config.Model{
			{Name: "model-a", Provider: "openai", Active: true},
			{Name: "model-b", Provider: "ollama", Active: true},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error when multiple models are active")
	}
}
