package importer

import "testing"

func TestCanonicalMarkdown(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "body only",
			raw:  "# Title\n\nbody\n",
			want: "# Title\n\nbody\n",
		},
		{
			name: "body trims surrounding blank lines and ensures final newline",
			raw:  "\n\n# Title\nbody\n\n",
			want: "# Title\nbody\n",
		},
		{
			name: "crlf collapsed by parser",
			raw:  "---\ntags:\n  - a\r\n---\r\n# T\r\n\r\nbody\r\n",
			want: "---\ntags:\n    - a\n---\n# T\n\nbody\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalMarkdown(tc.raw)
			if err != nil {
				t.Fatalf("CanonicalMarkdown: %v", err)
			}
			if got != tc.want {
				t.Errorf("canonical = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestManagedContentFingerprint(t *testing.T) {
	base := "---\ntags:\n  - guide\ncustom: keep\n---\n# Heading\n\nbody text\n"

	fp := func(title, raw string) string {
		t.Helper()
		v, err := ManagedContentFingerprint(title, raw)
		if err != nil {
			t.Fatalf("fingerprint: %v", err)
		}
		return v
	}

	// Stable across repeated calls.
	if fp("Title", base) != fp("Title", base) {
		t.Fatal("fingerprint not deterministic")
	}

	// Extra frontmatter key order is irrelevant.
	reordered := "---\ncustom: keep\ntags:\n  - guide\n---\n# Heading\n\nbody text\n"
	if fp("Title", base) != fp("Title", reordered) {
		t.Fatal("frontmatter key order must not change fingerprint")
	}

	// System frontmatter (timestamps, pinning) is excluded: pinning a page
	// or an import-driven metadata touch must not register as drift.
	withSystem := "---\nleafwiki_id: p1\nleafwiki_title: Title\nleafwiki_updated_at: 2026-10-19T08:00:00Z\nleafwiki_pinned: true\ntags:\n  - guide\ncustom: keep\n---\n# Heading\n\nbody text\n"
	if fp("Title", base) != fp("Title", withSystem) {
		t.Fatal("leafwiki_* frontmatter must not change fingerprint")
	}

	// Title is a separate term.
	if fp("Title", base) == fp("Renamed", base) {
		t.Fatal("title change must change fingerprint")
	}

	// Body drift is detected.
	edited := "---\ntags:\n  - guide\ncustom: keep\n---\n# Heading\n\nbody text edited in the wiki\n"
	if fp("Title", base) == fp("Title", edited) {
		t.Fatal("body change must change fingerprint")
	}

	// Editor-owned string property / tag changes are drift.
	tagsChanged := "---\ntags:\n  - other\ncustom: keep\n---\n# Heading\n\nbody text\n"
	if fp("Title", base) == fp("Title", tagsChanged) {
		t.Fatal("tags change must change fingerprint")
	}
	propChanged := "---\ntags:\n  - guide\ncustom: changed\n---\n# Heading\n\nbody text\n"
	if fp("Title", base) == fp("Title", propChanged) {
		t.Fatal("extra property change must change fingerprint")
	}
}
