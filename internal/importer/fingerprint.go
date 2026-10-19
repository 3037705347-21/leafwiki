package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/perber/wiki/internal/core/markdown"
)

// managedFingerprintVersion is the fingerprint format. Bump if canonicalization
// changes; stale fingerprints then behave as "source changed" on the next plan.
const managedFingerprintVersion = "managed-content/v1"

// canonicalBody normalizes a markdown body for fingerprinting: CRLF has
// already been collapsed by the frontmatter parser, so only surrounding blank
// lines and the final newline are canonical.
func canonicalBody(body string) string {
	return strings.TrimSpace(body) + "\n"
}

// CanonicalMarkdown renders raw markdown in the deterministic form used for
// fingerprinting: system frontmatter (leafwiki_*) is structurally dropped by
// the parser, extra fields round-trip through the sorted YAML builder used on
// write, and the body gets one canonical trailing newline.
func CanonicalMarkdown(raw string) (string, error) {
	fm, body, has, err := markdown.ParseFrontmatter(raw)
	if err != nil {
		return "", err
	}
	canonical := canonicalBody(body)
	if !has || len(fm.ExtraFields) == 0 {
		return canonical, nil
	}
	return markdown.BuildMarkdownWithExtraFrontmatter(fm.ExtraFields, canonical)
}

// ManagedContentFingerprint hashes canonicalized title plus markdown content.
// Title is a separate domain (stored in leafwiki_title, not the body), so a
// title-only change must still register as a change. The same function is
// applied to:
//   - bundle content transformed with hash-token assets (desired state), and
//   - a live page's raw content with /assets/ hrefs turned into the same
//     tokens (current state)
//
// making drift detection independent of asset filenames and system
// frontmatter (including pinning).
func ManagedContentFingerprint(title string, rawMarkdown string) (string, error) {
	canonical, err := CanonicalMarkdown(rawMarkdown)
	if err != nil {
		return "", err
	}

	h := sha256.New()
	h.Write([]byte(managedFingerprintVersion))
	h.Write([]byte{'\n'})
	h.Write([]byte(strings.TrimSpace(title)))
	h.Write([]byte{0})
	h.Write([]byte(canonical))

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
