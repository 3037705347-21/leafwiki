package favorites

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func mustCreateBundle(t *testing.T, store *FavoritesStore, name string, pageIDs []string) Bundle {
	t.Helper()
	bundle, err := store.CreateBundle(name, "desc for "+name, pageIDs, "admin-1")
	if err != nil {
		t.Fatalf("CreateBundle %q: %v", name, err)
	}
	return bundle
}

// favoriteCount returns how many favorite rows exist for (userID, pageID).
func favoriteExists(t *testing.T, store *FavoritesStore, userID, pageID string) bool {
	t.Helper()
	var exists bool
	err := store.withDB(func(db *sql.DB) error {
		return db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM favorites WHERE user_id = ? AND page_id = ?)`,
			userID, pageID,
		).Scan(&exists)
	})
	if err != nil {
		t.Fatalf("favoriteExists(%s,%s): %v", userID, pageID, err)
	}
	return exists
}

func membershipExists(t *testing.T, store *FavoritesStore, userID, bundleID, pageID string) bool {
	t.Helper()
	var exists bool
	err := store.withDB(func(db *sql.DB) error {
		return db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM favorite_bundle_membership WHERE user_id = ? AND bundle_id = ? AND page_id = ?)`,
			userID, bundleID, pageID,
		).Scan(&exists)
	})
	if err != nil {
		t.Fatalf("membershipExists: %v", err)
	}
	return exists
}

func subscriptionExists(t *testing.T, store *FavoritesStore, userID, bundleID string) bool {
	t.Helper()
	var exists bool
	err := store.withDB(func(db *sql.DB) error {
		return db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM favorite_bundle_subscriptions WHERE user_id = ? AND bundle_id = ?)`,
			userID, bundleID,
		).Scan(&exists)
	})
	if err != nil {
		t.Fatalf("subscriptionExists: %v", err)
	}
	return exists
}

func tableCount(t *testing.T, store *FavoritesStore, table string) int {
	t.Helper()
	var n int
	err := store.withDB(func(db *sql.DB) error {
		return db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&n) //nolint:gosec // test-only fixed table names
	})
	if err != nil {
		t.Fatalf("tableCount(%s): %v", table, err)
	}
	return n
}

func requireFavorites(t *testing.T, store *FavoritesStore, userID string, want []string) {
	t.Helper()
	got, err := store.ListPageIDsForUser(userID)
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("favorites for %s = %v, want %v", userID, got, want)
	}
	wantSet := toSet(want)
	for _, id := range got {
		if !wantSet[id] {
			t.Fatalf("unexpected favorite %q for %s; got %v want %v", id, userID, got, want)
		}
	}
}

// ─── bundle CRUD ──────────────────────────────────────────────────────────────

func TestBundles_CreateAndGet_PreservesEntryOrder(t *testing.T) {
	store := newTestStore(t)

	bundle := mustCreateBundle(t, store, "team reading", []string{"p3", "p1", "p2"})

	got, entries, err := store.GetBundle(bundle.ID)
	if err != nil {
		t.Fatalf("GetBundle: %v", err)
	}
	if got.Name != "team reading" || got.CreatedBy != "admin-1" {
		t.Fatalf("unexpected bundle: %+v", got)
	}
	gotIDs := make([]string, len(entries))
	for i, e := range entries {
		if e.Position != i {
			t.Fatalf("entry %d position = %d", i, e.Position)
		}
		gotIDs[i] = e.PageID
	}
	if len(gotIDs) != 3 || gotIDs[0] != "p3" || gotIDs[1] != "p1" || gotIDs[2] != "p2" {
		t.Fatalf("entries not in submitted order: %v", gotIDs)
	}
}

func TestBundles_Create_DuplicateEntriesRejectedAndNothingWritten(t *testing.T) {
	store := newTestStore(t)

	_, err := store.CreateBundle("dup", "", []string{"p1", "p2", "p1"}, "admin-1")
	if !errors.Is(err, ErrDuplicateBundleEntry) {
		t.Fatalf("want ErrDuplicateBundleEntry, got %v", err)
	}
	if tableCount(t, store, "favorite_bundles") != 0 {
		t.Fatal("rejected create must not leave a bundle row")
	}
	if tableCount(t, store, "favorite_bundle_entries") != 0 {
		t.Fatal("rejected create must not leave entry rows")
	}
}

func TestBundles_List_NewestFirst(t *testing.T) {
	store := newTestStore(t)
	first := mustCreateBundle(t, store, "first", nil)
	time.Sleep(2 * time.Millisecond)
	second := mustCreateBundle(t, store, "second", nil)

	bundles, err := store.ListBundles()
	if err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	if len(bundles) != 2 || bundles[0].ID != second.ID || bundles[1].ID != first.ID {
		t.Fatalf("want newest first, got %+v", bundles)
	}
}

func TestBundles_Update_ReplacesEntries(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})

	if err := store.UpdateBundle(bundle.ID, "team v2", "new desc", []string{"p2", "p3"}); err != nil {
		t.Fatalf("UpdateBundle: %v", err)
	}
	got, entries, err := store.GetBundle(bundle.ID)
	if err != nil {
		t.Fatalf("GetBundle: %v", err)
	}
	if got.Name != "team v2" || got.Description != "new desc" {
		t.Fatalf("update not reflected: %+v", got)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.PageID)
	}
	if len(ids) != 2 || ids[0] != "p2" || ids[1] != "p3" {
		t.Fatalf("entries not replaced: %v", ids)
	}
}

func TestBundles_Update_MissingAndDuplicate(t *testing.T) {
	store := newTestStore(t)

	if err := store.UpdateBundle("nope", "x", "", nil); !errors.Is(err, ErrBundleNotFound) {
		t.Fatalf("want ErrBundleNotFound, got %v", err)
	}
	bundle := mustCreateBundle(t, store, "team", []string{"p1"})
	if err := store.UpdateBundle(bundle.ID, "team", "", []string{"p1", "p1"}); !errors.Is(err, ErrDuplicateBundleEntry) {
		t.Fatalf("want ErrDuplicateBundleEntry, got %v", err)
	}
}

// ─── sync / reconcile ─────────────────────────────────────────────────────────

func TestBundles_Sync_FirstSyncAddsFavoritesMembershipSubscription(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})

	res, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Added) != 2 || len(res.Removed) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	requireFavorites(t, store, "user-1", []string{"p1", "p2"})
	if !membershipExists(t, store, "user-1", bundle.ID, "p1") ||
		!membershipExists(t, store, "user-1", bundle.ID, "p2") {
		t.Fatal("membership rows missing")
	}
	if !subscriptionExists(t, store, "user-1", bundle.ID) {
		t.Fatal("subscription row missing")
	}

	states, err := store.ListSubscriptionStates("user-1")
	if err != nil {
		t.Fatalf("ListSubscriptionStates: %v", err)
	}
	sub, ok := states[bundle.ID]
	if !ok || sub.LastSyncedAt == nil {
		t.Fatalf("subscription state missing lastSyncedAt: %+v", states)
	}
}

func TestBundles_Sync_IsIdempotent(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})

	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	res, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p2", "p1"}) // reordered
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Fatalf("idempotent sync must change nothing, got %+v", res)
	}
	if tableCount(t, store, "favorites") != 2 ||
		tableCount(t, store, "favorite_bundle_membership") != 2 ||
		tableCount(t, store, "favorite_bundle_subscriptions") != 1 {
		t.Fatal("idempotent sync created duplicate rows")
	}
}

func TestBundles_Sync_PersonalFavoriteConflictRollsBackEverything(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})
	// p2 is already a MANUAL personal favorite (no membership anywhere).
	if err := store.Add("user-1", "p2"); err != nil {
		t.Fatalf("Add personal favorite: %v", err)
	}

	_, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"})
	if !errors.Is(err, ErrPersonalFavoriteConflict) {
		t.Fatalf("want ErrPersonalFavoriteConflict, got %v", err)
	}

	// Whole sync rejected: no favorite, membership or subscription rows
	// beyond the pre-existing personal favorite.
	requireFavorites(t, store, "user-1", []string{"p2"})
	if tableCount(t, store, "favorite_bundle_membership") != 0 {
		t.Fatal("conflict rollback left membership rows")
	}
	if tableCount(t, store, "favorite_bundle_subscriptions") != 0 {
		t.Fatal("conflict rollback left a subscription row")
	}
}

func TestBundles_Sync_ReconcilesAddsAndRemoves_LeavingPersonalFavorites(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})
	// A personal favorite that is not part of the bundle.
	if err := store.Add("user-1", "personal"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	// Admin drops p2 and adds p3; member re-syncs.
	if err := store.UpdateBundle(bundle.ID, "team", "", []string{"p1", "p3"}); err != nil {
		t.Fatalf("UpdateBundle: %v", err)
	}
	res, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p3"})
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0] != "p3" {
		t.Fatalf("want only p3 added, got %+v", res)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "p2" {
		t.Fatalf("want only p2 removed, got %+v", res)
	}
	// p2 gone (bundle-owned), personal page and bundle pages remain.
	requireFavorites(t, store, "user-1", []string{"p1", "p3", "personal"})
}

func TestBundles_Sync_RefavoriteManuallyRemovedBundlePage(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1"})
	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1"}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// User un-favorites p1 with the normal personal-favorite API; the
	// membership row still says the bundle owns it.
	if err := store.Remove("user-1", "p1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if favoriteExists(t, store, "user-1", "p1") {
		t.Fatal("favorite should be removed")
	}

	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1"}); err != nil {
		t.Fatalf("re-reconcile: %v", err)
	}
	if !favoriteExists(t, store, "user-1", "p1") {
		t.Fatal("re-sync must re-add the bundle page the user manually removed")
	}
}

func TestBundles_Sync_ModifiedBundleRejected(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})

	// Use case validated [p1 p2], but entries have since changed.
	if err := store.UpdateBundle(bundle.ID, "team", "", []string{"p1"}); err != nil {
		t.Fatalf("UpdateBundle: %v", err)
	}
	_, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"})
	if !errors.Is(err, ErrBundleModified) {
		t.Fatalf("want ErrBundleModified, got %v", err)
	}
	if tableCount(t, store, "favorite_bundle_subscriptions") != 0 {
		t.Fatal("modified-bundle rejection must not create a subscription")
	}
}

func TestBundles_Sync_MissingBundle(t *testing.T) {
	store := newTestStore(t)
	_, err := store.ReconcileBundleMembership("user-1", "ghost", nil)
	if !errors.Is(err, ErrBundleNotFound) {
		t.Fatalf("want ErrBundleNotFound, got %v", err)
	}
}

func TestBundles_Sync_EmptyBundleDoesNotConflict(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "empty", nil)
	if err := store.Add("user-1", "personal"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	res, err := store.ReconcileBundleMembership("user-1", bundle.ID, nil)
	if err != nil {
		t.Fatalf("empty reconcile: %v", err)
	}
	if len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Fatalf("empty sync must change nothing: %+v", res)
	}
	requireFavorites(t, store, "user-1", []string{"personal"})
}

// ─── unsubscribe ──────────────────────────────────────────────────────────────

func TestBundles_Unsubscribe_RemovesOnlyBundleOwnedFavorites(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})
	if err := store.Add("user-1", "personal"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if err := store.Unsubscribe("user-1", bundle.ID); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	requireFavorites(t, store, "user-1", []string{"personal"})
	if tableCount(t, store, "favorite_bundle_membership") != 0 {
		t.Fatal("unsubscribe left membership rows")
	}
	if subscriptionExists(t, store, "user-1", bundle.ID) {
		t.Fatal("unsubscribe left subscription row")
	}

	// Idempotent.
	if err := store.Unsubscribe("user-1", bundle.ID); err != nil {
		t.Fatalf("second Unsubscribe: %v", err)
	}
}

func TestBundles_Unsubscribe_KeepsFavoritesAnotherBundleStillOwns(t *testing.T) {
	store := newTestStore(t)
	a := mustCreateBundle(t, store, "A", []string{"shared"})
	b := mustCreateBundle(t, store, "B", []string{"shared"})

	if _, err := store.ReconcileBundleMembership("user-1", a.ID, []string{"shared"}); err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if _, err := store.ReconcileBundleMembership("user-1", b.ID, []string{"shared"}); err != nil {
		t.Fatalf("sync B: %v", err)
	}

	// Leaving A must keep the page because B still owns it.
	if err := store.Unsubscribe("user-1", a.ID); err != nil {
		t.Fatalf("Unsubscribe A: %v", err)
	}
	if !favoriteExists(t, store, "user-1", "shared") {
		t.Fatal("page must stay favorited while bundle B still owns it")
	}
	if !subscriptionExists(t, store, "user-1", b.ID) {
		t.Fatal("subscription to B must remain")
	}
	// Leaving B as well finally removes the favorite.
	if err := store.Unsubscribe("user-1", b.ID); err != nil {
		t.Fatalf("Unsubscribe B: %v", err)
	}
	if favoriteExists(t, store, "user-1", "shared") {
		t.Fatal("page must be unfavorited once no bundle owns it")
	}
}

// ─── delete bundle ────────────────────────────────────────────────────────────

func TestBundles_DeleteBundle_KeepsFavoritesAsPersonal(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1"})
	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1"}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if err := store.DeleteBundle(bundle.ID); err != nil {
		t.Fatalf("DeleteBundle: %v", err)
	}
	if !favoriteExists(t, store, "user-1", "p1") {
		t.Fatal("deleting a bundle must leave its favorites in place as personal favorites")
	}
	if tableCount(t, store, "favorite_bundles") != 0 ||
		tableCount(t, store, "favorite_bundle_entries") != 0 ||
		tableCount(t, store, "favorite_bundle_membership") != 0 ||
		tableCount(t, store, "favorite_bundle_subscriptions") != 0 {
		t.Fatal("delete bundle left definition/provenance rows")
	}
	if err := store.DeleteBundle(bundle.ID); !errors.Is(err, ErrBundleNotFound) {
		t.Fatalf("second delete want ErrBundleNotFound, got %v", err)
	}
}

// ─── cascades ─────────────────────────────────────────────────────────────────

func TestBundles_DeleteAllForPage_ClearsEntriesMembershipAndFavorites(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})
	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if err := store.DeleteAllForPage("p1"); err != nil {
		t.Fatalf("DeleteAllForPage: %v", err)
	}
	if favoriteExists(t, store, "user-1", "p1") || membershipExists(t, store, "user-1", bundle.ID, "p1") {
		t.Fatal("page cascade left favorite/membership rows")
	}
	entries, err := store.ListEntries(bundle.ID)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].PageID != "p2" {
		t.Fatalf("page cascade must remove the bundle entry, got %+v", entries)
	}
	requireFavorites(t, store, "user-1", []string{"p2"})
}

func TestBundles_DeleteAllForUser_ClearsSubscriptionsAndMembership(t *testing.T) {
	store := newTestStore(t)
	bundle := mustCreateBundle(t, store, "team", []string{"p1"})
	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1"}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := store.ReconcileBundleMembership("user-2", bundle.ID, []string{"p1"}); err != nil {
		t.Fatalf("reconcile user-2: %v", err)
	}

	if err := store.DeleteAllForUser("user-1"); err != nil {
		t.Fatalf("DeleteAllForUser: %v", err)
	}
	if subscriptionExists(t, store, "user-1", bundle.ID) ||
		tableCount(t, store, "favorite_bundle_membership") != 1 {
		t.Fatal("user cascade left user-1 rows")
	}
	// Bundle and other users survive.
	if !subscriptionExists(t, store, "user-2", bundle.ID) {
		t.Fatal("user-2 subscription must survive")
	}
}

// ─── persistence and old-database upgrade ─────────────────────────────────────

func TestBundles_ReopenStore_PreservesSubscriptionsAndMembership(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFavoritesStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFavoritesStore: %v", err)
	}
	bundle := mustCreateBundle(t, store, "team", []string{"p1", "p2"})
	if _, err := store.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewFavoritesStore(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	requireFavorites(t, reopened, "user-1", []string{"p1", "p2"})
	if !subscriptionExists(t, reopened, "user-1", bundle.ID) {
		t.Fatal("subscription lost across restart")
	}
	if !membershipExists(t, reopened, "user-1", bundle.ID, "p1") {
		t.Fatal("membership lost across restart")
	}
	// A second sync after restart stays idempotent.
	res, err := reopened.ReconcileBundleMembership("user-1", bundle.ID, []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("post-restart reconcile: %v", err)
	}
	if len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Fatalf("post-restart sync not idempotent: %+v", res)
	}
}

// A favorites.db created before bundles shipped (only the favorites table)
// must gain the new tables automatically on open, and old favorite rows are
// treated as personal favorites by the conflict check.
func TestBundles_OpenOldDatabase_AddsTablesAndKeepsOldFavorites(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "favorites.db")

	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE favorites (
			user_id TEXT NOT NULL, page_id TEXT NOT NULL, created_at TIMESTAMP NOT NULL,
			PRIMARY KEY (user_id, page_id)
		)`); err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO favorites (user_id, page_id, created_at) VALUES (?, ?, ?)`,
		"user-1", "old-page", time.Now().UTC(),
	); err != nil {
		t.Fatalf("seed old favorite: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewFavoritesStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFavoritesStore over old db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	requireFavorites(t, store, "user-1", []string{"old-page"})
	bundle := mustCreateBundle(t, store, "team", []string{"old-page"})
	_, err = store.ReconcileBundleMembership("user-1", bundle.ID, []string{"old-page"})
	if !errors.Is(err, ErrPersonalFavoriteConflict) {
		t.Fatalf("old favorite rows must count as personal favorites, got %v", err)
	}
}
