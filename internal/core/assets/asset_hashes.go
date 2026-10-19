package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/perber/wiki/internal/core/tree"
)

// hashFile returns the lowercase hex SHA-256 of the file at path.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AssetHashesForPageID maps each asset filename of a page to the lowercase
// hex SHA-256 of its contents. A missing asset directory yields an empty map
// (pages without assets are the common case during import planning).
func (s *AssetService) AssetHashesForPageID(pageID string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pagePath := assetPageDiskPath(s.assetsDir, pageID)
	entries, err := os.ReadDir(pagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read asset directory for page %q: %w", pageID, err)
	}

	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sum, err := hashFile(assetFileDiskPath(pagePath, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("hash asset %q: %w", entry.Name(), err)
		}
		out[entry.Name()] = sum
	}
	return out, nil
}

// FindAssetByHash returns the public path of an existing asset of pageID
// whose contents hash to hashHex (lowercase hex SHA-256). The boolean is
// false when no such asset exists. It lets content-addressed importers skip
// re-uploading identical files, which otherwise collide into suffixed
// copies on every replay after a crash.
func (s *AssetService) FindAssetByHash(pageID string, hashHex string) (string, bool, error) {
	hashes, err := s.AssetHashesForPageID(pageID)
	if err != nil {
		return "", false, err
	}
	for filename, sum := range hashes {
		if sum == hashHex {
			// buildPublicPath only reads the node ID.
			return s.buildPublicPath(&tree.PageNode{ID: pageID}, filename), true, nil
		}
	}
	return "", false, nil
}
