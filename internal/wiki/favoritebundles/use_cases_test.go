package favoritebundles_test

import (
	"context"
	"errors"
	"testing"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/favorites"
	"github.com/perber/wiki/internal/test_utils"
	favoritebundles "github.com/perber/wiki/internal/wiki/favoritebundles"
)

type bundleDeps struct {
	tree  *tree.TreeService
	store *favorites.FavoritesStore

	create      *favoritebundles.CreateBundleUseCase
	update      *favoritebundles.UpdateBundleUseCase
	del         *favoritebundles.DeleteBundleUseCase
	get         *favoritebundles.GetBundleUseCase
	list        *favoritebundles.ListBundlesUseCase
	sync        *favoritebundles.SyncBundleUseCase
	unsubscribe *favoritebundles.UnsubscribeBundleUseCase
}

func newBundleDeps(t *testing.T) *bundleDeps {
	t.Helper()
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	store, err := favorites.NewFavoritesStore(dir, nil)
	if err != nil {
		t.Fatalf("NewFavoritesStore: %v", err)
	}
	t.Cleanup(func() { test_utils.WrapCloseWithErrorCheck(store.Close, t) })

	return &bundleDeps{
		tree:        treeService,
		store:       store,
		create:      favoritebundles.NewCreateBundleUseCase(treeService, store),
		update:      favoritebundles.NewUpdateBundleUseCase(treeService, store),
		del:         favoritebundles.NewDeleteBundleUseCase(store),
		get:         favoritebundles.NewGetBundleUseCase(store),
		list:        favoritebundles.NewListBundlesUseCase(store),
		sync:        favoritebundles.NewSyncBundleUseCase(treeService, store),
		unsubscribe: favoritebundles.NewUnsubscribeBundleUseCase(store),
	}
}

func mustCreatePage(t *testing.T, deps *bundleDeps, title, slug string) string {
	t.Helper()
	kind := tree.NodeKindPage
	id, err := deps.tree.CreateNode("admin-1", nil, title, slug, &kind)
	if err != nil {
		t.Fatalf("CreateNode %s: %v", slug, err)
	}
	return *id
}

func createBundle(t *testing.T, deps *bundleDeps, name string, pageIDs []string) string {
	t.Helper()
	bundle, err := deps.create.Execute(context.Background(), favoritebundles.CreateBundleInput{
		Name: name, PageIDs: pageIDs, CreatedBy: "admin-1",
	})
	if err != nil {
		t.Fatalf("create bundle %q: %v", name, err)
	}
	return bundle.ID
}

func TestCreateBundle_HappyPath(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	p2 := mustCreatePage(t, deps, "Beta", "beta")

	bundle, err := deps.create.Execute(context.Background(), favoritebundles.CreateBundleInput{
		Name: "team", Description: "desc", PageIDs: []string{p1, p2}, CreatedBy: "admin-1",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if bundle.Name != "team" || bundle.CreatedBy != "admin-1" {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}

	out, err := deps.get.Execute(context.Background(), favoritebundles.GetBundleInput{UserID: "user-1", BundleID: bundle.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(out.PageIDs) != 2 || out.PageIDs[0] != p1 || out.PageIDs[1] != p2 || out.Subscribed {
		t.Fatalf("unexpected summary: %+v", out)
	}
}

func TestCreateBundle_MissingPage_RejectedWithLocalizedCode(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")

	_, err := deps.create.Execute(context.Background(), favoritebundles.CreateBundleInput{
		Name: "team", PageIDs: []string{p1, "ghost-page"}, CreatedBy: "admin-1",
	})
	loc, ok := sharederrors.AsLocalizedError(err)
	if !ok || loc.Code != favoritebundles.ErrCodeBundleEntryPageMissing {
		t.Fatalf("want localized %s, got %v", favoritebundles.ErrCodeBundleEntryPageMissing, err)
	}
	bundles, err := deps.store.ListBundles()
	if err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	if len(bundles) != 0 {
		t.Fatal("rejected create must not persist a bundle")
	}
}

func TestCreateBundle_DuplicateAndEmptyName(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")

	_, err := deps.create.Execute(context.Background(), favoritebundles.CreateBundleInput{
		Name: "team", PageIDs: []string{p1, p1}, CreatedBy: "admin-1",
	})
	if !errors.Is(err, favorites.ErrDuplicateBundleEntry) {
		t.Fatalf("want ErrDuplicateBundleEntry, got %v", err)
	}

	_, err = deps.create.Execute(context.Background(), favoritebundles.CreateBundleInput{
		Name: "   ", PageIDs: []string{p1}, CreatedBy: "admin-1",
	})
	var vErr *sharederrors.ValidationErrors
	if !errors.As(err, &vErr) {
		t.Fatalf("want ValidationErrors, got %v", err)
	}
}

func TestUpdateBundle_HappyPathAndMissing(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	p2 := mustCreatePage(t, deps, "Beta", "beta")
	id := createBundle(t, deps, "team", []string{p1})

	if err := deps.update.Execute(context.Background(), favoritebundles.UpdateBundleInput{
		BundleID: id, Name: "team v2", PageIDs: []string{p2},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	out, err := deps.get.Execute(context.Background(), favoritebundles.GetBundleInput{UserID: "user-1", BundleID: id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.Bundle.Name != "team v2" || len(out.PageIDs) != 1 || out.PageIDs[0] != p2 {
		t.Fatalf("update not reflected: %+v", out)
	}

	err = deps.update.Execute(context.Background(), favoritebundles.UpdateBundleInput{BundleID: "nope", Name: "x"})
	if !errors.Is(err, favorites.ErrBundleNotFound) {
		t.Fatalf("want ErrBundleNotFound, got %v", err)
	}
}

func TestSync_FirstSyncSubscribesAndAdds(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	p2 := mustCreatePage(t, deps, "Beta", "beta")
	id := createBundle(t, deps, "team", []string{p1, p2})

	res, err := deps.sync.Execute(context.Background(), favoritebundles.SyncBundleInput{UserID: "user-1", BundleID: id})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Added) != 2 || len(res.Removed) != 0 {
		t.Fatalf("unexpected sync result: %+v", res)
	}

	out, err := deps.get.Execute(context.Background(), favoritebundles.GetBundleInput{UserID: "user-1", BundleID: id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !out.Subscribed || out.LastSyncedAt == nil {
		t.Fatalf("expected subscribed state, got %+v", out)
	}
}

func TestSync_MissingPage_RejectedWholeSync(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	p2 := mustCreatePage(t, deps, "Beta", "beta")
	id := createBundle(t, deps, "team", []string{p1, p2})

	// Delete the page on disk (bypassing the pages use-case cascade, as a
	// resync/restore mismatch would), so the stored entry dangles.
	page2, err := deps.tree.GetPage(p2)
	if err != nil {
		t.Fatalf("GetPage p2: %v", err)
	}
	if err := deps.tree.DeleteNode("admin-1", p2, false, page2.Version()); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	_, err = deps.sync.Execute(context.Background(), favoritebundles.SyncBundleInput{UserID: "user-1", BundleID: id})
	loc, ok := sharederrors.AsLocalizedError(err)
	if !ok || loc.Code != favoritebundles.ErrCodeBundleSyncPageMissing {
		t.Fatalf("want localized %s, got %v", favoritebundles.ErrCodeBundleSyncPageMissing, err)
	}
	got, err := deps.store.ListPageIDsForUser("user-1")
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("rejected sync must add no favorites, got %v", got)
	}
}

func TestSync_PersonalFavoriteConflict_RejectedWholeSync(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	p2 := mustCreatePage(t, deps, "Beta", "beta")
	id := createBundle(t, deps, "team", []string{p1, p2})
	if err := deps.store.Add("user-1", p2); err != nil {
		t.Fatalf("personal Add: %v", err)
	}

	_, err := deps.sync.Execute(context.Background(), favoritebundles.SyncBundleInput{UserID: "user-1", BundleID: id})
	if !errors.Is(err, favorites.ErrPersonalFavoriteConflict) {
		t.Fatalf("want ErrPersonalFavoriteConflict, got %v", err)
	}
	got, err := deps.store.ListPageIDsForUser("user-1")
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(got) != 1 || got[0] != p2 {
		t.Fatalf("conflict rollback must leave only the personal favorite, got %v", got)
	}
}

func TestSync_MissingBundle(t *testing.T) {
	deps := newBundleDeps(t)
	_, err := deps.sync.Execute(context.Background(), favoritebundles.SyncBundleInput{UserID: "user-1", BundleID: "ghost"})
	if !errors.Is(err, favorites.ErrBundleNotFound) {
		t.Fatalf("want ErrBundleNotFound, got %v", err)
	}
}

func TestUnsubscribe_RemovesBundleFavoritesAndIsIdempotent(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	personal := mustCreatePage(t, deps, "Personal", "personal")
	id := createBundle(t, deps, "team", []string{p1})
	if _, err := deps.sync.Execute(context.Background(), favoritebundles.SyncBundleInput{UserID: "user-1", BundleID: id}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := deps.store.Add("user-1", personal); err != nil {
		t.Fatalf("personal Add: %v", err)
	}

	if err := deps.unsubscribe.Execute(context.Background(), favoritebundles.UnsubscribeBundleInput{
		UserID: "user-1", BundleID: id,
	}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	got, _ := deps.store.ListPageIDsForUser("user-1")
	if len(got) != 1 || got[0] != personal {
		t.Fatalf("only the personal favorite must remain, got %v", got)
	}
	// Idempotent.
	if err := deps.unsubscribe.Execute(context.Background(), favoritebundles.UnsubscribeBundleInput{
		UserID: "user-1", BundleID: id,
	}); err != nil {
		t.Fatalf("second Unsubscribe: %v", err)
	}
	// Missing bundle is a 404.
	err := deps.unsubscribe.Execute(context.Background(), favoritebundles.UnsubscribeBundleInput{
		UserID: "user-1", BundleID: "ghost",
	})
	if !errors.Is(err, favorites.ErrBundleNotFound) {
		t.Fatalf("want ErrBundleNotFound, got %v", err)
	}
}

func TestListBundles_PerUserSubscriptionState(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	id := createBundle(t, deps, "team", []string{p1})
	if _, err := deps.sync.Execute(context.Background(), favoritebundles.SyncBundleInput{UserID: "user-1", BundleID: id}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	out1, err := deps.list.Execute(context.Background(), favoritebundles.ListBundlesInput{UserID: "user-1"})
	if err != nil {
		t.Fatalf("List user-1: %v", err)
	}
	out2, err := deps.list.Execute(context.Background(), favoritebundles.ListBundlesInput{UserID: "user-2"})
	if err != nil {
		t.Fatalf("List user-2: %v", err)
	}
	if len(out1.Bundles) != 1 || !out1.Bundles[0].Subscribed {
		t.Fatalf("user-1 should see the bundle as subscribed: %+v", out1.Bundles)
	}
	if len(out2.Bundles) != 1 || out2.Bundles[0].Subscribed {
		t.Fatalf("user-2 should see the bundle as unsubscribed: %+v", out2.Bundles)
	}
}

func TestDeleteBundle_RemovesDefinitionKeepsFavorites(t *testing.T) {
	deps := newBundleDeps(t)
	p1 := mustCreatePage(t, deps, "Alpha", "alpha")
	id := createBundle(t, deps, "team", []string{p1})
	if _, err := deps.sync.Execute(context.Background(), favoritebundles.SyncBundleInput{UserID: "user-1", BundleID: id}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if err := deps.del.Execute(context.Background(), favoritebundles.DeleteBundleInput{BundleID: id}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := deps.store.ListPageIDsForUser("user-1")
	if len(got) != 1 || got[0] != p1 {
		t.Fatalf("favorites must survive bundle deletion, got %v", got)
	}
	if err := deps.del.Execute(context.Background(), favoritebundles.DeleteBundleInput{BundleID: id}); !errors.Is(err, favorites.ErrBundleNotFound) {
		t.Fatalf("want ErrBundleNotFound, got %v", err)
	}
}
