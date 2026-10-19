package tagmerge

import (
	"fmt"

	"github.com/perber/wiki/internal/core/markdown"
	"github.com/perber/wiki/internal/tags"
)

// PlanTagsRewrite computes the post-merge raw markdown for one page.
//
//   - changed: the tags value was rewritten (caller should persist out).
//   - unsupported: tags exist and need merging but the frontmatter cannot
//     be edited safely; the page must be counted and left untouched.
//
// The rewritten content is re-parsed and verified to carry exactly the
// desired tag set before it is returned, so a splice surprise can never
// reach disk.
func PlanTagsRewrite(raw string, compiled *Compiled) (out string, changed bool, unsupported bool, err error) {
	old := tags.ExtractTagsFromContent(raw)
	if len(old) == 0 {
		return raw, false, false, nil
	}

	desired := compiled.ApplyTags(old)
	if equalTags(old, desired) {
		return raw, false, false, nil
	}

	out, kind, err := markdown.RewriteFrontmatterTags(raw, desired)
	if err != nil {
		return raw, false, false, fmt.Errorf("rewrite tags: %w", err)
	}
	switch kind {
	case markdown.TagsSpanBlock, markdown.TagsSpanFlow:
		// Verify before trusting the splice.
		back := tags.ExtractTagsFromContent(out)
		if !equalTags(back, desired) {
			return raw, false, false, fmt.Errorf("tag rewrite verification mismatch: got %v want %v", back, desired)
		}
		return out, true, false, nil
	default:
		// ExtractTagsFromContent recognized tags that the strict rewriter
		// refuses to touch (scalar value, mixed-type list, repaired YAML…).
		return raw, false, true, nil
	}
}

func equalTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
