// Package releasecheckpoints stores named release checkpoints: a publication
// name (e.g. "v1.0") pinned to a specific revision of each listed page.
// Unlike tags/links/properties/search, this data is not derived from the
// filesystem tree and must never be touched by resync (see ADR-0001).
package releasecheckpoints

import "time"

const (
	MaxVersionNameLen = 200
	MaxPagePathLen    = 1024
	MaxVersions       = 200
	MaxTotalEntries   = 2000
)

// Manifest is the desired release checklist submitted by an administrator.
// Pages hold route paths (e.g. "docs/intro"), not page IDs.
type Manifest struct {
	Versions []ManifestVersion
}

// ManifestVersion is one named release and the pages it pins.
type ManifestVersion struct {
	Name  string
	Pages []string
}

// DesiredCheckpoint is one validated, resolved manifest entry.
type DesiredCheckpoint struct {
	VersionName string
	PageID      string
	RevisionID  string
	ContentHash string
	PagePath    string
}

// Checkpoint is one stored checkpoint row.
type Checkpoint struct {
	VersionName string
	PageID      string
	RevisionID  string
	ContentHash string
	PagePath    string
	CreatedAt   time.Time
}

// ManifestView is the stored checkpoints grouped by version, as returned by
// GET /release-checkpoints.
type ManifestView struct {
	Name    string
	Entries []Checkpoint
}

// ReconcileResult summarizes one apply run.
type ReconcileResult struct {
	Created   int `json:"created"`
	Removed   int `json:"removed"`
	Unchanged int `json:"unchanged"`
}
