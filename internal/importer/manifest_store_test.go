package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/perber/wiki/internal/core/tree"
)

func TestValidateCollectionName(t *testing.T) {
	good := []string{"a", "default", "manual-v2", strings.Repeat("a", 64)}
	for _, name := range good {
		if err := ValidateCollectionName(name); err != nil {
			t.Errorf("ValidateCollectionName(%q) err = %v", name, err)
		}
	}
	bad := []string{"", "A", "has space", "a/b", "../x", "-lead", "trail-", "con", "nul", strings.Repeat("a", 65)}
	for _, name := range bad {
		if err := ValidateCollectionName(name); !errors.Is(err, ErrInvalidCollectionName) {
			t.Errorf("ValidateCollectionName(%q) err = %v, want ErrInvalidCollectionName", name, err)
		}
	}
}

func TestBasePathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"", "", true},
		{"", "docs", true},
		{"docs", "", true},
		{"docs", "docs", true},
		{"docs", "docs/v1", true},
		{"docs/v1", "docs", true},
		{"docs/v1", "docs/v2", false},
		{"docs", "manual", false},
		{"docs", "docs2", false}, // segment-prefix, not string-prefix
	}
	for _, tc := range cases {
		if got := basePathsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("basePathsOverlap(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCollectionStore_SaveLoadRoundTrip(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "managed"))
	now := time.Now().UTC().Truncate(time.Second)
	m := &CollectionManifest{
		SchemaVersion:  ManifestSchemaVersion,
		Collection:     "manual",
		TargetBasePath: "docs/v1",
		CreatedAt:      now,
		UpdatedAt:      now,
		Entries: map[string]ManifestEntry{
			"intro/install.md": {
				SourcePath:         "intro/install.md",
				TargetPath:         "docs/v1/intro/install",
				PageID:             "p1",
				Kind:               tree.NodeKindPage,
				Title:              "Installation",
				AppliedFingerprint: "sha256:abc",
				AppliedAt:          now,
				Generation:         1,
			},
		},
	}
	if err := store.Save(m); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Load("manual")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.TargetBasePath != "docs/v1" || len(got.Entries) != 1 {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
	if e := got.Entries["intro/install.md"]; e.PageID != "p1" || e.Kind != tree.NodeKindPage || e.AppliedFingerprint != "sha256:abc" {
		t.Fatalf("entry mismatch: %#v", e)
	}
}

func TestCollectionStore_Load_MissingAndCorrupt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "managed")
	store := NewCollectionStore(dir)

	if _, err := store.Load("ghost"); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("missing load err = %v, want ErrNoCollection", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write broken: %v", err)
	}
	if _, err := store.Load("broken"); !errors.Is(err, ErrImportStateUnavailable) {
		t.Fatalf("corrupt load err = %v, want ErrImportStateUnavailable", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "future.json"), []byte(`{"schema_version":99,"collection":"future"}`), 0o644); err != nil {
		t.Fatalf("write future: %v", err)
	}
	if _, err := store.Load("future"); !errors.Is(err, ErrUnsupportedManifestVersion) {
		t.Fatalf("future-version load err = %v, want ErrUnsupportedManifestVersion", err)
	}
	// A corrupt file never poisons the store: a repaired file loads fine.
	if err := os.WriteFile(filepath.Join(dir, "broken.json"),
		[]byte(`{"schema_version":1,"collection":"broken","entries":{}}`), 0o644); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if _, err := store.Load("broken"); err != nil {
		t.Fatalf("repaired load err = %v", err)
	}
}

func TestCollectionStore_GenerationJournal_BumpResumeFinish(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "managed"))

	m, err := store.BeginGeneration("manual", "plan-1", "docs")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if m.Generation != 1 || m.PendingPlanID() != "plan-1" || len(m.CompletedKeys) != 0 {
		t.Fatalf("new generation state = %#v", m)
	}

	entry := ManifestEntry{SourcePath: "a.md", TargetPath: "docs/a", PageID: "p1", Kind: tree.NodeKindPage, Title: "A", AppliedFingerprint: "sha256:1"}
	if err := store.MarkCompleted(m, "a.md", entry); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	if !m.IsCompleted("a.md") || m.Entries["a.md"].Generation != 1 {
		t.Fatalf("completed state = %#v", m)
	}

	// Same planID = crash resume: journal retained, generation not bumped.
	resumed, err := store.BeginGeneration("manual", "plan-1", "docs")
	if err != nil {
		t.Fatalf("resume begin: %v", err)
	}
	if resumed.Generation != 1 || !resumed.IsCompleted("a.md") {
		t.Fatalf("resume must retain journal: gen=%d completed=%v", resumed.Generation, resumed.CompletedKeys)
	}

	// Different planID = fresh execution: generation bumped, journal cleared,
	// entries from prior generations retained.
	next, err := store.BeginGeneration("manual", "plan-2", "docs")
	if err != nil {
		t.Fatalf("next begin: %v", err)
	}
	if next.Generation != 2 || len(next.CompletedKeys) != 0 {
		t.Fatalf("fresh generation state = gen %d completed %v", next.Generation, next.CompletedKeys)
	}
	if _, ok := next.Entries["a.md"]; !ok {
		t.Fatalf("prior entries must survive a new generation: %#v", next.Entries)
	}

	// Delete item, then finish.
	if err := store.MarkDeleted(next, "a.md"); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	if _, ok := next.Entries["a.md"]; ok {
		t.Fatalf("entry still present after delete")
	}
	if err := store.MarkDeleted(next, "a.md"); err != nil {
		t.Fatalf("double delete must be idempotent: %v", err)
	}
	if err := store.FinishGeneration(next); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if next.PendingPlanID() != "" || next.CompletedKeys != nil {
		t.Fatalf("journal not cleared: pending=%q completed=%v", next.PendingPlanID(), next.CompletedKeys)
	}

	// State survived on disk.
	reloaded, err := store.Load("manual")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Generation != 2 || len(reloaded.Entries) != 0 || reloaded.PendingPlanID() != "" {
		t.Fatalf("reloaded state = %#v", reloaded)
	}
}

func TestCollectionStore_BeginGeneration_BasePathPinned(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "managed"))
	if _, err := store.BeginGeneration("manual", "plan-1", "docs"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Same base (different surrounding slashes/space) is fine.
	if _, err := store.BeginGeneration("manual", "plan-2", " /docs/ "); err != nil {
		t.Fatalf("same normalized base must be allowed: %v", err)
	}
	if _, err := store.BeginGeneration("manual", "plan-3", "manual"); !errors.Is(err, ErrManagedBasePathMismatch) {
		t.Fatalf("mismatch err = %v, want ErrManagedBasePathMismatch", err)
	}
}

func TestCollectionStore_EnsureBasePathAvailable(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "managed"))
	if err := store.EnsureBasePathAvailable("a", "docs"); err != nil {
		t.Fatalf("first collection: %v", err)
	}
	// No manifest exists yet, so planning another collection still passes.
	if err := store.EnsureBasePathAvailable("b", "manual"); err != nil {
		t.Fatalf("disjoint collection before first run: %v", err)
	}

	mustBegin := func(name, base string) {
		t.Helper()
		m, err := store.BeginGeneration(name, "plan-"+name, base)
		if err != nil {
			t.Fatalf("begin %q: %v", name, err)
		}
		if err := store.FinishGeneration(m); err != nil {
			t.Fatalf("finish %q: %v", name, err)
		}
	}
	mustBegin("a", "docs")

	if err := store.EnsureBasePathAvailable("a", "docs"); err != nil {
		t.Errorf("reuse own base: %v", err)
	}
	if err := store.EnsureBasePathAvailable("b", "manual"); err != nil {
		t.Errorf("disjoint base: %v", err)
	}
	if err := store.EnsureBasePathAvailable("b", "docs/v2"); !errors.Is(err, ErrManagedBasePathOverlap) {
		t.Errorf("nested base err = %v, want ErrManagedBasePathOverlap", err)
	}
	if err := store.EnsureBasePathAvailable("root", ""); !errors.Is(err, ErrManagedBasePathOverlap) {
		t.Errorf("root base err = %v, want ErrManagedBasePathOverlap", err)
	}
}

func TestCollectionStore_LoadAll_PartialCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "managed")
	store := NewCollectionStore(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good.json"),
		[]byte(`{"schema_version":1,"collection":"good","entries":{}}`), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	all, errs := store.LoadAll()
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want 1", errs)
	}
	if len(all) != 1 || all["good"] == nil {
		t.Fatalf("all = %#v, want only good loaded", all)
	}
}

func TestCollectionStore_InvalidNameRejectedBeforePathUse(t *testing.T) {
	store := NewCollectionStore(filepath.Join(t.TempDir(), "managed"))
	if _, err := store.Load("../escape"); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("load invalid err = %v", err)
	}
	if err := store.Save(&CollectionManifest{SchemaVersion: ManifestSchemaVersion, Collection: "../escape"}); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("save invalid err = %v", err)
	}
	if _, err := store.BeginGeneration("../escape", "p", ""); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("begin invalid err = %v", err)
	}
}
