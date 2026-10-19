package importer

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/perber/wiki/internal/core/ignore"
	"github.com/perber/wiki/internal/core/markdown"
	"github.com/perber/wiki/internal/core/shared"
	"github.com/perber/wiki/internal/core/tree"
)

type PlanAction string

const (
	PlanActionCreate PlanAction = "create" // creates new node
	PlanActionUpdate PlanAction = "update" // updates existing node
	PlanActionSkip   PlanAction = "skip"   // skips existing node
	PlanActionDelete PlanAction = "delete" // managed mode: source file removed
)

// ImportMDFile represents a markdown file to be imported
type ImportMDFile struct {
	SourcePath string // relative path to the markdown file in the zip directory
}

// PlanItem represents a single item in the import plan
type PlanItem struct {
	SourcePath  string        `json:"source_path"`
	TargetPath  string        `json:"target_path"`
	Title       string        `json:"title"`
	DesiredSlug string        `json:"desired_slug"`
	Kind        tree.NodeKind `json:"kind"`
	Exists      bool          `json:"exists"`
	ExistingID  *string       `json:"existing_id"`

	Action    PlanAction `json:"action"`
	Conflicts []string   `json:"conflicts"`
	Notes     []string   `json:"notes"`

	// DesiredFingerprint is the content fingerprint managed mode expects
	// after execution; empty for legacy plans.
	DesiredFingerprint string `json:"desired_fingerprint,omitempty"`
}

// PlanOptions represents options for creating an import plan
type PlanOptions struct {
	SourceBasePath string     `json:"source_base_path"`     // base path in the import source
	TargetBasePath string     `json:"target_base_path"`     // base path in the wiki where to import
	Mode           ImportMode `json:"mode,omitempty"`       // "" == legacy (one-shot create/skip)
	Collection     string     `json:"collection,omitempty"` // managed collection name (ModeManaged only)
}

// PlanResult represents the result of the import plan
type PlanResult struct {
	ID            string     `json:"id"`
	TreeHash      string     `json:"tree_hash"` // hash of the state of the wiki tree before import
	Items         []PlanItem `json:"items"`
	Errors        []string   `json:"errors"`
	Blocked       bool       `json:"blocked,omitempty"`        // true when at least one item has a blocking conflict; execution is refused
	ConflictCount int        `json:"conflict_count,omitempty"` // number of items carrying blocking conflicts
}

// Planner is responsible for creating an import plan
type Planner struct {
	log         *slog.Logger
	wiki        ImporterWiki
	slugger     *tree.SlugService
	storageDir  string
	ignoreCache *ignore.Cache
}

// NewPlanner creates a new Planner
func NewPlanner(wiki ImporterWiki, slugger *tree.SlugService, storageDir string) *Planner {
	return &Planner{
		log:        slog.Default().With("component", "Planner"),
		wiki:       wiki,
		slugger:    slugger,
		storageDir: storageDir,
	}
}

// SetIgnoreCache sets the ignore cache for filtering import targets.
func (p *Planner) SetIgnoreCache(ignoreCache *ignore.Cache) {
	p.ignoreCache = ignoreCache
}

// getIgnoreForDir returns the compiled ignore rules for the given directory.
func (p *Planner) getIgnoreForDir(dir string) *ignore.IgnoreFile {
	if p.ignoreCache == nil {
		return nil
	}
	return p.ignoreCache.Get(dir)
}

// CreatePlan creates an import plan based on the provided entries and options
func (p *Planner) CreatePlan(entries []ImportMDFile, options PlanOptions) (*PlanResult, error) {
	// Generate a unique ID for the new page
	id, err := shared.GenerateUniqueID()
	if err != nil {
		return nil, fmt.Errorf("could not generate unique ID: %w", err)
	}
	result := &PlanResult{
		ID:       id,
		Items:    []PlanItem{},
		Errors:   []string{},
		TreeHash: p.wiki.TreeHash(),
	}
	for _, entry := range entries {
		resEntry, err := p.analyzeEntry(entry, options)
		if err != nil {
			p.log.Warn("could not import resource", "source_path", entry.SourcePath, "error", err)
			result.Errors = append(result.Errors, err.Error())
			continue
		}

		// Skip entries targeting ignored paths
		targetRel := filepath.ToSlash(resEntry.TargetPath)
		isDir := resEntry.Kind == tree.NodeKindSection
		if p.targetIsIgnored(targetRel, isDir) {
			p.log.Debug("skipping import entry matching .leafwikiignore", "target", targetRel, "source", entry.SourcePath)
			continue
		}

		result.Items = append(result.Items, *resEntry)
	}
	return result, nil
}

// mappedSource is the path/title/occupant mapping shared by the legacy and
// managed planners; action selection and conflict handling differs per mode.
type mappedSource struct {
	SourcePath  string
	TargetPath  string
	Title       string
	DesiredSlug string
	Kind        tree.NodeKind
	Exists      bool
	ExistingID  *string
	Notes       []string
}

// targetIsIgnored applies the same .leafwikiignore rules as the tree layer to
// a planned wiki target path.
func (p *Planner) targetIsIgnored(targetRel string, isDir bool) bool {
	dir := filepath.Join(p.storageDir, "root", filepath.Dir(targetRel))
	if ig := p.getIgnoreForDir(dir); ig != nil {
		return ig.Matches(targetRel, isDir)
	}
	return false
}

// mapSourceEntry maps one source markdown file to its wiki route, title, kind
// and current occupant. It performs no writes and decides no action.
func (p *Planner) mapSourceEntry(mdFile ImportMDFile, options PlanOptions) (*mappedSource, error) {
	// FS path for reading
	sourcePath := filepath.Join(options.SourceBasePath, filepath.FromSlash(mdFile.SourcePath))

	// Validate if sourcePath exists and is a file
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("source path is a directory, expected a file: " + mdFile.SourcePath)
	}

	// normalize source path (zip-ish)
	rel := filepath.ToSlash(strings.TrimSpace(mdFile.SourcePath))
	rel = strings.TrimPrefix(rel, "/")

	filenameLower := strings.ToLower(path.Base(rel))
	sourceDir := path.Dir(rel)
	if sourceDir == "." {
		sourceDir = ""
	}

	// Normalize import path segments into valid route slugs before we look anything up in the wiki.
	// At planning time we intentionally do not try to enforce sibling uniqueness in the tree.
	normalizedSourceDir, err := p.slugger.NormalizePathToValidSlugs(sourceDir)
	if err != nil {
		return nil, err
	}
	normalizedSourceDir = strings.Trim(normalizedSourceDir, "/")

	// compute wiki path (route)
	targetBase := strings.Trim(strings.TrimSpace(options.TargetBasePath), "/")

	kind := tree.NodeKindPage
	var wikiPath string

	if filenameLower == "index.md" {
		kind = tree.NodeKindSection
		wikiPath = strings.Trim(path.Join(targetBase, normalizedSourceDir), "/")
	} else {
		// File names map to page slugs, so we normalize the basename but preserve the extension.
		normalizedFilename, err := p.slugger.NormalizeFilenameToValidSlug(filenameLower) // e.g. "my-page.md"
		if err != nil {
			return nil, err
		}
		baseSlug := strings.TrimSuffix(normalizedFilename, path.Ext(normalizedFilename))
		wikiPath = strings.Trim(path.Join(targetBase, normalizedSourceDir, baseSlug), "/")
	}

	// lookup existing
	result, err := p.wiki.LookupPagePath(wikiPath)
	if err != nil {
		return nil, err
	}

	var notes []string
	md, err := markdown.LoadMarkdownFile(sourcePath)
	if err != nil {
		notes = append(notes, fmt.Sprintf("Failed to load markdown file for title extraction: %v", err))
	}

	// Determine fallback title
	title := path.Base(wikiPath) // fallback to last segment of wiki path
	if wikiPath == "" {
		// For root-level index.md or empty paths, use filename without extension
		title = strings.TrimSuffix(filenameLower, path.Ext(filenameLower))
		if title == "" {
			title = "root"
		}
	}

	if md != nil {
		var titleErr error
		title, titleErr = md.GetTitle()
		if titleErr != nil {
			notes = append(notes, fmt.Sprintf("Failed to extract title from file: %v", titleErr))
			title = "unknown" // ensure title is set
		}
	}

	mapped := &mappedSource{
		SourcePath: mdFile.SourcePath,
		TargetPath: wikiPath,
		Title:      title,
		Kind:       kind,
		Notes:      notes,
		Exists:     result.Exists,
	}

	if !result.Exists {
		// slug = last segment
		slug := ""
		if wikiPath != "" {
			segs := strings.Split(wikiPath, "/")
			slug = segs[len(segs)-1]
		}
		mapped.DesiredSlug = slug
		return mapped, nil
	}

	if len(result.Segments) == 0 {
		return nil, errors.New("invalid lookup result with zero segments for existing path")
	}

	last := result.Segments[len(result.Segments)-1]
	mapped.DesiredSlug = last.Slug
	mapped.ExistingID = last.ID
	return mapped, nil
}

// analyzeEntry analyzes a entry (directory or file) to be imported
func (p *Planner) analyzeEntry(mdFile ImportMDFile, options PlanOptions) (*PlanItem, error) {
	mapped, err := p.mapSourceEntry(mdFile, options)
	if err != nil {
		return nil, err
	}

	item := &PlanItem{
		SourcePath:  mapped.SourcePath,
		TargetPath:  mapped.TargetPath,
		Title:       mapped.Title,
		DesiredSlug: mapped.DesiredSlug,
		Kind:        mapped.Kind,
		Exists:      mapped.Exists,
		ExistingID:  mapped.ExistingID,
		Notes:       mapped.Notes,
	}
	if mapped.Exists {
		item.Action = PlanActionSkip
	} else {
		item.Action = PlanActionCreate
	}
	return item, nil
}
