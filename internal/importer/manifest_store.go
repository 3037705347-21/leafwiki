package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/perber/wiki/internal/core/shared"
	"github.com/perber/wiki/internal/core/tree"
)

// ManifestSchemaVersion is the on-disk schema version of CollectionManifest.
const ManifestSchemaVersion = 1

const (
	// maxCollectionNameLen bounds the manifest filename length on every OS.
	maxCollectionNameLen = 64
	manifestFileSuffix   = ".json"
	manifestDirPerm      = 0o755
	manifestFilePerm     = 0o644
)

var (
	// ErrNoCollection signals that no manifest exists for the collection yet.
	ErrNoCollection = errors.New("managed collection does not exist")
	// ErrInvalidCollectionName signals a collection name outside the
	// [a-z0-9-] charset/length rules (also the path-traversal guard).
	ErrInvalidCollectionName = errors.New("invalid managed collection name")
	// ErrManagedBasePathMismatch signals that a collection was reused with a
	// target base path different from the one it is pinned to.
	ErrManagedBasePathMismatch = errors.New("managed collection target base path mismatch")
	// ErrManagedBasePathOverlap signals that two collections pin target base
	// paths where one path-prefix-contains the other.
	ErrManagedBasePathOverlap = errors.New("managed collection target base paths overlap")
	// ErrUnsupportedManifestVersion signals an unknown manifest schema.
	ErrUnsupportedManifestVersion = errors.New("unsupported managed collection manifest version")
)

// collectionNamePattern mirrors the safe subset of tree slugs but is
// lowercase-only so manifest filenames never collide on case-insensitive
// filesystems (Windows/macOS).
var collectionNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// windowsReservedBaseNames protects manifest file creation on Windows where
// these device names are reserved regardless of extension.
var windowsReservedBaseNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true,
	"com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true,
	"lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateCollectionName enforces the collection naming rules used both for
// user input and for manifest filenames read back from disk.
func ValidateCollectionName(name string) error {
	if len(name) == 0 || len(name) > maxCollectionNameLen {
		return fmt.Errorf("%w: %q", ErrInvalidCollectionName, name)
	}
	if !collectionNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidCollectionName, name)
	}
	if windowsReservedBaseNames[name] {
		return fmt.Errorf("%w: %q", ErrInvalidCollectionName, name)
	}
	return nil
}

// NormalizeTargetBasePath canonicalizes a user-provided target base path to
// the stored form: trimmed of surrounding whitespace and slashes.
func NormalizeTargetBasePath(p string) string {
	return strings.Trim(strings.TrimSpace(p), "/")
}

// ManifestEntry records that one source file of a collection was applied to
// one wiki page, together with the fingerprint of the applied content. The
// manifest is the only place that records collection ownership: page files
// stay free of any import markers so UI-originated edits cannot wipe it.
type ManifestEntry struct {
	SourcePath         string        `json:"source_path"`
	TargetPath         string        `json:"target_path"`
	PageID             string        `json:"page_id"`
	Kind               tree.NodeKind `json:"kind"`
	Title              string        `json:"title"`
	AppliedFingerprint string        `json:"applied_fingerprint"`
	AppliedAt          time.Time     `json:"applied_at"`
	Generation         int64         `json:"generation"`
}

// CollectionManifest is the persisted reconciliation state for one named
// collection, rooted at one target base path.
type CollectionManifest struct {
	SchemaVersion  int       `json:"schema_version"`
	Collection     string    `json:"collection"`
	TargetBasePath string    `json:"target_base_path"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastPlanID     string    `json:"last_plan_id,omitempty"`
	// Generation is bumped each time an execution starts; entries stamped
	// with it were applied by the latest (possibly resumed) execution.
	Generation int64 `json:"generation"`
	// PendingPlanID is non-empty while an execution may still be running.
	// It makes BeginGeneration idempotent across crash-resume retries of the
	// same plan; CompletedKeys is the per-item completion journal.
	CompletedKeys []string                 `json:"completed_keys,omitempty"`
	Entries       map[string]ManifestEntry `json:"entries"`
}

// CollectionStore persists collection manifests under
// <storageDir>/.importer/managed/<collection>.json. Files are written
// atomically (temp file + rename), mirroring PlanStore. Unlike PlanStore, a
// corrupt file never poisons the process: the error is returned per request
// and repairing the file makes the next request succeed.
type CollectionStore struct {
	mu         sync.RWMutex
	managedDir string
}

// NewCollectionStore constructs a store rooted at managedDir. The directory
// is created lazily on first write.
func NewCollectionStore(managedDir string) *CollectionStore {
	return &CollectionStore{managedDir: managedDir}
}

func (s *CollectionStore) pathFor(name string) (string, error) {
	if err := ValidateCollectionName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.managedDir, name+manifestFileSuffix), nil
}

// Load reads one collection manifest. A missing file returns ErrNoCollection;
// a corrupt or unsupported file returns an error wrapping
// ErrImportStateUnavailable.
func (s *CollectionStore) Load(name string) (*CollectionManifest, error) {
	path, err := s.pathFor(name)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	raw, readErr := os.ReadFile(path)
	s.mu.RUnlock()
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return nil, ErrNoCollection
		}
		return nil, fmt.Errorf("read managed collection %q: %w", name, readErr)
	}
	return parseManifest(name, raw)
}

// Save validates and atomically writes the manifest.
func (s *CollectionStore) Save(m *CollectionManifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrImportStateUnavailable)
	}
	if err := ValidateCollectionName(m.Collection); err != nil {
		return err
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("%w: %w: version %d", ErrImportStateUnavailable, ErrUnsupportedManifestVersion, m.SchemaVersion)
	}
	path, err := s.pathFor(m.Collection)
	if err != nil {
		return err
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal managed collection %q: %w", m.Collection, err)
	}
	raw = append(raw, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.managedDir, manifestDirPerm); err != nil {
		return fmt.Errorf("create managed collection dir: %w", err)
	}
	if err := shared.WriteFileAtomic(path, raw, manifestFilePerm); err != nil {
		return fmt.Errorf("write managed collection %q: %w", m.Collection, err)
	}
	return nil
}

// LoadAll returns every persisted collection manifest. Per-file parse errors
// are returned alongside successfully loaded manifests and never abort the
// scan (callers decide whether one corrupt file blocks planning).
func (s *CollectionStore) LoadAll() (map[string]*CollectionManifest, []error) {
	s.mu.RLock()
	entries, err := os.ReadDir(s.managedDir)
	s.mu.RUnlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]*CollectionManifest{}, nil
		}
		return nil, []error{fmt.Errorf("list managed collections: %w", err)}
	}

	out := make(map[string]*CollectionManifest)
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), manifestFileSuffix) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), manifestFileSuffix)
		m, loadErr := s.Load(name)
		if loadErr != nil {
			errs = append(errs, loadErr)
			continue
		}
		out[name] = m
	}
	return out, errs
}

// BeginGeneration starts (or resumes, when called again with the same planID)
// the execution journal for a collection. A brand-new collection is pinned to
// targetBasePath; an existing one rejects a different base path.
func (s *CollectionStore) BeginGeneration(name string, planID string, targetBasePath string) (*CollectionManifest, error) {
	base := NormalizeTargetBasePath(targetBasePath)

	m, err := s.Load(name)
	switch {
	case err == nil:
		if m.TargetBasePath != base {
			return nil, fmt.Errorf("%w: collection %q pinned to %q, got %q",
				ErrManagedBasePathMismatch, name, m.TargetBasePath, base)
		}
		if m.PendingPlanID() == planID && planID != "" {
			// Crash resume of the same execution: keep the completion journal.
			return m, nil
		}
	case errors.Is(err, ErrNoCollection):
		now := time.Now().UTC()
		m = &CollectionManifest{
			SchemaVersion:  ManifestSchemaVersion,
			Collection:     name,
			TargetBasePath: base,
			CreatedAt:      now,
			Entries:        map[string]ManifestEntry{},
		}
	default:
		return nil, err
	}

	m.Generation++
	m.LastPlanID = planID
	m.CompletedKeys = []string{}
	m.UpdatedAt = time.Now().UTC()
	if err := s.Save(m); err != nil {
		return nil, err
	}
	return m, nil
}

// MarkCompleted records a successfully applied item and its journal key.
func (s *CollectionStore) MarkCompleted(m *CollectionManifest, key string, entry ManifestEntry) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrImportStateUnavailable)
	}
	if m.Entries == nil {
		m.Entries = map[string]ManifestEntry{}
	}
	entry.Generation = m.Generation
	m.Entries[key] = entry
	m.CompletedKeys = appendUnique(m.CompletedKeys, key)
	m.UpdatedAt = time.Now().UTC()
	return s.Save(m)
}

// MarkDeleted removes the entry for a successfully deleted item and records
// its journal key. Removing an already-absent entry is not an error (crash
// resume after the disk delete but before the journal write).
func (s *CollectionStore) MarkDeleted(m *CollectionManifest, key string) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrImportStateUnavailable)
	}
	_, existed := m.Entries[key]
	delete(m.Entries, key)
	m.CompletedKeys = appendUnique(m.CompletedKeys, key)
	if existed {
		m.UpdatedAt = time.Now().UTC()
	}
	return s.Save(m)
}

// FinishGeneration closes the execution journal.
func (s *CollectionStore) FinishGeneration(m *CollectionManifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrImportStateUnavailable)
	}
	m.CompletedKeys = nil
	m.LastPlanID = ""
	m.UpdatedAt = time.Now().UTC()
	return s.Save(m)
}

// PendingPlanID returns the plan currently owning the execution journal.
func (m *CollectionManifest) PendingPlanID() string {
	if m == nil {
		return ""
	}
	return m.LastPlanID
}

// IsCompleted reports whether key was already applied in the running
// generation (used by the executor to skip journaled items on resume).
func (m *CollectionManifest) IsCompleted(key string) bool {
	if m == nil {
		return false
	}
	for _, k := range m.CompletedKeys {
		if k == key {
			return true
		}
	}
	return false
}

// EnsureBasePathAvailable verifies at plan time that targetBasePath for name
// does not overlap another collection's pinned base. Reusing the collection's
// own base is allowed. The empty base (wiki root) overlaps every other base.
func (s *CollectionStore) EnsureBasePathAvailable(name string, targetBasePath string) error {
	base := NormalizeTargetBasePath(targetBasePath)
	others, errs := s.LoadAll()
	if len(errs) > 0 {
		return fmt.Errorf("%w: cannot read managed collections: %w", ErrImportStateUnavailable, errs[0])
	}
	for otherName, other := range others {
		if otherName == name {
			continue
		}
		if basePathsOverlap(base, other.TargetBasePath) {
			return fmt.Errorf("%w: %q (%q) overlaps collection %q (%q)",
				ErrManagedBasePathOverlap, name, base, otherName, other.TargetBasePath)
		}
	}
	return nil
}

// basePathsOverlap reports whether two normalized base paths are equal or one
// contains the other by path segments.
func basePathsOverlap(a, b string) bool {
	segsA := strings.FieldsFunc(a, func(r rune) bool { return r == '/' })
	segsB := strings.FieldsFunc(b, func(r rune) bool { return r == '/' })
	if len(segsA) > len(segsB) {
		segsA, segsB = segsB, segsA
	}
	if len(segsA) == 0 {
		return true // empty base is the wiki root and contains everything
	}
	if len(segsA) == len(segsB) {
		return strings.EqualFold(strings.Join(segsA, "/"), strings.Join(segsB, "/"))
	}
	for i, seg := range segsA {
		if !strings.EqualFold(seg, segsB[i]) {
			return false
		}
	}
	return true
}

func appendUnique(values []string, v string) []string {
	for _, existing := range values {
		if existing == v {
			return values
		}
	}
	return append(values, v)
}

func parseManifest(name string, raw []byte) (*CollectionManifest, error) {
	var m CollectionManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: parse managed collection %q: %w", ErrImportStateUnavailable, name, err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return nil, fmt.Errorf("%w: %w: collection %q version %d",
			ErrImportStateUnavailable, ErrUnsupportedManifestVersion, name, m.SchemaVersion)
	}
	if m.Collection == "" {
		m.Collection = name
	}
	if m.Entries == nil {
		m.Entries = map[string]ManifestEntry{}
	}
	return &m, nil
}
