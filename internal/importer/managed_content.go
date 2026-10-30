package importer

import (
	"fmt"
	"path/filepath"

	"github.com/perber/wiki/internal/core/markdown"
	"github.com/perber/wiki/internal/core/tree"
)

// managedContent bundles the three link/asset transformers a managed
// reconcile needs:
//
//   - token: read-only planning view of the bundle (assets become
//     asset://sha256 tokens, nothing is uploaded),
//   - live:  the same token view of an already-imported page (existing
//     /assets/<id>/<name> hrefs are content-addressed),
//   - upload: execution view that rewrites links and dedupes asset uploads
//     by content hash before the page is written.
type managedContent struct {
	sourceBasePath string
	token          *contentTransformer
	live           *contentTransformer
	upload         *contentTransformer
	wiki           ImporterWiki
}

func newManagedContent(plan *PlanResult, sourceBasePath string, assetMaxBytes int64, wiki ImporterWiki) *managedContent {
	return &managedContent{
		sourceBasePath: sourceBasePath,
		token: newContentTransformerWithResolver(
			plan, sourceBasePath, newTokenAssetResolver(sourceBasePath)),
		live: newContentTransformerWithResolver(
			plan, "", newLiveAssetResolver(wiki)),
		upload: newContentTransformerWithResolver(
			plan, sourceBasePath, newUploadAssetResolver(sourceBasePath, assetMaxBytes, true)),
		wiki: wiki,
	}
}

// sourceMarkdown loads a bundle file and renders it as importable markdown
// (source extra frontmatter preserved, body unchanged).
func sourceMarkdown(sourceBasePath, sourceRel string) (string, error) {
	sourceAbs := filepath.Join(sourceBasePath, filepath.FromSlash(sourceRel))
	mdFile, err := markdown.LoadMarkdownFile(sourceAbs)
	if err != nil {
		return "", fmt.Errorf("load source %q: %w", sourceRel, err)
	}
	rendered, err := buildImportedContent(mdFile)
	if err != nil {
		return "", fmt.Errorf("build content %q: %w", sourceRel, err)
	}
	return rendered, nil
}

// DesiredFingerprint is the content-addressed fingerprint of what the bundle
// wants this page to contain. Planning only: no wiki writes.
func (m *managedContent) DesiredFingerprint(sourceRel, title string, kind tree.NodeKind) (string, error) {
	imported, err := sourceMarkdown(m.sourceBasePath, sourceRel)
	if err != nil {
		return "", err
	}
	placeholder := &tree.Page{PageNode: &tree.PageNode{Kind: kind}}
	transformed, err := m.token.TransformContent("system", sourceRel, placeholder, imported, m.wiki)
	if err != nil {
		return "", fmt.Errorf("transform %q: %w", sourceRel, err)
	}
	return ManagedContentFingerprint(title, transformed)
}

// CurrentFingerprint fingerprints a live page with its asset hrefs normalized
// to the same content-addressed tokens the bundle side uses.
func (m *managedContent) CurrentFingerprint(page *tree.Page, sourceRel string) (string, error) {
	transformed, err := m.live.TransformContent("system", sourceRel, page, page.RawContent, m.wiki)
	if err != nil {
		return "", err
	}
	return ManagedContentFingerprint(page.Title, transformed)
}

// WriteContent renders the markdown actually passed to UpdatePage, uploading
// (or reusing by hash) referenced assets.
func (m *managedContent) WriteContent(userID, sourceRel string, page *tree.Page) (string, error) {
	imported, err := sourceMarkdown(m.sourceBasePath, sourceRel)
	if err != nil {
		return "", err
	}
	return m.upload.TransformContent(userID, sourceRel, page, imported, m.wiki)
}
