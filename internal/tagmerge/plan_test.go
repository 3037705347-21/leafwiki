package tagmerge

import (
	"strings"
	"testing"

	"github.com/perber/wiki/internal/tags"
)

func TestPlanTagsRewrite_BlockChange(t *testing.T) {
	raw := "---\ntitle: keep\ntags:\n  - JS\n  - Node\n---\nbody stays\n"
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js"}},
	}})
	out, changed, unsupported, err := PlanTagsRewrite(raw, c)
	if err != nil || !changed || unsupported {
		t.Fatalf("changed=%v unsupported=%v err=%v", changed, unsupported, err)
	}
	if !strings.Contains(out, "title: keep") || !strings.Contains(out, "body stays") {
		t.Fatalf("unrelated content changed: %q", out)
	}
	if strings.Contains(out, "- JS") || strings.Contains(out, "- js") {
		t.Fatalf("old tag still present: %q", out)
	}
	if !strings.Contains(out, "- javascript") {
		t.Fatalf("canonical tag missing: %q", out)
	}
}

func TestPlanTagsRewrite_FlowCaseVariant(t *testing.T) {
	raw := "---\ntags: [JS, GO]\n---\n"
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js"}},
	}})
	out, changed, _, err := PlanTagsRewrite(raw, c)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got := tags.ExtractTagsFromContent(out)
	want := []string{"javascript", "go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}

func TestPlanTagsRewrite_NoTagsNoChange(t *testing.T) {
	raw := "---\ntitle: x\n---\nbody"
	c := compileOrFail(t, Config{Rules: []Rule{{Canonical: "a", Aliases: []string{"b"}}}})
	out, changed, unsupported, err := PlanTagsRewrite(raw, c)
	if err != nil || changed || unsupported || out != raw {
		t.Fatalf("out=%q changed=%v unsupported=%v err=%v", out, changed, unsupported, err)
	}
}

func TestPlanTagsRewrite_ConvergedNoChange(t *testing.T) {
	raw := "---\ntags:\n  - javascript\n  - node\n---\n"
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js"}},
	}})
	out, changed, _, err := PlanTagsRewrite(raw, c)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v out=%q", changed, err, out)
	}
}

func TestPlanTagsRewrite_UnsupportedMixedTypeSkipped(t *testing.T) {
	// ExtractTagsFromContent reads "js" from [js, 1]; the strict rewriter
	// refuses the mixed-type list, so the page is flagged unsupported and
	// left byte-identical.
	raw := "---\ntags: [js, 1]\n---\nbody"
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js"}},
	}})
	out, changed, unsupported, err := PlanTagsRewrite(raw, c)
	if err != nil || changed || !unsupported {
		t.Fatalf("changed=%v unsupported=%v err=%v", changed, unsupported, err)
	}
	if out != raw {
		t.Fatalf("unsupported file was modified: %q", out)
	}
}

func TestPlanTagsRewrite_RerunAfterChangeIsNoOp(t *testing.T) {
	raw := "---\ntags:\n  - js\n---\n"
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js"}},
	}})
	out, changed, _, err := PlanTagsRewrite(raw, c)
	if err != nil || !changed {
		t.Fatalf("first run changed=%v err=%v", changed, err)
	}
	out2, changed2, unsupported2, err := PlanTagsRewrite(out, c)
	if err != nil || changed2 || unsupported2 {
		t.Fatalf("rerun changed=%v unsupported=%v err=%v", changed2, unsupported2, err)
	}
	if out2 != out {
		t.Fatal("rerun modified converged content")
	}
}
