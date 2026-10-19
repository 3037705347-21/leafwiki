package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/test_utils"
)

func TestAssetHashesForPageID_AndFindByHash(t *testing.T) {
	tmp := t.TempDir()
	service := NewAssetService(tmp, tree.NewSlugService())
	page := &tree.PageNode{ID: "p-hash", Slug: "page", Kind: tree.NodeKindPage}

	png := []byte("png-content")
	sum := sha256.Sum256(png)
	pngHash := hex.EncodeToString(sum[:])

	file, _, err := test_utils.CreateMultipartFile("logo.png", png)
	if err != nil {
		t.Fatalf("create multipart: %v", err)
	}
	defer func() { _ = file.Close() }()
	url, err := service.SaveAssetForPage(page, file, "logo.png", testAssetMaxBytes)
	if err != nil {
		t.Fatalf("save asset: %v", err)
	}
	if url != "/assets/p-hash/logo.png" {
		t.Fatalf("url = %q", url)
	}

	hashes, err := service.AssetHashesForPageID("p-hash")
	if err != nil {
		t.Fatalf("hashes: %v", err)
	}
	if len(hashes) != 1 || hashes["logo.png"] != pngHash {
		t.Fatalf("hashes = %#v, want logo.png->%s", hashes, pngHash)
	}

	found, ok, err := service.FindAssetByHash("p-hash", pngHash)
	if err != nil || !ok {
		t.Fatalf("find by hash ok=%v err=%v", ok, err)
	}
	if found != "/assets/p-hash/logo.png" {
		t.Fatalf("found = %q", found)
	}
	if _, ok, err := service.FindAssetByHash("p-hash", "deadbeef"); err != nil || ok {
		t.Fatalf("unknown hash ok=%v err=%v", ok, err)
	}

	// A page without an asset directory yields an empty map, not an error.
	missing, err := service.AssetHashesForPageID("no-such-page")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing page hashes=%v err=%v", missing, err)
	}
}
