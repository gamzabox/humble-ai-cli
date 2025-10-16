package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrNotFound indicates that the configuration file does not exist.
var ErrNotFound = errors.New("config not found")

// Model represents a configured LLM model entry.
type Model struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey,omitempty"`
	BaseURL  string `json:"baseUrl,omitempty"`
}

// Config captures CLI configuration.
type Config struct {
	Provider    string  `json:"provider,omitempty"`
	ActiveModel string  `json:"activeModel,omitempty"`
	Models      []Model `json:"models,omitempty"`
}

// FindModel locates a model by name.
func (c Config) FindModel(name string) (Model, bool) {
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// Validate ensures configuration integrity.
func (c Config) Validate() error {
	if c.ActiveModel == "" {
		return nil
	}
	if _, ok := c.FindModel(c.ActiveModel); !ok {
		return fmt.Errorf("active model %q not found in models", c.ActiveModel)
	}
	return nil
}

// Store abstracts configuration persistence.
type Store interface {
	Load() (Config, error)
	Save(Config) error
}

// FileStore implements Store backed by the user's home directory.
type FileStore struct {
	home string
	mu   sync.Mutex
}

// NewFileStore creates a FileStore rooted at home.
func NewFileStore(home string) *FileStore {
	return &FileStore{home: home}
}

func (f *FileStore) configPath() string {
	return filepath.Join(f.home, ".config", "hunble-ai-cli", "config.json")
}

// Load reads configuration from disk.
func (f *FileStore) Load() (Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := f.configPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes configuration to disk.
func (f *FileStore) Save(cfg Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := cfg.Validate(); err != nil {
		return err
	}

	path := f.configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

var _ Store = (*FileStore)(nil)
