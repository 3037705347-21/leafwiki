package importer

import (
	"mime/multipart"

	"github.com/perber/wiki/internal/core/tree"
)

type ImporterWiki interface {
	TreeHash() string
	LookupPagePath(path string) (*tree.PathLookup, error)
	EnsurePath(userID string, targetPath string, title string, kind *tree.NodeKind) (*tree.Page, error)
	UpdatePage(userID string, id, title, slug string, content *string, kind *tree.NodeKind) (*tree.Page, error)
	UploadAsset(userID, pageID string, file multipart.File, filename string, maxBytes int64) (string, error)
	// FindAssetByHash returns the public URL of an existing page asset whose
	// contents match the lowercase hex SHA-256, enabling idempotent replays.
	FindAssetByHash(userID, pageID, hashHex string) (string, bool, error)
	// AssetHashesForPage maps asset filename -> lowercase hex SHA-256 of its
	// contents, used to normalize already-imported asset hrefs into the same
	// content-addressed form fingerprints use on the bundle side.
	AssetHashesForPage(pageID string) (map[string]string, error)
}
