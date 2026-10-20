package releasecheckpoints

import (
	"testing"
	"time"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func sampleDesired() []DesiredCheckpoint {
	return []DesiredCheckpoint{
		{VersionName: "v1", PageID: "p1", RevisionID: "r1", ContentHash: "h1", PagePath: "intro"},
		{VersionName: "v1", PageID: "p2", RevisionID: "r2", ContentHash: "h2", PagePath: "guide"},
		{VersionName: "v2", PageID: "p1", RevisionID: "r1", ContentHash: "h1", PagePath: "intro"},
	}
}

func TestStoreReconcileCreates(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)

	result, err := store.Reconcile(sampleDesired(), now)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Created != 3 || result.Removed != 0 || result.Unchanged != 0 {
		t.Fatalf("result = %+v", result)
	}

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(all))
	}
	if all[0].VersionName != "v1" || all[0].PagePath != "guide" {
		t.Fatalf("unexpected ordering/row: %+v", all[0])
	}
	for _, cp := range all {
		if !cp.CreatedAt.Equal(now) {
			t.Fatalf("createdAt = %v, want %v", cp.CreatedAt, now)
		}
	}
}

func TestStoreReconcileIdempotent(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	desired := sampleDesired()

	if _, err := store.Reconcile(desired, now); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	result, err := store.Reconcile(desired, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if result.Created != 0 || result.Removed != 0 || result.Unchanged != 3 {
		t.Fatalf("result = %+v", result)
	}

	all, _ := store.ListAll()
	for _, cp := range all {
		if !cp.CreatedAt.Equal(now) {
			t.Fatalf("createdAt moved to %v, want %v", cp.CreatedAt, now)
		}
	}
}

func TestStoreReconcileDriftRejectsAndRollsBack(t *testing.T) {
	store := newTestStore(t)
	desired := sampleDesired()
	if _, err := store.Reconcile(desired, time.Now().UTC()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	// Simulate the page having a new latest revision: same (v1, p1) key but a
	// different revision ID, while another entry would be removed.
	drifted := []DesiredCheckpoint{
		{VersionName: "v1", PageID: "p1", RevisionID: "r9", ContentHash: "h9", PagePath: "intro"},
		{VersionName: "v1", PageID: "p2", RevisionID: "r2", ContentHash: "h2", PagePath: "guide"},
		{VersionName: "v2", PageID: "p1", RevisionID: "r1", ContentHash: "h1", PagePath: "intro"},
	}
	_, err := store.Reconcile(drifted, time.Now().UTC())
	localized, ok := sharederrors.AsLocalizedError(err)
	if !ok || localized.Code != ErrCodeDrift {
		t.Fatalf("expected drift LocalizedError, got %v", err)
	}
	if len(localized.Args) != 2 || localized.Args[0] != "v1" || localized.Args[1] != "intro" {
		t.Fatalf("drift args = %v", localized.Args)
	}

	// State must be untouched: r1 still pinned, 3 rows, including v2.
	all, _ := store.ListAll()
	if len(all) != 3 {
		t.Fatalf("expected 3 rows after rejected reconcile, got %d", len(all))
	}
	protected, _ := store.ProtectedRevisionIDsForPage("p1")
	if !protected["r1"] || protected["r9"] {
		t.Fatalf("protected set after drift = %v", protected)
	}
}

func TestStoreReconcileRemovesAndRenames(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Reconcile(sampleDesired(), time.Now().UTC()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	// Rename v2 -> v3 (same content), drop p2 from v1.
	next := []DesiredCheckpoint{
		{VersionName: "v1", PageID: "p1", RevisionID: "r1", ContentHash: "h1", PagePath: "intro"},
		{VersionName: "v3", PageID: "p1", RevisionID: "r1", ContentHash: "h1", PagePath: "intro"},
	}
	result, err := store.Reconcile(next, time.Now().UTC())
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if result.Created != 1 || result.Removed != 2 || result.Unchanged != 1 {
		t.Fatalf("result = %+v", result)
	}

	names, err := store.ListRevisionNames("p1")
	if err != nil {
		t.Fatalf("ListRevisionNames: %v", err)
	}
	if got := names["r1"]; len(got) != 2 || got[0] != "v1" || got[1] != "v3" {
		t.Fatalf("p1 revision names = %v", got)
	}
	if _, ok := names["r2"]; ok {
		t.Fatal("p2 revision should no longer be listed")
	}
}

func TestStoreReconcileEmptyClearsAll(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Reconcile(sampleDesired(), time.Now().UTC()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	result, err := store.Reconcile(nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("clear Reconcile: %v", err)
	}
	if result.Removed != 3 {
		t.Fatalf("result = %+v", result)
	}
	all, _ := store.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected no rows, got %d", len(all))
	}
}

func TestStoreDeleteAllForPage(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Reconcile(sampleDesired(), time.Now().UTC()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if err := store.DeleteAllForPage("p1"); err != nil {
		t.Fatalf("DeleteAllForPage: %v", err)
	}
	all, _ := store.ListAll()
	if len(all) != 1 || all[0].PageID != "p2" {
		t.Fatalf("expected only p2 rows, got %+v", all)
	}
}

func TestStoreProtectedRevisionIDsForPage(t *testing.T) {
	store := newTestStore(t)
	desired := []DesiredCheckpoint{
		{VersionName: "v1", PageID: "p1", RevisionID: "r1", ContentHash: "h1", PagePath: "intro"},
		{VersionName: "v2", PageID: "p1", RevisionID: "r1", ContentHash: "h1", PagePath: "intro"},
		{VersionName: "v2", PageID: "p2", RevisionID: "r2", ContentHash: "h2", PagePath: "guide"},
	}
	if _, err := store.Reconcile(desired, time.Now().UTC()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	protected, err := store.ProtectedRevisionIDsForPage("p1")
	if err != nil {
		t.Fatalf("ProtectedRevisionIDsForPage: %v", err)
	}
	if len(protected) != 1 || !protected["r1"] {
		t.Fatalf("protected = %v", protected)
	}
}

func TestStorePauseForSwapBlocksQueries(t *testing.T) {
	store := newTestStore(t)
	if err := store.PauseForSwap(); err != nil {
		t.Fatalf("PauseForSwap: %v", err)
	}
	if _, err := store.ListAll(); err == nil {
		t.Fatal("expected ListAll to fail while suspended")
	}
	if err := store.PauseForSwap(); err != nil {
		t.Fatalf("second PauseForSwap should be a no-op: %v", err)
	}
}

func TestStoreReplaceReopens(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, err := store.Reconcile(sampleDesired(), time.Now().UTC()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if err := store.PauseForSwap(); err != nil {
		t.Fatalf("PauseForSwap: %v", err)
	}
	if err := store.Replace(dir); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("ListAll after Replace: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 rows after Replace, got %d", len(all))
	}
}
