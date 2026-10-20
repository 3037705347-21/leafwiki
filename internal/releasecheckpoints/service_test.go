package releasecheckpoints_test

import (
	"strings"
	"testing"

	"github.com/perber/wiki/internal/core/revision"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/releasecheckpoints"
)

type fakePageLookup struct {
	pages map[string]*tree.Page
	calls int
}

func (f *fakePageLookup) FindPageByRoutePath(routePath string) (*tree.Page, error) {
	f.calls++
	page, ok := f.pages[routePath]
	if !ok {
		return nil, tree.ErrPageNotFound
	}
	return page, nil
}

type fakeRevisionLookup struct {
	latest map[string]*revision.Revision
	err    error
}

func (f *fakeRevisionLookup) GetLatestRevision(pageID string) (*revision.Revision, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.latest[pageID], nil
}

func newTestService(t *testing.T, pages map[string]*tree.Page, latest map[string]*revision.Revision) (*releasecheckpoints.Service, *releasecheckpoints.Store, *fakePageLookup) {
	t.Helper()
	store, err := releasecheckpoints.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pageLookup := &fakePageLookup{pages: pages}
	svc := releasecheckpoints.NewService(store, pageLookup, &fakeRevisionLookup{latest: latest}, nil)
	return svc, store, pageLookup
}

func testPage(id, slug string) *tree.Page {
	return &tree.Page{PageNode: &tree.PageNode{ID: id, Slug: slug}}
}

func testRevision(id, contentHash string) *revision.Revision {
	return &revision.Revision{ID: id, ContentHash: contentHash}
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	localized, ok := sharederrors.AsLocalizedError(err)
	if !ok {
		t.Fatalf("expected LocalizedError %q, got %v", code, err)
	}
	if localized.Code != code {
		t.Fatalf("code = %q, want %q (args %v)", localized.Code, code, localized.Args)
	}
}

func TestServiceReconcileBindsLatestRevision(t *testing.T) {
	pages := map[string]*tree.Page{
		"intro": testPage("p1", "intro"),
		"guide": testPage("p2", "guide"),
	}
	latest := map[string]*revision.Revision{
		"p1": testRevision("r1", "h1"),
		"p2": testRevision("r2", "h2"),
	}
	svc, _, pageLookup := newTestService(t, pages, latest)

	result, err := svc.Reconcile(releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro", "guide"}},
		{Name: "v2", Pages: []string{"intro"}},
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Created != 3 {
		t.Fatalf("created = %d, want 3", result.Created)
	}
	// Same page appearing in two versions is resolved once.
	if pageLookup.calls != 2 {
		t.Fatalf("page lookup calls = %d, want 2", pageLookup.calls)
	}

	views, err := svc.GetManifest()
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(views) != 2 || views[0].Name != "v1" || views[1].Name != "v2" {
		t.Fatalf("views = %+v", views)
	}
	if len(views[0].Entries) != 2 || views[0].Entries[0].PagePath != "guide" {
		t.Fatalf("v1 entries = %+v", views[0].Entries)
	}
	if e := views[0].Entries[1]; e.RevisionID != "r1" || e.ContentHash != "h1" || e.PagePath != "intro" {
		t.Fatalf("intro entry = %+v", e)
	}

	// Re-running the same manifest is a no-op.
	result, err = svc.Reconcile(releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro", "guide"}},
		{Name: "v2", Pages: []string{"intro"}},
	}})
	if err != nil {
		t.Fatalf("re-run Reconcile: %v", err)
	}
	if result.Unchanged != 3 || result.Created != 0 || result.Removed != 0 {
		t.Fatalf("re-run result = %+v", result)
	}
}

func TestServiceReconcilePageNotFound(t *testing.T) {
	pages := map[string]*tree.Page{"intro": testPage("p1", "intro")}
	svc, store, _ := newTestService(t, pages, map[string]*revision.Revision{"p1": testRevision("r1", "h1")})

	_, err := svc.Reconcile(releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro", "missing"}},
	}})
	assertErrorCode(t, err, releasecheckpoints.ErrCodePageNotFound)

	all, _ := store.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected no rows after rejected reconcile, got %d", len(all))
	}
}

func TestServiceReconcileRevisionMissing(t *testing.T) {
	pages := map[string]*tree.Page{"intro": testPage("p1", "intro")}
	svc, store, _ := newTestService(t, pages, map[string]*revision.Revision{}) // no revision

	_, err := svc.Reconcile(releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro"}},
	}})
	assertErrorCode(t, err, releasecheckpoints.ErrCodeRevisionMissing)

	all, _ := store.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected no rows, got %d", len(all))
	}
}

func TestServiceReconcileDriftAfterPageEdit(t *testing.T) {
	pages := map[string]*tree.Page{"intro": testPage("p1", "intro")}
	latest := map[string]*revision.Revision{"p1": testRevision("r1", "h1")}
	svc, store, revLookup := newTestService(t, pages, latest)
	_ = revLookup

	manifest := releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro"}},
	}}
	if _, err := svc.Reconcile(manifest); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	// Page edited: latest revision moves to r2. Re-running the same manifest
	// must be rejected and leave the old checkpoint in place.
	latest["p1"] = testRevision("r2", "h2")
	_, err := svc.Reconcile(manifest)
	assertErrorCode(t, err, releasecheckpoints.ErrCodeDrift)

	all, _ := store.ListAll()
	if len(all) != 1 || all[0].RevisionID != "r1" {
		t.Fatalf("state after drift rejection = %+v", all)
	}

	// Publishing the changed content under a new name succeeds.
	_, err = svc.Reconcile(releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro"}},
		{Name: "v2", Pages: []string{"intro"}},
	}})
	assertErrorCode(t, err, releasecheckpoints.ErrCodeDrift) // v1 still pins r1 — whole batch rejected
}

func TestServiceReconcileValidation(t *testing.T) {
	pages := map[string]*tree.Page{"intro": testPage("p1", "intro")}
	latest := map[string]*revision.Revision{"p1": testRevision("r1", "h1")}
	svc, _, _ := newTestService(t, pages, latest)

	cases := []struct {
		name     string
		manifest releasecheckpoints.Manifest
		code     string
	}{
		{"empty version name", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "  ", Pages: []string{"intro"}},
		}}, releasecheckpoints.ErrCodeInvalidVersionName},
		{"empty page path", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "v1", Pages: []string{"   "}},
		}}, releasecheckpoints.ErrCodeInvalidPagePath},
		{"leading slash", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "v1", Pages: []string{"/intro"}},
		}}, releasecheckpoints.ErrCodeInvalidPagePath},
		{"trailing slash", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "v1", Pages: []string{"intro/"}},
		}}, releasecheckpoints.ErrCodeInvalidPagePath},
		{"empty segment", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "v1", Pages: []string{"a//b"}},
		}}, releasecheckpoints.ErrCodeInvalidPagePath},
		{"dotdot segment", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "v1", Pages: []string{"a/../b"}},
		}}, releasecheckpoints.ErrCodeInvalidPagePath},
		{"duplicate version", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "v1", Pages: []string{"intro"}},
			{Name: "v1", Pages: []string{"intro"}},
		}}, releasecheckpoints.ErrCodeDuplicateVersion},
		{"duplicate page", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: "v1", Pages: []string{"intro", "intro"}},
		}}, releasecheckpoints.ErrCodeDuplicatePage},
		{"name too long", releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
			{Name: strings.Repeat("x", releasecheckpoints.MaxVersionNameLen+1), Pages: []string{"intro"}},
		}}, releasecheckpoints.ErrCodeNameTooLong},
		{"too many versions", releasecheckpoints.Manifest{Versions: tooManyVersions()}, releasecheckpoints.ErrCodeTooManyEntries},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Reconcile(tc.manifest)
			assertErrorCode(t, err, tc.code)
		})
	}
}

func tooManyVersions() []releasecheckpoints.ManifestVersion {
	versions := make([]releasecheckpoints.ManifestVersion, 0, releasecheckpoints.MaxVersions+1)
	for i := 0; i <= releasecheckpoints.MaxVersions; i++ {
		versions = append(versions, releasecheckpoints.ManifestVersion{Name: "v" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+i)) + string(rune('0'+i%10))})
	}
	return versions
}

func TestServiceReconcileRevisionDisabled(t *testing.T) {
	store, err := releasecheckpoints.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pages := &fakePageLookup{pages: map[string]*tree.Page{"intro": testPage("p1", "intro")}}
	svc := releasecheckpoints.NewService(store, pages, nil, nil) // revision lookup disabled

	_, err = svc.Reconcile(releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro"}},
	}})
	assertErrorCode(t, err, releasecheckpoints.ErrCodeRevisionDisabled)

	// Empty manifest (clear all) is still allowed without revisions.
	if _, err := svc.Reconcile(releasecheckpoints.Manifest{}); err != nil {
		t.Fatalf("empty reconcile with revisions disabled: %v", err)
	}
}

func TestServiceDeleteAllForPage(t *testing.T) {
	pages := map[string]*tree.Page{"intro": testPage("p1", "intro")}
	latest := map[string]*revision.Revision{"p1": testRevision("r1", "h1")}
	svc, _, _ := newTestService(t, pages, latest)

	if _, err := svc.Reconcile(releasecheckpoints.Manifest{Versions: []releasecheckpoints.ManifestVersion{
		{Name: "v1", Pages: []string{"intro"}},
	}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if err := svc.DeleteAllForPage("p1"); err != nil {
		t.Fatalf("DeleteAllForPage: %v", err)
	}
	views, _ := svc.GetManifest()
	if len(views) != 0 {
		t.Fatalf("expected no checkpoints, got %+v", views)
	}
}
