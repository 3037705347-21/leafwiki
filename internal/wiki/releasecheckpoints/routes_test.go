package wikireleasecheckpoints

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/releasecheckpoints"
)

type fakePageLookup struct {
	pages map[string]*tree.Page
}

func (f *fakePageLookup) FindPageByRoutePath(routePath string) (*tree.Page, error) {
	page, ok := f.pages[routePath]
	if !ok {
		return nil, tree.ErrPageNotFound
	}
	return page, nil
}

type fakeRevisionLookup struct {
	latest map[string]*revision.Revision
}

func (f *fakeRevisionLookup) GetLatestRevision(pageID string) (*revision.Revision, error) {
	return f.latest[pageID], nil
}

func newTestRouter(t *testing.T) (*gin.Engine, *fakeRevisionLookup) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store, err := releasecheckpoints.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pages := &fakePageLookup{pages: map[string]*tree.Page{
		"intro": {PageNode: &tree.PageNode{ID: "p1", Slug: "intro"}},
	}}
	revs := &fakeRevisionLookup{latest: map[string]*revision.Revision{
		"p1": {ID: "r1", ContentHash: "h1"},
	}}
	svc := releasecheckpoints.NewService(store, pages, revs, nil)

	routes := NewRoutes(RoutesConfig{
		GetManifest: NewGetManifestUseCase(svc),
		Reconcile:   NewReconcileUseCase(svc),
	})
	engine := gin.New()
	engine.GET("/api/admin/release-checkpoints", routes.handleGetManifest)
	engine.PUT("/api/admin/release-checkpoints/manifest", routes.handleReconcile)
	return engine, revs
}

func doRequest(t *testing.T, engine *gin.Engine, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != "" {
		buf = bytes.NewReader([]byte(body))
	} else {
		buf = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestGetManifest_Empty(t *testing.T) {
	engine, _ := newTestRouter(t)
	rec := doRequest(t, engine, http.MethodGet, "/api/admin/release-checkpoints", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Versions []json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Versions) != 0 {
		t.Fatalf("expected empty versions, got %s", rec.Body.String())
	}
}

func TestReconcileManifest_HappyPathAndIdempotent(t *testing.T) {
	engine, _ := newTestRouter(t)
	body := `{"versions":[{"name":"v1","pages":["intro"]}]}`

	rec := doRequest(t, engine, http.MethodPut, "/api/admin/release-checkpoints/manifest", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result releasecheckpoints.ReconcileResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("created = %d, want 1", result.Created)
	}

	// Same manifest again: idempotent no-op.
	rec = doRequest(t, engine, http.MethodPut, "/api/admin/release-checkpoints/manifest", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-run status = %d, body = %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result.Unchanged != 1 || result.Created != 0 || result.Removed != 0 {
		t.Fatalf("re-run result = %+v", result)
	}

	// GET reflects the checkpoint.
	rec = doRequest(t, engine, http.MethodGet, "/api/admin/release-checkpoints", "")
	var got struct {
		Versions []struct {
			Name    string `json:"name"`
			Entries []struct {
				PagePath   string `json:"pagePath"`
				RevisionID string `json:"revisionId"`
			} `json:"entries"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if len(got.Versions) != 1 || got.Versions[0].Name != "v1" ||
		len(got.Versions[0].Entries) != 1 ||
		got.Versions[0].Entries[0].RevisionID != "r1" ||
		got.Versions[0].Entries[0].PagePath != "intro" {
		t.Fatalf("unexpected manifest: %s", rec.Body.String())
	}
}

func TestReconcileManifest_DriftReturnsConflictAndKeepsState(t *testing.T) {
	engine, revs := newTestRouter(t)
	body := `{"versions":[{"name":"v1","pages":["intro"]}]}`

	if rec := doRequest(t, engine, http.MethodPut, "/api/admin/release-checkpoints/manifest", body); rec.Code != http.StatusOK {
		t.Fatalf("initial apply status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Page edited: latest revision moves.
	revs.latest["p1"] = &revision.Revision{ID: "r2", ContentHash: "h2"}
	rec := doRequest(t, engine, http.MethodPut, "/api/admin/release-checkpoints/manifest", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("drift status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code string   `json:"code"`
			Args []string `json:"args"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if errResp.Error.Code != releasecheckpoints.ErrCodeDrift {
		t.Fatalf("code = %q, want %q", errResp.Error.Code, releasecheckpoints.ErrCodeDrift)
	}
	if len(errResp.Error.Args) != 2 || errResp.Error.Args[0] != "v1" || errResp.Error.Args[1] != "intro" {
		t.Fatalf("args = %v", errResp.Error.Args)
	}

	// Old checkpoint still pins r1.
	rec = doRequest(t, engine, http.MethodGet, "/api/admin/release-checkpoints", "")
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"revisionId":"r1"`)) {
		t.Fatalf("expected r1 checkpoint to survive rejected apply: %s", rec.Body.String())
	}
}

func TestReconcileManifest_PageNotFound(t *testing.T) {
	engine, _ := newTestRouter(t)
	rec := doRequest(t, engine, http.MethodPut, "/api/admin/release-checkpoints/manifest",
		`{"versions":[{"name":"v1","pages":["missing"]}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(releasecheckpoints.ErrCodePageNotFound)) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestReconcileManifest_InvalidJSON(t *testing.T) {
	engine, _ := newTestRouter(t)
	rec := doRequest(t, engine, http.MethodPut, "/api/admin/release-checkpoints/manifest", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(releasecheckpoints.ErrCodeInvalidPayload)) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestReconcileManifest_RevisionDisabledConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := releasecheckpoints.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pages := &fakePageLookup{pages: map[string]*tree.Page{
		"intro": {PageNode: &tree.PageNode{ID: "p1", Slug: "intro"}},
	}}
	svc := releasecheckpoints.NewService(store, pages, nil, nil) // revision lookup disabled
	routes := NewRoutes(RoutesConfig{
		GetManifest: NewGetManifestUseCase(svc),
		Reconcile:   NewReconcileUseCase(svc),
	})
	engine := gin.New()
	engine.PUT("/api/admin/release-checkpoints/manifest", routes.handleReconcile)

	rec := doRequest(t, engine, http.MethodPut, "/api/admin/release-checkpoints/manifest",
		`{"versions":[{"name":"v1","pages":["intro"]}]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
}
