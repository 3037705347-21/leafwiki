// Package tagmerge owns the tag merge rules: admin-authored mappings from
// old tags (aliases, case-insensitive) to one canonical tag, the validation
// and compilation of those rules, and the per-file planning that rewrites
// only the tags value of a page's frontmatter. It contains no HTTP code and
// no orchestration; the internal/wiki/tagmerge slice drives batch applies.
package tagmerge

import (
	"fmt"
	"strings"
)

const (
	// MaxRules bounds how many rules one config may carry.
	MaxRules = 500
	// MaxAliasesPerRule bounds aliases under a single canonical tag.
	MaxAliasesPerRule = 100
	// MaxTagLength bounds a single tag's byte length after trimming.
	MaxTagLength = 200
)

// Rule maps every tag in Aliases to Canonical when a merge is applied.
type Rule struct {
	Canonical string   `json:"canonical"`
	Aliases   []string `json:"aliases"`
}

// Config is the on-disk shape of tag-merge-rules.json.
type Config struct {
	Rules []Rule `json:"rules"`
}

func foldTag(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// NormalizeConfig normalizes cfg in place: canonical and alias tags are
// trimmed and folded to lowercase, empty values dropped, aliases within a
// rule deduped, self-edges (alias == its own canonical) removed, and rules
// left without a canonical or aliases dropped. Rule and alias order is
// preserved. Lenient by design — it is also applied as the settings store's
// on-load hook, so it must not reject a hand-edited file; the API path runs
// ValidateConfig first.
func NormalizeConfig(cfg *Config) {
	if cfg == nil {
		return
	}
	normalized := make([]Rule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		canonical := foldTag(rule.Canonical)
		if canonical == "" {
			continue
		}
		seen := make(map[string]struct{}, len(rule.Aliases))
		aliases := make([]string, 0, len(rule.Aliases))
		for _, alias := range rule.Aliases {
			a := foldTag(alias)
			if a == "" || a == canonical {
				continue
			}
			if _, ok := seen[a]; ok {
				continue
			}
			seen[a] = struct{}{}
			aliases = append(aliases, a)
		}
		if len(aliases) == 0 {
			continue
		}
		normalized = append(normalized, Rule{Canonical: canonical, Aliases: aliases})
	}
	cfg.Rules = normalized
}

// ValidateConfig rejects rule sets that exceed the safety caps. It assumes
// the caller will normalize afterwards; it validates the raw incoming
// values so an oversized payload cannot be silently truncated.
func ValidateConfig(cfg Config) error {
	if len(cfg.Rules) > MaxRules {
		return fmt.Errorf("too many tag merge rules: %d > %d", len(cfg.Rules), MaxRules)
	}
	for i, rule := range cfg.Rules {
		if len(rule.Aliases) > MaxAliasesPerRule {
			return fmt.Errorf("rule %d has too many aliases: %d > %d", i, len(rule.Aliases), MaxAliasesPerRule)
		}
		if len(rule.Canonical) > MaxTagLength {
			return fmt.Errorf("rule %d canonical tag exceeds %d bytes", i, MaxTagLength)
		}
		for _, alias := range rule.Aliases {
			if len(alias) > MaxTagLength {
				return fmt.Errorf("rule %d alias tag exceeds %d bytes", i, MaxTagLength)
			}
		}
	}
	return nil
}
