package tree

import (
	"os"
	"path/filepath"
	"testing"
)

func testPageNode() *PageNode {
	root := &PageNode{ID: "root", Slug: "root", Title: "root", Kind: NodeKindSection}
	page := &PageNode{ID: "p1", Slug: "page-one", Title: "Page One", Kind: NodeKindPage, Parent: root}
	return page
}

func TestNodeStore_WriteRaw_WritesBytesAndPreservesMode(t *testing.T) {
	tmp := t.TempDir()
	store := NewNodeStore(tmp)
	entry := testPageNode()

	filePath := filepath.Join(tmp, "root", "page-one.md")
	mustWriteFile(t, filePath, "---\ntags:\n  - js\n---\nold body", 0o600)

	newRaw := "---\ntags:\n  - javascript\n---\nold body"
	if err := store.WriteRaw(entry, newRaw); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != newRaw {
		t.Fatalf("file contents = %q, want %q", string(got), newRaw)
	}

	st, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 0600", st.Mode().Perm())
	}
}

func TestNodeStore_WriteRaw_MissingFileIsDrift(t *testing.T) {
	tmp := t.TempDir()
	store := NewNodeStore(tmp)

	err := store.WriteRaw(testPageNode(), "whatever")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, ok := err.(*DriftError); !ok {
		t.Fatalf("error = %T, want *DriftError", err)
	}
}

func TestNodeStore_WriteRaw_NilEntry(t *testing.T) {
	store := NewNodeStore(t.TempDir())
	if err := store.WriteRaw(nil, "x"); err == nil {
		t.Fatal("expected error for nil entry")
	}
}
