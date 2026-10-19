package tagmerge

import (
	"reflect"
	"sort"
	"testing"
)

func compileOrFail(t *testing.T, cfg Config) *Compiled {
	t.Helper()
	c, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	return c
}

func TestCompile_DirectMapping(t *testing.T) {
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js", "JS"}},
	}})
	got := c.ApplyTags([]string{"js", "node"})
	want := []string{"javascript", "node"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyTags = %v, want %v", got, want)
	}
}

func TestCompile_ResolvesChainsToTerminal(t *testing.T) {
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "b", Aliases: []string{"a"}},
		{Canonical: "c", Aliases: []string{"b"}},
	}})
	got := c.ApplyTags([]string{"a", "b", "x"})
	want := []string{"c", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyTags = %v, want %v", got, want)
	}
}

func TestCompile_DedupsMappedTagsFirstWins(t *testing.T) {
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "go", Aliases: []string{"golang"}},
	}})
	got := c.ApplyTags([]string{"golang", "Go", "rust", "RUST"})
	want := []string{"go", "rust"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyTags = %v, want %v", got, want)
	}
}

func TestCompile_ConflictingAliasAggregatesAndAborts(t *testing.T) {
	_, err := Compile(Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js"}},
		{Canonical: "frontend", Aliases: []string{"js"}},
	}})
	if err == nil {
		t.Fatal("expected CompileError, got nil")
	}
	ce, ok := err.(*CompileError)
	if !ok {
		t.Fatalf("error type = %T, want *CompileError", err)
	}
	if len(ce.Conflicts) != 1 || ce.Conflicts[0].Alias != "js" {
		t.Fatalf("conflicts = %#v", ce.Conflicts)
	}
	if got := ce.Conflicts[0].Canonicals; !reflect.DeepEqual(got, []string{"frontend", "javascript"}) {
		t.Fatalf("canonicals = %v", got)
	}
}

func TestCompile_CycleDetected(t *testing.T) {
	_, err := Compile(Config{Rules: []Rule{
		{Canonical: "b", Aliases: []string{"a"}},
		{Canonical: "a", Aliases: []string{"b"}},
	}})
	if err == nil {
		t.Fatal("expected CompileError, got nil")
	}
	ce := err.(*CompileError)
	if len(ce.Cycles) != 1 {
		t.Fatalf("cycles = %#v", ce.Cycles)
	}
	got := append([]string{}, ce.Cycles[0]...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("cycle members = %v", got)
	}
}

func TestCompile_SelfEdgeDropped(t *testing.T) {
	c := compileOrFail(t, Config{Rules: []Rule{
		{Canonical: "go", Aliases: []string{"go", "golang"}},
	}})
	got := c.ApplyTags([]string{"go", "golang", "Go"})
	want := []string{"go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyTags = %v, want %v", got, want)
	}
}

func TestNormalizeConfig(t *testing.T) {
	cfg := Config{Rules: []Rule{
		{Canonical: "  Go ", Aliases: []string{" Golang ", "golang", "go", " "}},
		{Canonical: "", Aliases: []string{"x"}},
		{Canonical: "y", Aliases: nil},
	}}
	NormalizeConfig(&cfg)
	want := Config{Rules: []Rule{
		{Canonical: "go", Aliases: []string{"golang"}},
	}}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("cfg = %#v, want %#v", cfg, want)
	}
}

func TestValidateConfig_Caps(t *testing.T) {
	if err := ValidateConfig(Config{}); err != nil {
		t.Fatalf("empty config rejected: %v", err)
	}
	long := make([]string, MaxAliasesPerRule+1)
	for i := range long {
		long[i] = "a"
	}
	if err := ValidateConfig(Config{Rules: []Rule{{Canonical: "x", Aliases: long}}}); err == nil {
		t.Fatal("expected too-many-aliases error")
	}
	big := make([]Rule, MaxRules+1)
	for i := range big {
		big[i] = Rule{Canonical: "c", Aliases: []string{"a"}}
	}
	if err := ValidateConfig(Config{Rules: big}); err == nil {
		t.Fatal("expected too-many-rules error")
	}
}
