package tagmerge

import (
	"fmt"
	"sort"
	"strings"
)

// Conflict describes one folded alias that two different canonical tags
// claim. Applying such a rule set is ambiguous, so the whole batch aborts.
type Conflict struct {
	Alias      string   `json:"alias"`
	Canonicals []string `json:"canonicals"`
}

// CompileError aggregates every conflict and cycle found in a rule set so
// the UI can display all problems in one pass.
type CompileError struct {
	Conflicts []Conflict `json:"conflicts"`
	Cycles    [][]string `json:"cycles"`
}

func (e *CompileError) Error() string {
	return fmt.Sprintf("tag merge rules contain %d conflict(s) and %d cycle(s)", len(e.Conflicts), len(e.Cycles))
}

// Compiled is a conflict-free, chain-resolved mapping from every folded
// alias directly to its terminal canonical tag.
type Compiled struct {
	resolved map[string]string
}

// Compile normalizes a copy of cfg, builds the direct alias→canonical edges,
// aggregates ambiguous aliases and cycles, and — when neither exists —
// resolves every edge to its terminal canonical so chains (a→b, b→c) fold
// in a single pass. Rule input must pass ValidateConfig beforehand.
func Compile(cfg Config) (*Compiled, error) {
	NormalizeConfig(&cfg)

	// First-seen edge wins for the graph used in cycle detection; all
	// competing targets are still reported as conflicts.
	edges := make(map[string]string)
	aliasOrder := make([]string, 0)
	targets := make(map[string]map[string]struct{})

	addTarget := func(alias, canonical string) {
		set, ok := targets[alias]
		if !ok {
			set = make(map[string]struct{})
			targets[alias] = set
			aliasOrder = append(aliasOrder, alias)
		}
		set[canonical] = struct{}{}
	}

	for _, rule := range cfg.Rules {
		for _, alias := range rule.Aliases {
			addTarget(alias, rule.Canonical)
			if _, exists := edges[alias]; !exists {
				edges[alias] = rule.Canonical
			}
		}
	}

	conflicts := make([]Conflict, 0)
	for _, alias := range aliasOrder {
		set := targets[alias]
		if len(set) <= 1 {
			continue
		}
		canonicals := make([]string, 0, len(set))
		for c := range set {
			canonicals = append(canonicals, c)
		}
		sort.Strings(canonicals)
		conflicts = append(conflicts, Conflict{Alias: alias, Canonicals: canonicals})
	}

	cycles := detectCycles(edges)

	if len(conflicts) > 0 || len(cycles) > 0 {
		return nil, &CompileError{Conflicts: conflicts, Cycles: cycles}
	}

	// Resolve each edge to its terminal by following the chain.
	// Conflict- and cycle-free input guarantees termination: any node with
	// an outgoing edge leads either to a terminal or into a repeat, which
	// would be a cycle and was rejected above.
	resolved := make(map[string]string, len(edges))
	for alias := range edges {
		cur := alias
		for {
			next, ok := edges[cur]
			if !ok {
				resolved[alias] = cur
				break
			}
			cur = next
		}
	}

	return &Compiled{resolved: resolved}, nil
}

// detectCycles returns the distinct node sets participating in a cycle,
// each sorted and deduplicated.
func detectCycles(edges map[string]string) [][]string {
	seenCycle := make(map[string]struct{})
	cycles := make([][]string, 0)

	// Deterministic iteration order.
	starts := make([]string, 0, len(edges))
	for node := range edges {
		starts = append(starts, node)
	}
	sort.Strings(starts)

	for _, start := range starts {
		pathIndex := make(map[string]int)
		path := make([]string, 0)
		cur := start
		for {
			if idx, repeated := pathIndex[cur]; repeated {
				members := append([]string{}, path[idx:]...)
				sort.Strings(members)
				key := strings.Join(members, ",")
				if _, ok := seenCycle[key]; !ok {
					seenCycle[key] = struct{}{}
					cycles = append(cycles, members)
				}
				break
			}
			next, ok := edges[cur]
			if !ok {
				break
			}
			pathIndex[cur] = len(path)
			path = append(path, cur)
			cur = next
		}
	}
	return cycles
}

// ApplyTags maps each tag to its terminal canonical when an edge exists,
// keeps unmapped tags as-is, and dedupes the folded result with first
// occurrence winning.
func (c *Compiled) ApplyTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, raw := range tags {
		t := foldTag(raw)
		if t == "" {
			continue
		}
		if canonical, ok := c.resolved[t]; ok {
			t = canonical
		}
		if _, exists := seen[t]; exists {
			continue
		}
		seen[t] = struct{}{}
		result = append(result, t)
	}
	return result
}
