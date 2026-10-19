package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/perber/wiki/internal/core/tree"
)

func planWithItem(sourcePath, targetPath string, kind tree.NodeKind) *PlanResult {
	return &PlanResult{
		Items: []PlanItem{{SourcePath: sourcePath, TargetPath: targetPath, Kind: kind}},
	}
}

// The same content fingerprint must result from (a) bundle content tokenized
// at plan time and (b) the previously imported /assets/ hrefs tokenized from
// live page content.
func TestAssetTokenEquivalence_BundleVsLive(t *testing.T) {
	ws := t.TempDir()
	pngBytes := []byte("not-a-real-png-but-stable")
	mustWrite(t, ws, "images/logo.png", string(pngBytes))
	sum := sha256.Sum256(pngBytes)
	hashHex := hex.EncodeToString(sum[:])

	sourceContent := "# Home\n\n![logo](images/logo.png)\n\n[doc](doc.md)\n"
	storedContent := "# Home\n\n![logo](/assets/p1/logo.png)\n\n[doc](/docs/doc)\n"

	plan := planWithItem("doc.md", "docs/doc", tree.NodeKindPage)
	page := &tree.Page{PageNode: &tree.PageNode{ID: "p1", Kind: tree.NodeKindPage}}

	bundleTransformer := newContentTransformerWithResolver(plan, ws, newTokenAssetResolver(ws))
	gotBundle, err := bundleTransformer.TransformContent("user1", "home.md", page, sourceContent, nil)
	if err != nil {
		t.Fatalf("bundle transform: %v", err)
	}

	wiki := &fakeWiki{
		assetHashes: map[string]map[string]string{
			"p1": {"logo.png": hashHex},
		},
	}
	liveTransformer := newContentTransformerWithResolver(plan, "", newLiveAssetResolver(wiki))
	gotLive, err := liveTransformer.TransformContent("user1", "home.md", page, storedContent, wiki)
	if err != nil {
		t.Fatalf("live transform: %v", err)
	}

	fpBundle, err := ManagedContentFingerprint("Home", gotBundle)
	if err != nil {
		t.Fatalf("bundle fingerprint: %v", err)
	}
	fpLive, err := ManagedContentFingerprint("Home", gotLive)
	if err != nil {
		t.Fatalf("live fingerprint: %v", err)
	}
	if fpBundle != fpLive {
		t.Fatalf("fingerprints differ:\nbundle=%q\nlive=%q\nbundle-transformed=%q\nlive-transformed=%q",
			fpBundle, fpLive, gotBundle, gotLive)
	}
}

func TestTokenAssetResolver_LocalFileOnly(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, ws, "img.png", "png-bytes")
	page := &tree.Page{PageNode: &tree.PageNode{ID: "p1", Kind: tree.NodeKindPage}}
	r := newTokenAssetResolver(ws)

	// External URLs and page links are left for the page-link resolution /
	// pass-through path; the resolver reports not-an-asset.
	if _, isAsset, err := r.ResolveHref("u", "a.md", page, "https://example.com/x.png", nil); err != nil || isAsset {
		t.Fatalf("external href isAsset=%v err=%v, want false,nil", isAsset, err)
	}
	if _, isAsset, err := r.ResolveHref("u", "a.md", page, "../escape.png", nil); err != nil || isAsset {
		t.Fatalf("path-escape href isAsset=%v err=%v, want false,nil", isAsset, err)
	}
	resolved, isAsset, err := r.ResolveHref("u", "a.md", page, "img.png", nil)
	if err != nil || !isAsset {
		t.Fatalf("local href isAsset=%v err=%v, want true,nil", isAsset, err)
	}
	want := assetHashToken(hashHexFor(t, "png-bytes"))
	if resolved != want {
		t.Fatalf("token = %q, want %q", resolved, want)
	}
	// Second resolution for the same file is cached and identical.
	again, _, err := r.ResolveHref("u", "a.md", page, "img.png", nil)
	if err != nil || again != resolved {
		t.Fatalf("cached resolution = %q, %v", again, err)
	}
}

func TestLiveAssetResolver_OwnPageAssetsOnly(t *testing.T) {
	wiki := &fakeWiki{
		assetHashes: map[string]map[string]string{
			"p1": {"a.png": "aaa"},
		},
	}
	r := newLiveAssetResolver(wiki)
	page := &tree.Page{PageNode: &tree.PageNode{ID: "p1", Kind: tree.NodeKindPage}}

	if got, isAsset, err := r.ResolveHref("u", "a.md", page, "/assets/p1/a.png", wiki); err != nil || !isAsset || got != assetHashToken("aaa") {
		t.Fatalf("own asset got=%q isAsset=%v err=%v", got, isAsset, err)
	}
	// Another page's assets, external URLs and page links pass through.
	if _, isAsset, _ := r.ResolveHref("u", "a.md", page, "/assets/p2/a.png", wiki); isAsset {
		t.Fatal("other page asset must not be tokenized")
	}
	if _, isAsset, _ := r.ResolveHref("u", "a.md", page, "/docs/doc", wiki); isAsset {
		t.Fatal("page path must not be tokenized")
	}
}

// Managed execution reuses an existing identical asset and never uploads.
func TestUploadAssetResolver_DedupeByHash(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, ws, "img.png", "png-bytes")
	page := &tree.Page{PageNode: &tree.PageNode{ID: "p1", Kind: tree.NodeKindPage}}
	wiki := &fakeWiki{
		findAssetURL: map[string]string{
			"p1::" + hashHexFor(t, "png-bytes"): "/assets/p1/img.png",
		},
	}
	r := newUploadAssetResolver(ws, 0, true)
	got, isAsset, err := r.ResolveHref("u", "a.md", page, "img.png", wiki)
	if err != nil || !isAsset {
		t.Fatalf("resolve err=%v isAsset=%v", err, isAsset)
	}
	if got != "/assets/p1/img.png" || wiki.uploadCalls != 0 {
		t.Fatalf("got=%q uploadCalls=%d, want reuse with zero uploads", got, wiki.uploadCalls)
	}
}

func hashHexFor(t *testing.T, content string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
