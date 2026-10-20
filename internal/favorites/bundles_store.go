package favorites

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/perber/wiki/internal/core/shared"
)

// Sentinel errors for favorite bundle operations. The HTTP slice
// (internal/wiki/favoritebundles) maps these to localized error codes; keep
// them plain errors so the store stays free of HTTP concerns.
var (
	// ErrBundleNotFound is returned when an operation targets a bundle id
	// that no longer exists.
	ErrBundleNotFound = errors.New("favorite bundle not found")

	// ErrDuplicateBundleEntry is returned when a bundle create/update
	// payload lists the same page id more than once — "the same entry
	// conflicts", so the whole save is rejected.
	ErrDuplicateBundleEntry = errors.New("duplicate page in favorite bundle entries")

	// ErrPersonalFavoriteConflict is returned when a sync would take over a
	// page the user already favorited manually (a favorite row with no
	// bundle membership). Ownership would be ambiguous, so the whole sync is
	// rejected and nothing changes.
	ErrPersonalFavoriteConflict = errors.New("page is already a personal favorite")

	// ErrBundleModified is returned when the bundle's entries change
	// between the use case's existence validation and the reconcile
	// transaction. The client should refetch and retry; nothing changed.
	ErrBundleModified = errors.New("favorite bundle changed during sync")
)

// Bundle is an admin-managed named reading list.
type Bundle struct {
	ID          string
	Name        string
	Description string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BundleEntry is one page in a bundle, ordered by Position.
type BundleEntry struct {
	BundleID string
	PageID   string
	Position int
}

// Subscription records one user's subscription to one bundle and when it
// was last reconciled.
type Subscription struct {
	UserID       string
	BundleID     string
	SubscribedAt time.Time
	LastSyncedAt *time.Time
}

// SyncResult reports what a reconcile changed.
type SyncResult struct {
	Added        []string
	Removed      []string
	LastSyncedAt time.Time
}

// CreateBundle inserts a new bundle and its entries in one transaction.
// pageIDs give the entries in display order. A repeated page id makes the
// whole create fail with ErrDuplicateBundleEntry before anything is written.
func (s *FavoritesStore) CreateBundle(name, description string, pageIDs []string, createdBy string) (Bundle, error) {
	if err := checkDuplicateEntries(pageIDs); err != nil {
		return Bundle{}, err
	}
	id, err := shared.GenerateUniqueID()
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to generate favorite bundle id: %w", err)
	}
	now := time.Now().UTC()
	bundle := Bundle{
		ID:          id,
		Name:        name,
		Description: description,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err = s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO favorite_bundles (id, name, description, created_by, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			bundle.ID, bundle.Name, bundle.Description, bundle.CreatedBy, bundle.CreatedAt, bundle.UpdatedAt,
		); err != nil {
			return fmt.Errorf("failed to insert favorite bundle: %w", err)
		}
		return insertEntries(tx, bundle.ID, pageIDs)
	})
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to create favorite bundle: %w", err)
	}
	return bundle, nil
}

// UpdateBundle replaces the bundle's name/description and its full entry
// set in one transaction. A repeated page id fails the whole update with
// ErrDuplicateBundleEntry; a missing bundle id fails with ErrBundleNotFound.
func (s *FavoritesStore) UpdateBundle(id, name, description string, pageIDs []string) error {
	if err := checkDuplicateEntries(pageIDs); err != nil {
		return err
	}
	now := time.Now().UTC()
	err := s.withTx(func(tx *sql.Tx) error {
		if err := requireBundleExists(tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE favorite_bundles SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
			name, description, now, id,
		); err != nil {
			return fmt.Errorf("failed to update favorite bundle %s: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_entries WHERE bundle_id = ?`, id); err != nil {
			return fmt.Errorf("failed to replace entries of favorite bundle %s: %w", id, err)
		}
		return insertEntries(tx, id, pageIDs)
	})
	if err != nil {
		return fmt.Errorf("failed to update favorite bundle %s: %w", id, err)
	}
	return nil
}

// DeleteBundle removes the bundle definition, its entries, every
// subscription and every membership — but never the favorites rows.
// Favorites the bundle brought in become indistinguishable from personal
// favorites and are kept (deleting user data is not the list's call to
// make, especially with overlapping bundles).
func (s *FavoritesStore) DeleteBundle(id string) error {
	err := s.withTx(func(tx *sql.Tx) error {
		if err := requireBundleExists(tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_membership WHERE bundle_id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete memberships of favorite bundle %s: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_subscriptions WHERE bundle_id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete subscriptions to favorite bundle %s: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorite_bundle_entries WHERE bundle_id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete entries of favorite bundle %s: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM favorite_bundles WHERE id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete favorite bundle %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete favorite bundle %s: %w", id, err)
	}
	return nil
}

// GetBundle returns the bundle and its entries ordered by position.
func (s *FavoritesStore) GetBundle(id string) (Bundle, []BundleEntry, error) {
	var bundle Bundle
	err := s.withDB(func(db *sql.DB) error {
		err := db.QueryRow(
			`SELECT id, name, description, created_by, created_at, updated_at FROM favorite_bundles WHERE id = ?`,
			id,
		).Scan(&bundle.ID, &bundle.Name, &bundle.Description, &bundle.CreatedBy, &bundle.CreatedAt, &bundle.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBundleNotFound
		}
		return err
	})
	if err != nil {
		return Bundle{}, nil, fmt.Errorf("failed to get favorite bundle %s: %w", id, err)
	}
	entries, err := s.ListEntries(id)
	if err != nil {
		return Bundle{}, nil, err
	}
	return bundle, entries, nil
}

// ListBundles returns every bundle, newest first.
func (s *FavoritesStore) ListBundles() ([]Bundle, error) {
	var bundles []Bundle
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(
			`SELECT id, name, description, created_by, created_at, updated_at
			 FROM favorite_bundles ORDER BY created_at DESC, name ASC`,
		)
		if err != nil {
			return err
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)
		for rows.Next() {
			var b Bundle
			if err := rows.Scan(&b.ID, &b.Name, &b.Description, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt); err != nil {
				return err
			}
			bundles = append(bundles, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list favorite bundles: %w", err)
	}
	return bundles, nil
}

// ListEntries returns the pages of bundleID ordered by position.
func (s *FavoritesStore) ListEntries(bundleID string) ([]BundleEntry, error) {
	var entries []BundleEntry
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(
			`SELECT bundle_id, page_id, position FROM favorite_bundle_entries
			 WHERE bundle_id = ? ORDER BY position ASC, page_id ASC`,
			bundleID,
		)
		if err != nil {
			return err
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)
		for rows.Next() {
			var e BundleEntry
			if err := rows.Scan(&e.BundleID, &e.PageID, &e.Position); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list entries of favorite bundle %s: %w", bundleID, err)
	}
	return entries, nil
}

// ListAllEntries returns the entries of every bundle (ordered by bundle
// then position), for building list responses in one round trip.
func (s *FavoritesStore) ListAllEntries() ([]BundleEntry, error) {
	var entries []BundleEntry
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(
			`SELECT bundle_id, page_id, position FROM favorite_bundle_entries
			 ORDER BY bundle_id ASC, position ASC, page_id ASC`,
		)
		if err != nil {
			return err
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)
		for rows.Next() {
			var e BundleEntry
			if err := rows.Scan(&e.BundleID, &e.PageID, &e.Position); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list favorite bundle entries: %w", err)
	}
	return entries, nil
}

// ListSubscriptionStates returns the subscriptions of userID keyed by
// bundle id.
func (s *FavoritesStore) ListSubscriptionStates(userID string) (map[string]Subscription, error) {
	states := make(map[string]Subscription)
	err := s.withDB(func(db *sql.DB) error {
		rows, err := db.Query(
			`SELECT user_id, bundle_id, subscribed_at, last_synced_at
			 FROM favorite_bundle_subscriptions WHERE user_id = ?`,
			userID,
		)
		if err != nil {
			return err
		}
		defer shared.LogClose(rows.Close, logCloseRowsFailed)
		for rows.Next() {
			var sub Subscription
			var lastSynced sql.NullTime
			if err := rows.Scan(&sub.UserID, &sub.BundleID, &sub.SubscribedAt, &lastSynced); err != nil {
				return err
			}
			if lastSynced.Valid {
				t := lastSynced.Time
				sub.LastSyncedAt = &t
			}
			states[sub.BundleID] = sub
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list bundle subscriptions for user %s: %w", userID, err)
	}
	return states, nil
}

// ReconcileBundleMembership synchronizes one user's favorites to the
// bundle's validated entry set in one transaction:
//
//   - Every desired page ends up both favorited and owned (membership) by
//     this bundle. A desired page the user already favorited MANUALLY (a
//     favorite with no membership) is a conflict — ErrPersonalFavoriteConflict
//     and the whole sync rolls back.
//   - Pages this bundle used to own but no longer lists lose this bundle's
//     membership, and the favorite is deleted only when no other subscribed
//     bundle still owns it.
//   - The subscription is upserted and last_synced_at refreshed.
//
// validatedPageIDs must be the bundle entry page ids in any order; the use
// case has already verified every page exists. If the entries changed since
// that validation, ErrBundleModified is returned and nothing changed.
func (s *FavoritesStore) ReconcileBundleMembership(userID, bundleID string, validatedPageIDs []string) (SyncResult, error) {
	now := time.Now().UTC()
	result := SyncResult{LastSyncedAt: now}

	err := s.withTx(func(tx *sql.Tx) error {
		if err := requireBundleExists(tx, bundleID); err != nil {
			return err
		}

		current, err := queryEntryPageIDs(tx, bundleID)
		if err != nil {
			return err
		}
		if !sameStringSet(current, validatedPageIDs) {
			return ErrBundleModified
		}

		desired := dedupeInOrder(validatedPageIDs)
		conflicts, err := queryPersonalConflictPageIDs(tx, userID, desired)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return ErrPersonalFavoriteConflict
		}

		if _, err := tx.Exec(
			`INSERT INTO favorite_bundle_subscriptions (user_id, bundle_id, subscribed_at, last_synced_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(user_id, bundle_id)
			 DO UPDATE SET last_synced_at = excluded.last_synced_at`,
			userID, bundleID, now, now,
		); err != nil {
			return fmt.Errorf("failed to upsert bundle subscription: %w", err)
		}

		owned, err := queryMembershipPageIDs(tx, userID, bundleID)
		if err != nil {
			return err
		}
		ownedSet := toSet(owned)

		for _, pageID := range desired {
			// INSERT OR IGNORE on both tables keeps the sync idempotent and
			// re-claims a favorite the user manually un-favorited while the
			// membership still listed it as bundle-owned.
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO favorites (user_id, page_id, created_at) VALUES (?, ?, ?)`,
				userID, pageID, now,
			); err != nil {
				return fmt.Errorf("failed to add favorite for page %s: %w", pageID, err)
			}
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO favorite_bundle_membership (user_id, bundle_id, page_id, added_at)
				 VALUES (?, ?, ?, ?)`,
				userID, bundleID, pageID, now,
			); err != nil {
				return fmt.Errorf("failed to add bundle membership for page %s: %w", pageID, err)
			}
			if !ownedSet[pageID] {
				result.Added = append(result.Added, pageID)
			}
		}

		for _, pageID := range owned {
			if contains(desired, pageID) {
				continue
			}
			if _, err := tx.Exec(
				`DELETE FROM favorite_bundle_membership WHERE user_id = ? AND bundle_id = ? AND page_id = ?`,
				userID, bundleID, pageID,
			); err != nil {
				return fmt.Errorf("failed to remove bundle membership for page %s: %w", pageID, err)
			}
			// Another bundle may still own it — then the favorite stays.
			if _, err := tx.Exec(
				`DELETE FROM favorites WHERE user_id = ? AND page_id = ?
				 AND NOT EXISTS (
					SELECT 1 FROM favorite_bundle_membership m
					WHERE m.user_id = favorites.user_id AND m.page_id = favorites.page_id
				 )`,
				userID, pageID,
			); err != nil {
				return fmt.Errorf("failed to remove favorite for page %s: %w", pageID, err)
			}
			result.Removed = append(result.Removed, pageID)
		}
		return nil
	})
	if err != nil {
		return SyncResult{}, fmt.Errorf("failed to sync favorite bundle %s for user %s: %w", bundleID, userID, err)
	}
	return result, nil
}

// Unsubscribe removes the user's subscription and the favorites this
// bundle brought in, except those another subscription still owns.
// Personal favorites are never touched. Idempotent — unsubscribing without
// a subscription is a no-op; a missing bundle returns ErrBundleNotFound.
func (s *FavoritesStore) Unsubscribe(userID, bundleID string) error {
	err := s.withTx(func(tx *sql.Tx) error {
		if err := requireBundleExists(tx, bundleID); err != nil {
			return err
		}
		owned, err := queryMembershipPageIDs(tx, userID, bundleID)
		if err != nil {
			return err
		}
		for _, pageID := range owned {
			if _, err := tx.Exec(
				`DELETE FROM favorite_bundle_membership WHERE user_id = ? AND bundle_id = ? AND page_id = ?`,
				userID, bundleID, pageID,
			); err != nil {
				return fmt.Errorf("failed to remove bundle membership for page %s: %w", pageID, err)
			}
			if _, err := tx.Exec(
				`DELETE FROM favorites WHERE user_id = ? AND page_id = ?
				 AND NOT EXISTS (
					SELECT 1 FROM favorite_bundle_membership m
					WHERE m.user_id = favorites.user_id AND m.page_id = favorites.page_id
				 )`,
				userID, pageID,
			); err != nil {
				return fmt.Errorf("failed to remove favorite for page %s: %w", pageID, err)
			}
		}
		if _, err := tx.Exec(
			`DELETE FROM favorite_bundle_subscriptions WHERE user_id = ? AND bundle_id = ?`,
			userID, bundleID,
		); err != nil {
			return fmt.Errorf("failed to remove bundle subscription: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to unsubscribe user %s from favorite bundle %s: %w", userID, bundleID, err)
	}
	return nil
}

// ─── tx-local helpers ────────────────────────────────────────────────────────

func requireBundleExists(tx *sql.Tx, id string) error {
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM favorite_bundles WHERE id = ?`, id).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBundleNotFound
		}
		return fmt.Errorf("failed to look up favorite bundle %s: %w", id, err)
	}
	return nil
}

func insertEntries(tx *sql.Tx, bundleID string, pageIDs []string) error {
	for i, pageID := range pageIDs {
		if _, err := tx.Exec(
			`INSERT INTO favorite_bundle_entries (bundle_id, page_id, position) VALUES (?, ?, ?)`,
			bundleID, pageID, i,
		); err != nil {
			return fmt.Errorf("failed to insert entry %d of favorite bundle %s: %w", i, bundleID, err)
		}
	}
	return nil
}

func queryEntryPageIDs(tx *sql.Tx, bundleID string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT page_id FROM favorite_bundle_entries WHERE bundle_id = ? ORDER BY position ASC, page_id ASC`,
		bundleID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to read bundle entries: %w", err)
	}
	defer shared.LogClose(rows.Close, logCloseRowsFailed)
	return scanPageIDs(rows)
}

func queryMembershipPageIDs(tx *sql.Tx, userID, bundleID string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT page_id FROM favorite_bundle_membership WHERE user_id = ? AND bundle_id = ? ORDER BY page_id ASC`,
		userID, bundleID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to read bundle membership: %w", err)
	}
	defer shared.LogClose(rows.Close, logCloseRowsFailed)
	return scanPageIDs(rows)
}

// queryPersonalConflictPageIDs returns the subset of pageIDs the user has
// favorited without ANY bundle owning them — the manual/personal favorites.
func queryPersonalConflictPageIDs(tx *sql.Tx, userID string, pageIDs []string) ([]string, error) {
	if len(pageIDs) == 0 {
		return nil, nil
	}
	query := `
		SELECT f.page_id FROM favorites f
		WHERE f.user_id = ? AND f.page_id IN (` + placeholders(len(pageIDs)) + `)
		  AND NOT EXISTS (
			SELECT 1 FROM favorite_bundle_membership m
			WHERE m.user_id = f.user_id AND m.page_id = f.page_id
		  )`
	args := make([]any, 0, len(pageIDs)+1)
	args = append(args, userID)
	for _, id := range pageIDs {
		args = append(args, id)
	}
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to check personal favorite conflicts: %w", err)
	}
	defer shared.LogClose(rows.Close, logCloseRowsFailed)
	return scanPageIDs(rows)
}

func scanPageIDs(rows *sql.Rows) ([]string, error) {
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func checkDuplicateEntries(pageIDs []string) error {
	seen := make(map[string]struct{}, len(pageIDs))
	for _, id := range pageIDs {
		if _, ok := seen[id]; ok {
			return fmt.Errorf("page %s listed more than once: %w", id, ErrDuplicateBundleEntry)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func dedupeInOrder(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := toSet(a)
	for _, v := range b {
		if !set[v] {
			return false
		}
	}
	return true
}

func toSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func placeholders(n int) string {
	return strings.Repeat("?,", n)[:2*n-1]
}
