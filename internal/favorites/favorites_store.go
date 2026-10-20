// Package favorites stores each user's private set of favorited pages.
// Unlike tags/links/properties/search, this data is not derived from the
// filesystem tree and must never be touched by resync (see ADR-0001).
package favorites

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/perber/wiki/internal/core/shared"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/shared/sqliteutil"
	_ "modernc.org/sqlite"
)

const logCloseRowsFailed = "could not close rows"

// ErrCodeFavoritesStoreUnavailable identifies errFavoritesStoreUnavailable's
// LocalizedError. Mirrors ErrCodeAPIKeyStoreUnavailable.
const ErrCodeFavoritesStoreUnavailable = "favorites_store_unavailable"

// errFavoritesStoreUnavailable is returned while the store is suspended for
// an in-progress live restore (see FavoritesStore.PauseForSwap). Mirrors
// errAPIKeyStoreUnavailable.
func errFavoritesStoreUnavailable() error {
	return sharederrors.NewLocalizedError(
		ErrCodeFavoritesStoreUnavailable,
		"The server is restoring from a backup — please try again in a moment",
		"favorites store is suspended for an in-progress restore",
		nil,
	)
}

type FavoritesStore struct {
	mu sync.Mutex
	db *sql.DB
	// suspended is set by PauseForSwap and makes withDB refuse to serve
	// queries against a possibly-stale db handle until Replace reopens it.
	suspended bool
	log       *slog.Logger
}

func NewFavoritesStore(storageDir string, log *slog.Logger) (*FavoritesStore, error) {
	normalized := filepath.FromSlash(strings.ReplaceAll(storageDir, `\`, `/`))
	dbPath := filepath.Join(normalized, "favorites.db")

	s := &FavoritesStore{log: log}
	err := sqliteutil.OpenWithPolicy(dbPath, sqliteutil.QuarantineOnCorruption, func() error {
		db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
		if err != nil {
			return fmt.Errorf("failed to open favorites database: %w", err)
		}
		s.db = db
		if err := s.ensureSchema(); err != nil {
			_ = db.Close()
			s.db = nil
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FavoritesStore) ensureSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS favorites (
			user_id    TEXT NOT NULL,
			page_id    TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			PRIMARY KEY (user_id, page_id)
		);
		CREATE INDEX IF NOT EXISTS favorites_user_id_idx ON favorites(user_id);

		-- Team reading lists ("favorite bundles") and the per-user
		-- provenance of which favorites each bundle brought in. All in
		-- favorites.db so a reconcile can touch favorites, membership and
		-- subscriptions in one transaction, and so the data rides the
		-- existing snapshot/live-restore channel unchanged. No FOREIGN KEY
		-- pragma is enabled anywhere in this repo — cascades are explicit
		-- deletes in the store (DeleteAllForPage/DeleteAllForUser/DeleteBundle).
		CREATE TABLE IF NOT EXISTS favorite_bundles (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_by  TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMP NOT NULL,
			updated_at  TIMESTAMP NOT NULL
		);
		CREATE TABLE IF NOT EXISTS favorite_bundle_entries (
			bundle_id TEXT NOT NULL,
			page_id   TEXT NOT NULL,
			position  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bundle_id, page_id)
		);
		CREATE TABLE IF NOT EXISTS favorite_bundle_subscriptions (
			user_id        TEXT NOT NULL,
			bundle_id      TEXT NOT NULL,
			subscribed_at  TIMESTAMP NOT NULL,
			last_synced_at TIMESTAMP,
			PRIMARY KEY (user_id, bundle_id)
		);
		CREATE TABLE IF NOT EXISTS favorite_bundle_membership (
			user_id   TEXT NOT NULL,
			bundle_id TEXT NOT NULL,
			page_id   TEXT NOT NULL,
			added_at  TIMESTAMP NOT NULL,
			PRIMARY KEY (user_id, bundle_id, page_id)
		);
		CREATE INDEX IF NOT EXISTS favorite_bundle_membership_user_page_idx
			ON favorite_bundle_membership(user_id, page_id);
	`)
	return err
}

// withDB runs fn against the current db handle, or returns
// errFavoritesStoreUnavailable if the store is suspended for an in-progress
// restore. Mirrors APIKeyStore.withDB.
func (s *FavoritesStore) withDB(fn func(db *sql.DB) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.suspended || s.db == nil {
		return errFavoritesStoreUnavailable()
	}
	return fn(s.db)
}

// withTx runs fn inside a single transaction committed on nil error and
// rolled back otherwise (a rollback failure is joined onto the original
// error). Bundle reconciliation relies on this atomicity: a missing page
// or a conflict detected mid-reconcile must leave every table untouched.
func (s *FavoritesStore) withTx(fn func(tx *sql.Tx) error) error {
	return s.withDB(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			rbErr := tx.Rollback()
			if rbErr != nil {
				return errors.Join(err, rbErr)
			}
			return err
		}
		return tx.Commit()
	})
}

// Add favorites pageID for userID. Idempotent — favoriting an already-favorited page is a no-op.
func (s *FavoritesStore) Add(userID, pageID string) error {
	err := s.withDB(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT OR IGNORE INTO favorites (user_id, page_id, created_at) VALUES (?, ?, ?)`,
			userID, pageID, time.Now().UTC(),
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to add favorite for user %s, page %s: %w", userID, pageID, err)
	}
	return nil
}

// Remove un-favorites pageID for userID. Idempotent — removing a non-favorited page is a no-op.
func (s *FavoritesStore) Remove(userID, pageID string) error {
	err := s.withDB(func(db *sql.DB) error {
		_, err := db.Exec(`DELETE FROM favorites WHERE user_id = ? AND page_id = ?`, userID, pageID)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to remove favorite for user %s, page %s: %w", userID, pageID, err)
	}
	return nil
}

// ListPageIDsForUser returns the page IDs favorited by userID, most recently favorited first.
func (s *FavoritesStore) ListPageIDsForUser(userID string) ([]string, error) {
	var pageIDs []string
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(
			`SELECT page_id FROM favorites WHERE user_id = ? ORDER BY created_at DESC`,
			userID,
		)
		if err != nil {
			return err
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)

		for rows.Next() {
			var pageID string
			if err := rows.Scan(&pageID); err != nil {
				return err
			}
			pageIDs = append(pageIDs, pageID)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return pageIDs, nil
}

// DeleteAllForPage removes every user's favorite of pageID along with the
// bundle entries and memberships that referenced it. Called on page delete.
func (s *FavoritesStore) DeleteAllForPage(pageID string) error {
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_entries WHERE page_id = ?`, pageID); err != nil {
			return fmt.Errorf("failed to delete bundle entries for page %s: %w", pageID, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_membership WHERE page_id = ?`, pageID); err != nil {
			return fmt.Errorf("failed to delete bundle membership for page %s: %w", pageID, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorites WHERE page_id = ?`, pageID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete favorites for page %s: %w", pageID, err)
	}
	return nil
}

// DeleteAllForUser removes every favorite, bundle subscription and bundle
// membership belonging to userID. Called on user delete.
func (s *FavoritesStore) DeleteAllForUser(userID string) error {
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_membership WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("failed to delete bundle membership for user %s: %w", userID, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_subscriptions WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("failed to delete bundle subscriptions for user %s: %w", userID, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorites WHERE user_id = ?`, userID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete favorites for user %s: %w", userID, err)
	}
	return nil
}

// PauseForSwap closes the current *sql.DB connection and marks the store
// suspended so withDB refuses to serve queries against a possibly-stale
// handle, releasing any OS-level file lock on favorites.db before a live
// restore renames it. Idempotent: a second call is a safe no-op. Mirrors
// APIKeyStore.suspend.
func (s *FavoritesStore) PauseForSwap() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.suspended = true
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Replace reopens favorites.db from storageDir and swaps the new connection
// in, closing the previous one afterward — used by a live restore once
// favorites.db has been swapped in on disk (or left untouched, if this
// snapshot didn't capture one). Preserves this FavoritesStore's identity: no
// caller needs to be told about the new connection, they already hold this
// pointer. Mirrors APIKeyService.Replace, adapted for the fact that
// FavoritesStore has no separate service-layer indirection to swap.
func (s *FavoritesStore) Replace(storageDir string) error {
	fresh, err := NewFavoritesStore(storageDir, s.log)
	if err != nil {
		return err
	}

	s.mu.Lock()
	old := s.db
	s.db = fresh.db
	s.suspended = false
	s.mu.Unlock()

	if old != nil {
		if err := old.Close(); err != nil && s.log != nil {
			s.log.Warn("failed to close previous favorites store after restore", "error", err)
		}
	}
	return nil
}

func (s *FavoritesStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			return err
		}
		s.db = nil
	}
	return nil
}
