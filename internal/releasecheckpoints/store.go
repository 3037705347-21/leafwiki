package releasecheckpoints

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/perber/wiki/internal/core/shared"
	"github.com/perber/wiki/internal/core/shared/sqliteutil"
	_ "modernc.org/sqlite"
)

const logCloseRowsFailed = "could not close rows"

// Store persists release checkpoints in release_checkpoints.db. It is real,
// user-authored state (like the favorites store) — never rebuilt by resync —
// and therefore quarantined on corruption and swapped as a unit during a live
// restore.
type Store struct {
	mu        sync.Mutex
	db        *sql.DB
	suspended bool
	log       *slog.Logger
}

func NewStore(storageDir string, log *slog.Logger) (*Store, error) {
	normalized := filepath.FromSlash(strings.ReplaceAll(storageDir, `\`, `/`))
	dbPath := filepath.Join(normalized, "release_checkpoints.db")

	s := &Store{log: log}
	err := sqliteutil.OpenWithPolicy(dbPath, sqliteutil.QuarantineOnCorruption, func() error {
		db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
		if err != nil {
			return fmt.Errorf("failed to open release checkpoints database: %w", err)
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

func (s *Store) ensureSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS release_checkpoints (
			version_name TEXT    NOT NULL,
			page_id      TEXT    NOT NULL,
			revision_id  TEXT    NOT NULL,
			content_hash TEXT    NOT NULL,
			page_path    TEXT    NOT NULL,
			created_at   TIMESTAMP NOT NULL,
			PRIMARY KEY (version_name, page_id)
		);
		CREATE INDEX IF NOT EXISTS release_checkpoints_page_id_idx
			ON release_checkpoints(page_id);
	`)
	return err
}

// withDB runs fn against the current db handle, or returns
// errStoreUnavailable while suspended for an in-progress restore.
func (s *Store) withDB(fn func(db *sql.DB) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.suspended || s.db == nil {
		return errStoreUnavailable()
	}
	return fn(s.db)
}

type storedRow struct {
	revisionID string
	pagePath   string
	createdAt  time.Time
}

func checkpointKey(versionName, pageID string) string {
	return versionName + "\x00" + pageID
}

// Reconcile replaces the whole checkpoint set with desired, in one
// transaction. A stored (version, page) row whose revision no longer matches
// is a content drift and aborts the transaction before any write, leaving the
// previous state untouched.
func (s *Store) Reconcile(desired []DesiredCheckpoint, now time.Time) (ReconcileResult, error) {
	var result ReconcileResult
	err := s.withDB(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin release checkpoint transaction: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()

		rows, err := tx.Query(`
			SELECT version_name, page_id, revision_id, page_path, created_at
			FROM release_checkpoints
		`)
		if err != nil {
			return fmt.Errorf("load release checkpoints: %w", err)
		}
		existing := make(map[string]storedRow)
		for rows.Next() {
			var versionName, pageID, revisionID, pagePath string
			var createdAt time.Time
			if err := rows.Scan(&versionName, &pageID, &revisionID, &pagePath, &createdAt); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan release checkpoint: %w", err)
			}
			existing[checkpointKey(versionName, pageID)] = storedRow{
				revisionID: revisionID, pagePath: pagePath, createdAt: createdAt,
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("iterate release checkpoints: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close release checkpoint rows: %w", err)
		}

		desiredKeys := make(map[string]bool, len(desired))
		for _, d := range desired {
			key := checkpointKey(d.VersionName, d.PageID)
			desiredKeys[key] = true
			if old, ok := existing[key]; ok {
				if old.revisionID != d.RevisionID {
					return errDrift(d.VersionName, d.PagePath)
				}
				result.Unchanged++
			} else {
				result.Created++
			}
		}
		for key := range existing {
			if !desiredKeys[key] {
				result.Removed++
			}
		}

		if result.Created == 0 && result.Removed == 0 {
			committed = true
			return nil // nothing to write; Rollback of a read-only tx is harmless
		}

		if _, err := tx.Exec(`DELETE FROM release_checkpoints`); err != nil {
			return fmt.Errorf("clear release checkpoints: %w", err)
		}
		insert, err := tx.Prepare(`
			INSERT INTO release_checkpoints
				(version_name, page_id, revision_id, content_hash, page_path, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("prepare release checkpoint insert: %w", err)
		}
		defer func() { _ = insert.Close() }()

		for _, d := range desired {
			createdAt := now
			if old, ok := existing[checkpointKey(d.VersionName, d.PageID)]; ok {
				createdAt = old.createdAt // keep original checkpoint time on no-op rows
			}
			if _, err := insert.Exec(
				d.VersionName, d.PageID, d.RevisionID, d.ContentHash, d.PagePath, createdAt,
			); err != nil {
				return fmt.Errorf("insert release checkpoint (%s, %s): %w", d.VersionName, d.PagePath, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit release checkpoint reconcile: %w", err)
		}
		committed = true
		return nil
	})
	if err != nil {
		return ReconcileResult{}, err
	}
	return result, nil
}

// ListAll returns every checkpoint ordered by version name and page path.
func (s *Store) ListAll() ([]Checkpoint, error) {
	var out []Checkpoint
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(`
			SELECT version_name, page_id, revision_id, content_hash, page_path, created_at
			FROM release_checkpoints
			ORDER BY version_name ASC, page_path ASC
		`)
		if err != nil {
			return fmt.Errorf("list release checkpoints: %w", err)
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)

		for rows.Next() {
			var cp Checkpoint
			if err := rows.Scan(
				&cp.VersionName, &cp.PageID, &cp.RevisionID, &cp.ContentHash, &cp.PagePath, &cp.CreatedAt,
			); err != nil {
				return fmt.Errorf("scan release checkpoint: %w", err)
			}
			out = append(out, cp)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListRevisionNames maps each revision ID of a page to the names of the
// releases checkpointing it (ordered, de-duplicated by the primary key).
func (s *Store) ListRevisionNames(pageID string) (map[string][]string, error) {
	out := make(map[string][]string)
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(`
			SELECT revision_id, version_name
			FROM release_checkpoints
			WHERE page_id = ?
			ORDER BY version_name ASC
		`, pageID)
		if err != nil {
			return fmt.Errorf("list checkpoint names for page %s: %w", pageID, err)
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)

		for rows.Next() {
			var revisionID, versionName string
			if err := rows.Scan(&revisionID, &versionName); err != nil {
				return fmt.Errorf("scan checkpoint name: %w", err)
			}
			out[revisionID] = append(out[revisionID], versionName)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ProtectedRevisionIDsForPage returns revision IDs pinned by checkpoints, so
// revision pruning must not delete them.
func (s *Store) ProtectedRevisionIDsForPage(pageID string) (map[string]bool, error) {
	out := make(map[string]bool)
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(
			`SELECT DISTINCT revision_id FROM release_checkpoints WHERE page_id = ?`,
			pageID,
		)
		if err != nil {
			return fmt.Errorf("list protected revisions for page %s: %w", pageID, err)
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)

		for rows.Next() {
			var revisionID string
			if err := rows.Scan(&revisionID); err != nil {
				return fmt.Errorf("scan protected revision: %w", err)
			}
			out[revisionID] = true
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteAllForPage removes every checkpoint of pageID. Called on page delete.
func (s *Store) DeleteAllForPage(pageID string) error {
	err := s.withDB(func(db *sql.DB) error {
		_, err := db.Exec(`DELETE FROM release_checkpoints WHERE page_id = ?`, pageID)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to delete release checkpoints for page %s: %w", pageID, err)
	}
	return nil
}

// PauseForSwap closes the db handle and blocks queries for the duration of a
// live restore. Mirrors FavoritesStore.PauseForSwap.
func (s *Store) PauseForSwap() error {
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

// Replace reopens release_checkpoints.db from storageDir after a restore
// swap. Mirrors FavoritesStore.Replace.
func (s *Store) Replace(storageDir string) error {
	fresh, err := NewStore(storageDir, s.log)
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
			s.log.Warn("failed to close previous release checkpoint store after restore", "error", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
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
