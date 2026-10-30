package wiki

import (
	"context"
	"errors"
	"log/slog"
	"mime/multipart"

	"github.com/perber/wiki/internal/core/assets"
	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/favorites"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	"github.com/perber/wiki/internal/links"
	"github.com/perber/wiki/internal/properties"
	"github.com/perber/wiki/internal/search"
	"github.com/perber/wiki/internal/tags"
	wikiassets "github.com/perber/wiki/internal/wiki/assets"
	wikipages "github.com/perber/wiki/internal/wiki/pages"
	"github.com/perber/wiki/internal/wiki/pagesave"
)

// WikiImportAdapter implements the importer.ImporterWiki interface using
// the wiki's internal services directly via use cases.
type WikiImportAdapter struct {
	tree        *tree.TreeService
	slug        *tree.SlugService
	revision    *revision.Service
	links       *links.LinkService
	asset       *assets.AssetService
	tags        *tags.TagsService
	props       *properties.PropertiesService
	searchIndex *search.SQLiteIndex
	favorites   *favorites.FavoritesStore
	metrics     *httpmetrics.HTTPMetrics
	log         *slog.Logger
}

// NewWikiImportAdapter constructs an importer adapter backed by the wiki's
// internal services.
func NewWikiImportAdapter(w *Wiki) *WikiImportAdapter {
	return &WikiImportAdapter{
		tree:        w.tree,
		slug:        w.slug,
		revision:    w.revision,
		links:       w.links,
		asset:       w.asset,
		tags:        w.tags,
		props:       w.props,
		searchIndex: w.searchIndex,
		favorites:   w.favorites,
		metrics:     w.metrics,
		log:         w.log,
	}
}

func (a *WikiImportAdapter) TreeHash() string {
	return a.tree.TreeHash()
}

func (a *WikiImportAdapter) LookupPagePath(path string) (*tree.PathLookup, error) {
	return a.tree.LookupPagePath(path)
}

func (a *WikiImportAdapter) FindByPath(route string) (*tree.Page, error) {
	return a.tree.FindPageByRoutePath(route)
}

func (a *WikiImportAdapter) ListAssets(pageID string) ([]string, error) {
	page, err := a.tree.FindPageByID(pageID)
	if err != nil {
		return nil, err
	}
	return a.asset.ListAssetsForPage(page)
}

func (a *WikiImportAdapter) orchestrator() *pagesave.PageSaveOrchestrator {
	return pagesave.NewPageSaveOrchestrator(nil,
		pagesave.NewSearchIndexSideEffect(a.searchIndex, a.tree, a.log, nil),
		pagesave.NewLinkIndexSideEffect(a.links, a.log, nil),
		pagesave.NewTagsSideEffect(a.tags, a.log, nil),
		pagesave.NewPropertiesSideEffect(a.props, a.log, nil),
		pagesave.NewRevisionSideEffect(a.revision, a.log, nil),
	)
}

func (a *WikiImportAdapter) EnsurePath(userID, targetPath, title string, kind *tree.NodeKind) (*tree.Page, error) {
	out, err := wikipages.NewEnsurePathUseCase(a.tree, a.slug, a.orchestrator(), a.log).Execute(
		context.Background(),
		wikipages.EnsurePathInput{UserID: userID, TargetPath: targetPath, TargetTitle: title, Kind: kind},
	)
	if err != nil {
		return nil, err
	}
	return out.Page, nil
}

func (a *WikiImportAdapter) UpdatePage(userID, id, title, slug string, content *string, kind *tree.NodeKind) (*tree.Page, error) {
	current, err := a.tree.GetPage(id)
	if err != nil {
		return nil, err
	}
	out, err := wikipages.NewUpdatePageUseCase(a.tree, a.slug, a.orchestrator(), a.log, nil).Execute(
		context.Background(),
		wikipages.UpdatePageInput{UserID: userID, ID: id, Version: current.Version(), Title: title, Slug: slug, Content: content, Kind: kind, PreserveFrontmatter: true},
	)
	if err != nil {
		return nil, err
	}
	return out.Page, nil
}

func (a *WikiImportAdapter) UploadAsset(userID, pageID string, file multipart.File, filename string, maxBytes int64) (string, error) {
	out, err := wikiassets.NewUploadAssetUseCase(a.tree, a.asset, a.revision, a.log).Execute(
		context.Background(),
		wikiassets.UploadAssetInput{UserID: userID, PageID: pageID, File: file, Filename: filename, MaxBytes: maxBytes},
	)
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

// FindAssetByHash reuses an existing asset with identical contents instead of
// uploading a duplicate. Used by managed imports to stay idempotent on replay.
func (a *WikiImportAdapter) FindAssetByHash(_ string, pageID, hashHex string) (string, bool, error) {
	return a.asset.FindAssetByHash(pageID, hashHex)
}

// AssetHashesForPage maps an imported page's asset filenames to content hashes.
func (a *WikiImportAdapter) AssetHashesForPage(pageID string) (map[string]string, error) {
	return a.asset.AssetHashesForPageID(pageID)
}

// GetPagesByIDs batch-loads pages (content plus raw markdown) for managed
// reconciliation planning.
func (a *WikiImportAdapter) GetPagesByIDs(ids []string) ([]*tree.Page, []error) {
	return a.tree.GetPages(ids)
}

// FindNodeByID returns the live node for a managed manifest entry.
func (a *WikiImportAdapter) FindNodeByID(id string) (*tree.PageNode, error) {
	return a.tree.FindPageByID(id)
}

// GetPageByID returns the page with raw content, used by the managed executor
// for optimistic-lock versions and live conflict re-checks.
func (a *WikiImportAdapter) GetPageByID(id string) (*tree.Page, error) {
	return a.tree.GetPage(id)
}

// DeleteManagedPage performs a non-recursive delete through the same use case
// as the UI, cleaning assets, favorites, links and revisions. A page deleted
// concurrently (tree.ErrPageNotFound) is treated as success for replay.
func (a *WikiImportAdapter) DeleteManagedPage(userID, id, version string) error {
	err := wikipages.NewDeletePageUseCase(
		a.tree, a.revision, a.asset, a.favorites, a.orchestrator(), a.log, a.metrics,
	).Execute(context.Background(), wikipages.DeletePageInput{
		UserID: userID, ID: id, Version: version, Recursive: false,
	})
	if err != nil && errors.Is(err, tree.ErrPageNotFound) {
		return nil
	}
	return err
}
