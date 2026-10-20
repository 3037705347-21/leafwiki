package releasecheckpoints

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
)

// PageLookup resolves route paths to pages. *tree.TreeService satisfies it.
type PageLookup interface {
	FindPageByRoutePath(routePath string) (*tree.Page, error)
}

// RevisionLookup returns a page's latest revision. *revision.Service
// satisfies it; nil means revision history is disabled.
type RevisionLookup interface {
	GetLatestRevision(pageID string) (*revision.Revision, error)
}

// Service orchestrates checkpoint reconciliation: manifest validation,
// filesystem lookups and the transactional store update are serialized as a
// whole so two concurrent applies cannot interleave.
type Service struct {
	store     *Store
	pages     PageLookup
	revisions RevisionLookup
	mu        sync.Mutex
	log       *slog.Logger

	now func() time.Time
}

func NewService(store *Store, pages PageLookup, revisions RevisionLookup, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store:     store,
		pages:     pages,
		revisions: revisions,
		log:       log.With("component", "ReleaseCheckpointService"),
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// SetRevisionLookup wires the revision service once the composition root has
// constructed it (the checkpoint store is initialized earlier so revision
// pruning can register its protection callback).
func (s *Service) SetRevisionLookup(r RevisionLookup) {
	s.revisions = r
}

// Reconcile validates the submitted manifest against the live tree/revisions
// and replaces the stored checkpoint set atomically. Any invalid or drifted
// entry rejects the whole batch and leaves existing state untouched.
func (s *Service) Reconcile(in Manifest) (ReconcileResult, error) {
	if err := validateManifest(in); err != nil {
		return ReconcileResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(in.Versions) == 0 {
		// Empty manifest = clear all checkpoints; valid even with revisions
		// disabled.
		return s.store.Reconcile(nil, s.now())
	}
	if s.revisions == nil {
		return ReconcileResult{}, errRevisionDisabled()
	}

	resolvedPages := make(map[string]*tree.Page)
	desired := make([]DesiredCheckpoint, 0)

	for _, v := range in.Versions {
		versionName := strings.TrimSpace(v.Name)
		for _, rawPath := range v.Pages {
			pagePath := strings.TrimSpace(rawPath)

			page, ok := resolvedPages[pagePath]
			if !ok {
				p, err := s.pages.FindPageByRoutePath(pagePath)
				if err != nil || p == nil {
					return ReconcileResult{}, errPageNotFound(versionName, pagePath)
				}
				resolvedPages[pagePath] = p
				page = p
			}

			rev, err := s.revisions.GetLatestRevision(page.ID)
			if err != nil {
				return ReconcileResult{}, errInternal(err)
			}
			if rev == nil {
				return ReconcileResult{}, errRevisionMissing(versionName, pagePath)
			}

			desired = append(desired, DesiredCheckpoint{
				VersionName: versionName,
				PageID:      page.ID,
				RevisionID:  rev.ID,
				ContentHash: rev.ContentHash,
				PagePath:    page.CalculatePath(),
			})
		}
	}

	return s.store.Reconcile(desired, s.now())
}

// GetManifest returns stored checkpoints grouped by version name.
func (s *Service) GetManifest() ([]ManifestView, error) {
	all, err := s.store.ListAll()
	if err != nil {
		return nil, err
	}
	var views []ManifestView
	index := make(map[string]int)
	for _, cp := range all {
		i, ok := index[cp.VersionName]
		if !ok {
			views = append(views, ManifestView{Name: cp.VersionName})
			i = len(views) - 1
			index[cp.VersionName] = i
		}
		views[i].Entries = append(views[i].Entries, cp)
	}
	return views, nil
}

func (s *Service) ListRevisionNames(pageID string) (map[string][]string, error) {
	return s.store.ListRevisionNames(pageID)
}

func (s *Service) DeleteAllForPage(pageID string) error {
	return s.store.DeleteAllForPage(pageID)
}

// Store exposes the underlying store for composition-root wiring (restore
// swap, revision pruning protection).
func (s *Service) Store() *Store {
	return s.store
}

func validateManifest(in Manifest) error {
	if len(in.Versions) > MaxVersions {
		return errTooManyEntries()
	}

	seenVersions := make(map[string]bool)
	totalEntries := 0
	for vi, v := range in.Versions {
		name := strings.TrimSpace(v.Name)
		if name == "" {
			return errInvalidVersionName(strconv.Itoa(vi))
		}
		if len(name) > MaxVersionNameLen {
			return errNameTooLong(name)
		}
		if seenVersions[name] {
			return errDuplicateVersion(name)
		}
		seenVersions[name] = true

		seenPages := make(map[string]bool)
		for _, rawPath := range v.Pages {
			pagePath := strings.TrimSpace(rawPath)
			if !isValidPagePath(pagePath) {
				return errInvalidPagePath(name, pagePath)
			}
			if len(pagePath) > MaxPagePathLen {
				return errPathTooLong(name, pagePath)
			}
			if seenPages[pagePath] {
				return errDuplicatePage(name, pagePath)
			}
			seenPages[pagePath] = true
		}
		totalEntries += len(v.Pages)
		if totalEntries > MaxTotalEntries {
			return errTooManyEntries()
		}
	}
	return nil
}

// isValidPagePath mirrors the route-path shape consumed by
// TreeService.FindPageByRoutePath: non-empty, no leading/trailing slash, no
// empty segments, no "." or ".." segments.
func isValidPagePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
