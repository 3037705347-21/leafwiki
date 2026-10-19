package tagmerge

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestService_SaveLoadReload(t *testing.T) {
	dir := t.TempDir()

	svc, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(svc.Rules().Rules) != 0 {
		t.Fatalf("missing file should yield empty rules, got %#v", svc.Rules())
	}

	cfg := Config{Rules: []Rule{
		{Canonical: " JavaScript ", Aliases: []string{" JS ", "js"}},
	}}
	if err := svc.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := Config{Rules: []Rule{
		{Canonical: "javascript", Aliases: []string{"js"}},
	}}
	if !reflect.DeepEqual(svc.Rules(), want) {
		t.Fatalf("rules after save = %#v, want %#v", svc.Rules(), want)
	}

	// A fresh instance reads the same file from disk.
	svc2, err := New(dir)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if !reflect.DeepEqual(svc2.Rules(), want) {
		t.Fatalf("reloaded rules = %#v, want %#v", svc2.Rules(), want)
	}

	// The file lands flat in the data dir with the contracted name.
	if _, err := os.Stat(filepath.Join(dir, RulesFilename)); err != nil {
		t.Fatalf("rules file not persisted: %v", err)
	}
}

func TestService_SaveRejectsOversizedRules(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	big := make([]Rule, MaxRules+1)
	for i := range big {
		big[i] = Rule{Canonical: "c", Aliases: []string{"a"}}
	}
	if err := svc.Save(Config{Rules: big}); err == nil {
		t.Fatal("expected Save to reject oversized config")
	}
}
