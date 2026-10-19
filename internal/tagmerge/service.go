package tagmerge

import (
	"fmt"

	"github.com/perber/wiki/internal/core/settings"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
)

// RulesFilename is the JSON file, flat in the data directory, that holds the
// current tag merge rules.
const RulesFilename = "tag-merge-rules.json"

// Service persists and serves the current tag merge rules. Safe for
// concurrent use — the underlying settings.Store provides locking.
type Service struct {
	store *settings.Store[Config]
}

// New returns a Service reading (and writing) <storageDir>/tag-merge-rules.json.
// A missing file means an empty rule set.
func New(storageDir string) (*Service, error) {
	st, err := settings.New(storageDir, RulesFilename, 0o600, Config{}, func(cfg *Config) {
		NormalizeConfig(cfg)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read tag merge rules: %w", err)
	}
	return &Service{store: st}, nil
}

// Rules returns the normalized rules currently in effect.
func (s *Service) Rules() Config {
	return s.store.Get()
}

// Save validates, normalizes and persists cfg.
func (s *Service) Save(cfg Config) error {
	if err := ValidateConfig(cfg); err != nil {
		return sharederrors.NewLocalizedError(
			"tag_merge_invalid_rules",
			"Invalid tag merge rules",
			"invalid tag merge rules",
			err,
		)
	}
	NormalizeConfig(&cfg)
	if err := s.store.Put(cfg); err != nil {
		return sharederrors.NewLocalizedError(
			"tag_merge_internal_error",
			"Failed to persist tag merge rules",
			"failed to persist tag merge rules",
			err,
		)
	}
	return nil
}

// Reload re-reads the rules file, e.g. after a restore swapped the data dir.
func (s *Service) Reload() error {
	if err := s.store.Reload(); err != nil {
		return fmt.Errorf("failed to reload tag merge rules: %w", err)
	}
	return nil
}
