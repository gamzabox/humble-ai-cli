package config

import "sync"

// StaticStore keeps configuration in memory without touching disk.
type StaticStore struct {
	mu  sync.Mutex
	cfg Config
}

// NewStaticStore creates a read/write in-memory config store.
func NewStaticStore(cfg Config) *StaticStore {
	return &StaticStore{cfg: cfg}
}

// Load returns the stored configuration.
func (s *StaticStore) Load() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.cfg
	cfg.normalizeLegacyChunkSize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save updates the stored configuration after validation.
func (s *StaticStore) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg.normalizeLegacyChunkSize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

var _ Store = (*StaticStore)(nil)
