package importer

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/perber/wiki/internal/core/markdown"
	"github.com/perber/wiki/internal/core/shared"
	"github.com/perber/wiki/internal/core/tree"
)

// Machine-readable conflict codes carried in PlanItem.Conflicts. Any blocking
// code on any item sets PlanResult.Blocked and forbids execution.
const (
	// ConflictLocalModified: the upstream source changed and the page was
	// edited in the wiki since the last import.
	ConflictLocalModified = "local-modified"
	// ConflictUnmanagedOccupant: the target path is held by a page that does
	// not belong to this collection (manual page or another collection).
	ConflictUnmanagedOccupant = "unmanaged-occupant"
	// ConflictManagedMissing: a previously imported page was deleted or moved
	// in the wiki while its source file still exists.
	ConflictManagedMissing = "managed-missing"
	// ConflictDeleteDrifted: the source file was removed but the page was
	// edited in the wiki, so it must not be deleted automatically.
	ConflictDeleteDrifted = "delete-drifted"
	// ConflictSectionHasChildren: deleting this node would leave the tree
	// inconsistent because children survive the batch (manual pages or
	// sources still present).
	ConflictSectionHasChildren = "section-has-children"
	// ConflictKindChangeUnsupported: source mapping changed between page and
	// section (x.md <-> x/index.md); v1 does not convert.
	ConflictKindChangeUnsupported = "kind-change-unsupported"
	// ConflictCaseInsensitiveCollision: two sources map to the same wiki path
	// apart from case (unsafe on Windows/macOS).
	ConflictCaseInsensitiveCollision = "case-insensitive-collision"
)

// Non-blocking notes.
const (
	noteDriftIgnored   = "drift-ignored"
	noteAlreadyDeleted = "already-deleted"
)

// ErrManagedEmptyBundle refuses a managed plan with zero importable files,
// which would otherwise delete the entire collection.
var ErrManagedEmptyBundle = errors.New("managed import bundle contains no markdown files")

// managedStrategy builds reconciliation plans for a named collection.
type managedStrategy struct {
	planner     *Planner
	collections *CollectionStore
}

func newManagedStrategy(planner *Planner, collections *CollectionStore) *managedStrategy {
	return &managedStrategy{planner: planner, collections: collections}
}

// WithManagedCollections registers the managed-collection strategy.
func WithManagedCollections(store *CollectionStore) ServiceOption {
	return func(is *ImporterService) {
		is.registerStrategy(newManagedStrategy(is.planner, store))
	}
}

func (s *managedStrategy) Mode() ImportMode { return ModeManaged }

func (s *managedStrategy) NewExecutor(sp *StoredPlan, assetMaxBytes int64) PlanExecutor {
	return managedExecutorUnavailable{}
}

// bundleItem is one mapped source plus its computed desired fingerprint.
type bundleItem struct {
	mapped   *mappedSource
	key      string
	desired  string
	planItem PlanItem
}

func (s *managedStrategy) CreatePlan(entries []ImportMDFile, opts PlanOptions) (*PlanResult, error) {
	if err := ValidateCollectionName(opts.Collection); err != nil {
		return nil, err
	}
	base := NormalizeTargetBasePath(opts.TargetBasePath)
	if err := s.collections.EnsureBasePathAvailable(opts.Collection, base); err != nil {
		return nil, err
	}

	manifest, err := s.collections.Load(opts.Collection)
	if err != nil && !errors.Is(err, ErrNoCollection) {
		return nil, err
	}
	if manifest != nil && manifest.TargetBasePath != base {
		return nil, fmt.Errorf("%w: collection %q pinned to %q, got %q",
			ErrManagedBasePathMismatch, opts.Collection, manifest.TargetBasePath, base)
	}

	all, loadErrs := s.collections.LoadAll()
	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrImportStateUnavailable, loadErrs[0])
	}
	ownerByID := make(map[string]string)
	for collectionName, m := range all {
		for _, e := range m.Entries {
			ownerByID[e.PageID] = collectionName
		}
	}

	id, err := shared.GenerateUniqueID()
	if err != nil {
		return nil, fmt.Errorf("could not generate unique ID: %w", err)
	}
	result := &PlanResult{
		ID:       id,
		TreeHash: s.planner.wiki.TreeHash(),
		Items:    []PlanItem{},
		Errors:   []string{},
	}

	// 1) Map every source entry, applying the same ignore rules as legacy.
	bundle := make([]*bundleItem, 0, len(entries))
	bundleKeys := make(map[string]bool, len(entries))
	foldedTargets := make(map[string]int) // lowercased target -> first index in bundle
	for _, entry := range entries {
		mapped, mapErr := s.planner.mapSourceEntry(entry, opts)
		if mapErr != nil {
			return nil, mapErr
		}
		if s.planner.targetIsIgnored(filepath.ToSlash(mapped.TargetPath), mapped.Kind == tree.NodeKindSection) {
			continue
		}
		key := normalizePlanSourcePath(entry.SourcePath)
		if bundleKeys[key] {
			return nil, fmt.Errorf("duplicate source path in bundle: %s", entry.SourcePath)
		}
		bundleKeys[key] = true

		bi := &bundleItem{mapped: mapped, key: key}
		bi.planItem = PlanItem{
			SourcePath:  mapped.SourcePath,
			TargetPath:  mapped.TargetPath,
			Title:       mapped.Title,
			DesiredSlug: mapped.DesiredSlug,
			Kind:        mapped.Kind,
			Exists:      mapped.Exists,
			ExistingID:  mapped.ExistingID,
			Notes:       append([]string{}, mapped.Notes...),
		}
		folded := strings.ToLower(mapped.TargetPath)
		if folded != "" {
			if first, ok := foldedTargets[folded]; ok {
				bi.planItem.Conflicts = append(bi.planItem.Conflicts, ConflictCaseInsensitiveCollision)
				bundle[first].planItem.Conflicts = append(bundle[first].planItem.Conflicts, ConflictCaseInsensitiveCollision)
			} else {
				foldedTargets[folded] = len(bundle)
			}
		}
		bundle = append(bundle, bi)
	}

	if len(bundle) == 0 {
		return nil, ErrManagedEmptyBundle
	}

	// 2) Provisional plan items feed the side-effect-free link/asset resolver.
	provisionalItems := make([]PlanItem, len(bundle))
	for i, bi := range bundle {
		provisionalItems[i] = bi.planItem
	}
	provisional := &PlanResult{Items: provisionalItems}
	tokenTransformer := newContentTransformerWithResolver(
		provisional, opts.SourceBasePath, newTokenAssetResolver(opts.SourceBasePath))
	// Live pages are read-only here: relative asset paths are meaningless
	// outside the workspace, and the live resolver only rewrites /assets/
	// hrefs belonging to the page itself.
	liveTransformer := newContentTransformerWithResolver(
		provisional, "", newLiveAssetResolver(s.planner.wiki))

	// 3) Desired fingerprints.
	for _, bi := range bundle {
		desired, fpErr := s.desiredFingerprint(opts, tokenTransformer, bi)
		if fpErr != nil {
			return nil, fpErr
		}
		bi.desired = desired
		bi.planItem.DesiredFingerprint = desired
	}

	// 4) Batch-read current state of every page the manifest records.
	pageByID := s.loadManifestPages(manifest)

	// 5) Classify source items.
	adoptedKeys := map[string]bool{}
	for _, bi := range bundle {
		s.classifyBundleItem(bi, manifest, ownerByID, pageByID, adoptedKeys, liveTransformer)
	}

	// 6) Delete sweep for manifest keys absent from the bundle (unless their
	// page was adopted by a renamed source key).
	deleteItems := s.planDeletes(manifest, bundleKeys, adoptedKeys, ownerByID, pageByID, liveTransformer)

	// 7) Assemble: sources in their original order, deletes deepest-first.
	for _, bi := range bundle {
		result.Items = append(result.Items, bi.planItem)
	}
	sort.SliceStable(deleteItems, func(i, j int) bool {
		return strings.Count(deleteItems[i].TargetPath, "/") > strings.Count(deleteItems[j].TargetPath, "/")
	})
	result.Items = append(result.Items, deleteItems...)

	for i := range result.Items {
		if len(result.Items[i].Conflicts) > 0 {
			result.Blocked = true
			result.ConflictCount++
		}
	}
	return result, nil
}

// desiredFingerprint builds the content importer would write and fingerprints
// it with assets normalized to content-addressed tokens.
func (s *managedStrategy) desiredFingerprint(opts PlanOptions, transformer *contentTransformer, bi *bundleItem) (string, error) {
	sourceAbs := filepath.Join(opts.SourceBasePath, filepath.FromSlash(bi.mapped.SourcePath))
	mdFile, err := markdown.LoadMarkdownFile(sourceAbs)
	if err != nil {
		return "", fmt.Errorf("load source %q: %w", bi.mapped.SourcePath, err)
	}
	imported, err := buildImportedContent(mdFile)
	if err != nil {
		return "", fmt.Errorf("build content %q: %w", bi.mapped.SourcePath, err)
	}
	// Placeholder node: link resolution reads only the plan; the token
	// resolver never uses the page ID.
	placeholder := &tree.Page{PageNode: &tree.PageNode{Kind: bi.mapped.Kind}}
	transformed, err := transformer.TransformContent("system", bi.mapped.SourcePath, placeholder, imported, s.planner.wiki)
	if err != nil {
		return "", fmt.Errorf("transform %q: %w", bi.mapped.SourcePath, err)
	}
	return ManagedContentFingerprint(bi.mapped.Title, transformed)
}

func (s *managedStrategy) loadManifestPages(manifest *CollectionManifest) map[string]*tree.Page {
	out := map[string]*tree.Page{}
	if manifest == nil {
		return out
	}
	ids := make([]string, 0, len(manifest.Entries))
	seen := map[string]bool{}
	for _, e := range manifest.Entries {
		if e.PageID != "" && !seen[e.PageID] {
			seen[e.PageID] = true
			ids = append(ids, e.PageID)
		}
	}
	if len(ids) == 0 {
		return out
	}
	pages, _ := s.planner.wiki.GetPagesByIDs(ids)
	for _, p := range pages {
		if p != nil {
			out[p.ID] = p
		}
	}
	return out
}

// classifyBundleItem mutates bi.planItem applying the decision table.
func (s *managedStrategy) classifyBundleItem(
	bi *bundleItem,
	manifest *CollectionManifest,
	ownerByID map[string]string,
	pageByID map[string]*tree.Page,
	adoptedKeys map[string]bool,
	transformer *contentTransformer,
) {
	item := &bi.planItem

	entry, hasEntry := manifestEntry(manifest, bi.key)
	if !hasEntry && bi.mapped.Exists && bi.mapped.ExistingID != nil {
		// Row: target held by a page this collection previously imported under
		// a different source key (rename/move inside the bundle) -> adopt it.
		if ownerByID[*bi.mapped.ExistingID] == optsCollection(manifest) {
			if oldKey, old, ok := manifestEntryByPageID(manifest, *bi.mapped.ExistingID); ok && oldKey != bi.key {
				entry, hasEntry = old, true
				adoptedKeys[oldKey] = true
			}
		}
	}

	if !hasEntry {
		if !bi.mapped.Exists {
			item.Action = PlanActionCreate
			return
		}
		item.Action = PlanActionCreate
		if owner := ownerByID[*bi.mapped.ExistingID]; owner != "" && owner != optsCollection(manifest) {
			item.Conflicts = append(item.Conflicts, ConflictUnmanagedOccupant)
			item.Notes = append(item.Notes, "target path is owned by collection "+owner)
		} else {
			item.Conflicts = append(item.Conflicts, ConflictUnmanagedOccupant)
		}
		return
	}

	item.ExistingID = &entry.PageID
	page := pageByID[entry.PageID]
	if page == nil {
		item.Action = PlanActionUpdate
		item.Conflicts = append(item.Conflicts, ConflictManagedMissing)
		return
	}

	livePath := strings.Trim(page.CalculatePath(), "/")
	if livePath != entry.TargetPath {
		item.Action = PlanActionUpdate
		item.Conflicts = append(item.Conflicts, ConflictManagedMissing)
		item.Notes = append(item.Notes, fmt.Sprintf("page moved to %q", livePath))
		return
	}

	if bi.mapped.Exists && bi.mapped.ExistingID != nil && *bi.mapped.ExistingID != entry.PageID {
		item.Action = PlanActionUpdate
		item.Conflicts = append(item.Conflicts, ConflictUnmanagedOccupant)
		return
	}

	if page.Kind != entry.Kind {
		item.Action = PlanActionUpdate
		item.Conflicts = append(item.Conflicts, ConflictKindChangeUnsupported)
		return
	}

	if bi.desired == entry.AppliedFingerprint {
		item.Action = PlanActionSkip
		current, err := s.currentFingerprint(transformer, page, bi.mapped.SourcePath)
		if err == nil && current != entry.AppliedFingerprint {
			item.Notes = append(item.Notes, noteDriftIgnored)
		}
		return
	}

	current, err := s.currentFingerprint(transformer, page, bi.mapped.SourcePath)
	if err != nil {
		item.Action = PlanActionUpdate
		item.Conflicts = append(item.Conflicts, ConflictLocalModified)
		item.Notes = append(item.Notes, "could not read current page: "+err.Error())
		return
	}
	if current == entry.AppliedFingerprint {
		item.Action = PlanActionUpdate
		return
	}
	item.Action = PlanActionUpdate
	item.Conflicts = append(item.Conflicts, ConflictLocalModified)
}

// planDeletes produces delete/conflict items for sources removed from the bundle.
func (s *managedStrategy) planDeletes(
	manifest *CollectionManifest,
	bundleKeys map[string]bool,
	adoptedKeys map[string]bool,
	ownerByID map[string]string,
	pageByID map[string]*tree.Page,
	transformer *contentTransformer,
) []PlanItem {
	if manifest == nil {
		return nil
	}

	// IDs every sweep entry references; child nodes whose ID is not in this
	// set survive the batch and make a parent delete impossible.
	sweepIDs := map[string]bool{}
	sweepKeys := make([]string, 0)
	for key, e := range manifest.Entries {
		if bundleKeys[key] || adoptedKeys[key] {
			continue
		}
		sweepKeys = append(sweepKeys, key)
		sweepIDs[e.PageID] = true
	}
	sort.Strings(sweepKeys)

	items := make([]PlanItem, 0, len(sweepKeys))
	for _, key := range sweepKeys {
		entry := manifest.Entries[key]
		item := PlanItem{
			SourcePath: entry.SourcePath,
			TargetPath: entry.TargetPath,
			Title:      entry.Title,
			Kind:       entry.Kind,
			Exists:     true,
			ExistingID: &entry.PageID,
			Action:     PlanActionDelete,
		}

		page := pageByID[entry.PageID]
		if page == nil {
			item.Action = PlanActionSkip
			item.Notes = append(item.Notes, noteAlreadyDeleted)
			items = append(items, item)
			continue
		}

		livePath := strings.Trim(page.CalculatePath(), "/")
		if livePath != entry.TargetPath {
			item.Conflicts = append(item.Conflicts, ConflictManagedMissing)
			item.Notes = append(item.Notes, fmt.Sprintf("page moved to %q", livePath))
			items = append(items, item)
			continue
		}

		// A different occupant at the target path must never be deleted.
		lookup, lookupErr := s.planner.wiki.LookupPagePath(entry.TargetPath)
		if lookupErr == nil && lookup.Exists {
			last := lookup.Segments[len(lookup.Segments)-1]
			if last.ID != nil && *last.ID != entry.PageID {
				if owner := ownerByID[*last.ID]; owner != "" {
					item.Notes = append(item.Notes, "target path is owned by collection "+owner)
				}
				item.Conflicts = append(item.Conflicts, ConflictUnmanagedOccupant)
				items = append(items, item)
				continue
			}
		}

		// Non-recursive delete: children that are not themselves removed by
		// this batch (manual pages, surviving sources) block the delete.
		for _, child := range page.Children {
			if !sweepIDs[child.ID] {
				item.Conflicts = append(item.Conflicts, ConflictSectionHasChildren)
				item.Notes = append(item.Notes, "node still has children after the batch")
				break
			}
		}
		if len(item.Conflicts) > 0 {
			items = append(items, item)
			continue
		}

		current, err := s.currentFingerprint(transformer, page, entry.SourcePath)
		if err != nil || current != entry.AppliedFingerprint {
			item.Conflicts = append(item.Conflicts, ConflictDeleteDrifted)
			if err != nil {
				item.Notes = append(item.Notes, "could not read current page: "+err.Error())
			}
			items = append(items, item)
			continue
		}

		items = append(items, item)
	}
	return items
}

func (s *managedStrategy) currentFingerprint(transformer *contentTransformer, page *tree.Page, sourcePath string) (string, error) {
	transformed, err := transformer.TransformContent("system", sourcePath, page, page.RawContent, s.planner.wiki)
	if err != nil {
		return "", err
	}
	return ManagedContentFingerprint(page.Title, transformed)
}

func optsCollection(manifest *CollectionManifest) string {
	if manifest == nil {
		return ""
	}
	return manifest.Collection
}

func manifestEntry(manifest *CollectionManifest, key string) (ManifestEntry, bool) {
	if manifest == nil {
		return ManifestEntry{}, false
	}
	e, ok := manifest.Entries[key]
	return e, ok
}

func manifestEntryByPageID(manifest *CollectionManifest, pageID string) (string, ManifestEntry, bool) {
	if manifest == nil {
		return "", ManifestEntry{}, false
	}
	for key, e := range manifest.Entries {
		if e.PageID == pageID {
			return key, e, true
		}
	}
	return "", ManifestEntry{}, false
}

// managedExecutorUnavailable is replaced by the real managed executor in the
// follow-up change; without it a managed plan cannot execute.
type managedExecutorUnavailable struct{}

func (managedExecutorUnavailable) Execute(string) (*ExecutionResult, error) {
	return nil, errors.New("managed executor not available")
}

func (managedExecutorUnavailable) WithProgressCallback(func(ExecutionProgress, *ExecutionResult)) PlanExecutor {
	return managedExecutorUnavailable{}
}

func (managedExecutorUnavailable) WithCancelCheck(func() bool) PlanExecutor {
	return managedExecutorUnavailable{}
}

func (managedExecutorUnavailable) WithResumeState(int, *ExecutionResult) PlanExecutor {
	return managedExecutorUnavailable{}
}
