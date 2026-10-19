package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/perber/wiki/internal/core/tree"
)

// assetTokenScheme prefixes content-addressed asset references used while
// planning and fingerprinting: asset://sha256/<lowercase hex>. The same token
// is derived from the bundle file and from a previously imported /assets/
// href, so fingerprints match regardless of the stored filename.
const assetTokenScheme = "asset://sha256/"

func assetHashToken(hashHex string) string {
	return assetTokenScheme + hashHex
}

// hashFileSHA256 returns the lowercase hex SHA-256 of a local file.
func hashFileSHA256(absPath string) (string, error) {
	f, err := os.Open(absPath)
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

// assetResolver maps a local asset reference in source content to the href
// to emit. Returning isAsset=false leaves the destination untouched.
type assetResolver interface {
	ResolveHref(userID, sourcePath string, page *tree.Page, href string, wiki ImporterWiki) (resolved string, isAsset bool, err error)
}

// uploadAssetResolver resolves workspace asset files by uploading them to the
// page's asset folder (legacy behavior). With dedupeByHash enabled (managed
// execution) an identical existing asset is reused first, so replaying an
// item after a crash cannot produce a second suffixed copy.
type uploadAssetResolver struct {
	sourceBasePath string
	assetMaxBytes  int64
	dedupeByHash   bool
	cache          map[string]string
}

func newUploadAssetResolver(sourceBasePath string, assetMaxBytes int64, dedupeByHash bool) *uploadAssetResolver {
	return &uploadAssetResolver{
		sourceBasePath: sourceBasePath,
		assetMaxBytes:  assetMaxBytes,
		dedupeByHash:   dedupeByHash,
		cache:          map[string]string{},
	}
}

func (r *uploadAssetResolver) ResolveHref(userID, sourcePath string, page *tree.Page, href string, wiki ImporterWiki) (string, bool, error) {
	assetAbs, ok := resolveAssetPath(r.sourceBasePath, sourcePath, href)
	if !ok {
		return "", false, nil
	}

	cacheKey := page.ID + "::" + assetAbs
	if cached, ok := r.cache[cacheKey]; ok {
		return cached, true, nil
	}

	if r.dedupeByHash {
		sum, err := hashFileSHA256(assetAbs)
		if err != nil {
			return "", false, fmt.Errorf("hash asset %q: %w", assetAbs, err)
		}
		if existing, found, err := wiki.FindAssetByHash(userID, page.ID, sum); err != nil {
			return "", false, fmt.Errorf("lookup asset hash %q: %w", assetAbs, err)
		} else if found {
			r.cache[cacheKey] = existing
			return existing, true, nil
		}
	}

	file, err := os.Open(assetAbs)
	if err != nil {
		return "", false, fmt.Errorf("open asset %q: %w", assetAbs, err)
	}
	defer func() { _ = file.Close() }()

	publicPath, err := wiki.UploadAsset(userID, page.ID, multipart.File(file), filepath.Base(assetAbs), r.assetMaxBytes)
	if err != nil {
		return "", false, fmt.Errorf("upload asset %q: %w", assetAbs, err)
	}

	r.cache[cacheKey] = publicPath
	return publicPath, true, nil
}

// tokenAssetResolver is the side-effect-free resolver used at plan time: it
// maps workspace asset files to content-addressed tokens without touching the
// wiki. The same tokens are derived from stored assets by liveAssetResolver.
type tokenAssetResolver struct {
	sourceBasePath string
	cache          map[string]string
}

func newTokenAssetResolver(sourceBasePath string) *tokenAssetResolver {
	return &tokenAssetResolver{sourceBasePath: sourceBasePath, cache: map[string]string{}}
}

func (r *tokenAssetResolver) ResolveHref(_ string, sourcePath string, page *tree.Page, href string, _ ImporterWiki) (string, bool, error) {
	assetAbs, ok := resolveAssetPath(r.sourceBasePath, sourcePath, href)
	if !ok {
		return "", false, nil
	}

	cacheKey := page.ID + "::" + assetAbs
	if cached, ok := r.cache[cacheKey]; ok {
		return cached, true, nil
	}

	sum, err := hashFileSHA256(assetAbs)
	if err != nil {
		return "", false, fmt.Errorf("hash asset %q: %w", assetAbs, err)
	}
	token := assetHashToken(sum)
	r.cache[cacheKey] = token
	return token, true, nil
}

// liveAssetResolver rewrites hrefs the importer previously emitted as
// /assets/<pageID>/<name> back into content-addressed tokens, using the asset
// files' current hashes. Anything not owned by the page (external URLs, page
// links, anchors) passes through untouched.
type liveAssetResolver struct {
	wiki      ImporterWiki
	pageCache map[string]map[string]string
}

func newLiveAssetResolver(wiki ImporterWiki) *liveAssetResolver {
	return &liveAssetResolver{wiki: wiki, pageCache: map[string]map[string]string{}}
}

func (r *liveAssetResolver) ResolveHref(_ string, _ string, page *tree.Page, href string, _ ImporterWiki) (string, bool, error) {
	prefix := "/assets/" + page.ID + "/"
	if !strings.HasPrefix(href, prefix) {
		return "", false, nil
	}
	name := strings.TrimPrefix(href, prefix)
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", false, nil
	}

	hashes, ok := r.pageCache[page.ID]
	if !ok {
		var err error
		hashes, err = r.wiki.AssetHashesForPage(page.ID)
		if err != nil {
			return "", false, fmt.Errorf("read asset hashes for page %q: %w", page.ID, err)
		}
		r.pageCache[page.ID] = hashes
	}

	if sum, ok := hashes[name]; ok {
		return assetHashToken(sum), true, nil
	}
	return "", false, nil
}
