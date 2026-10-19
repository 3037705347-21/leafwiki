package markdown

import (
	"strings"
	"testing"
)

func TestRewriteFrontmatterTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		newTags  []string
		wantKind TagsSpanKind
		wantOut  string // empty means "must equal input"
		wantErr  bool
	}{
		{
			name:     "no frontmatter",
			input:    "# Title\n\nbody\n",
			newTags:  []string{"a"},
			wantKind: TagsSpanNone,
		},
		{
			name: "block sequence rewrite",
			input: "---\n" +
				"title: Hello\n" +
				"tags:\n" +
				"  - JS\n" +
				"  - node\n" +
				"---\n" +
				"# Body\n",
			newTags:  []string{"javascript", "node"},
			wantKind: TagsSpanBlock,
			wantOut: "---\n" +
				"title: Hello\n" +
				"tags:\n" +
				"  - javascript\n" +
				"  - node\n" +
				"---\n" +
				"# Body\n",
		},
		{
			name: "block sequence preserves deeper indentation",
			input: "---\n" +
				"tags:\n" +
				"    - js\n" +
				"    - go\n" +
				"---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanBlock,
			wantOut: "---\n" +
				"tags:\n" +
				"    - javascript\n" +
				"---\n",
		},
		{
			name: "block keeps key line comment and trailing keys",
			input: "---\n" +
				"tags: # the tags\n" +
				"  - js\n" +
				"author: bob\n" +
				"---\nbody",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanBlock,
			wantOut: "---\n" +
				"tags: # the tags\n" +
				"  - javascript\n" +
				"author: bob\n" +
				"---\nbody",
		},
		{
			name: "flow sequence rewrite",
			input: "---\n" +
				"tags: [JS, node]\n" +
				"title: x\n" +
				"---\nbody",
			newTags:  []string{"javascript", "node"},
			wantKind: TagsSpanFlow,
			wantOut: "---\n" +
				"tags: [javascript, node]\n" +
				"title: x\n" +
				"---\nbody",
		},
		{
			name: "flow keeps trailing comment and spaces",
			input: "---\n" +
				"tags:  [js, go]  # note\n" +
				"---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanFlow,
			wantOut: "---\n" +
				"tags:  [javascript]  # note\n" +
				"---\n",
		},
		{
			name: "flow multiline",
			input: "---\n" +
				"tags: [js,\n" +
				"  go]\n" +
				"---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanFlow,
			wantOut: "---\n" +
				"tags: [javascript]\n" +
				"---\n",
		},
		{
			name: "crlf preserved in block",
			input: "---\r\n" +
				"tags:\r\n" +
				"  - js\r\n" +
				"  - go\r\n" +
				"---\r\nbody\r\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanBlock,
			wantOut: "---\r\n" +
				"tags:\r\n" +
				"  - javascript\r\n" +
				"---\r\nbody\r\n",
		},
		{
			name:     "bom preserved",
			input:    "\ufeff---\ntags:\n  - js\n---\nbody",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanBlock,
			wantOut:  "\ufeff---\ntags:\n  - javascript\n---\nbody",
		},
		{
			name:     "case-insensitive key Tags",
			input:    "---\nTags:\n  - JS\n---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanBlock,
			wantOut:  "---\nTags:\n  - javascript\n---\n",
		},
		{
			name: "special characters get quoted",
			input: "---\n" +
				"tags:\n  - js\n---\n",
			newTags:  []string{"a: b", "true", "1", "#hash"},
			wantKind: TagsSpanBlock,
			wantOut: "---\n" +
				"tags:\n" +
				"  - 'a: b'\n" +
				"  - \"true\"\n" +
				"  - \"1\"\n" +
				"  - '#hash'\n" +
				"---\n",
		},
		{
			name:     "scalar tags unsupported",
			input:    "---\ntags: js\n---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanUnsupported,
		},
		{
			name:     "mixed-type sequence unsupported",
			input:    "---\ntags: [js, 1]\n---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanUnsupported,
		},
		{
			name: "duplicate tags keys unsupported",
			input: "---\n" +
				"tags: [js]\n" +
				"tags: [go]\n" +
				"---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanUnsupported,
		},
		{
			name: "nested tags key untouched",
			input: "---\n" +
				"meta:\n" +
				"  tags:\n" +
				"    - js\n" +
				"title: x\n" +
				"---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanNone,
		},
		{
			name:     "malformed yaml unsupported",
			input:    "---\ntags: [js\n---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanUnsupported,
		},
		{
			name:     "frontmatter without tags",
			input:    "---\ntitle: x\n---\n",
			newTags:  []string{"javascript"},
			wantKind: TagsSpanNone,
		},
		{
			name:     "empty new tags errors",
			input:    "---\ntags:\n  - js\n---\n",
			newTags:  nil,
			wantKind: TagsSpanNone,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, kind, err := RewriteFrontmatterTags(tc.input, tc.newTags)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if kind != tc.wantKind {
				t.Fatalf("kind = %d, want %d (out=%q)", kind, tc.wantKind, out)
			}
			want := tc.wantOut
			if want == "" {
				want = tc.input
			}
			if out != want {
				t.Fatalf("output mismatch:\ngot  %q\nwant %q", out, want)
			}
		})
	}
}

func TestRewriteFrontmatterTags_RoundTripsThroughParser(t *testing.T) {
	input := "---\ntitle: x\ntags:\n  - JS\n  - Node\n---\nbody"
	out, kind, err := RewriteFrontmatterTags(input, []string{"javascript", "nodejs"})
	if err != nil || kind != TagsSpanBlock {
		t.Fatalf("kind=%d err=%v", kind, err)
	}
	fm, _, has, perr := ParseFrontmatter(out)
	if perr != nil || !has {
		t.Fatalf("ParseFrontmatter has=%v err=%v", has, perr)
	}
	list, ok := fm.ExtraFields["tags"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("tags = %#v", fm.ExtraFields["tags"])
	}
	got := []string{list[0].(string), list[1].(string)}
	want := []string{"javascript", "nodejs"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tags after rewrite = %v, want %v", got, want)
	}
}

func TestRewriteFrontmatterTags_OnlyTagsBytesChange(t *testing.T) {
	input := "---\n" +
		"# leading comment\n" +
		"title: Keep me\n" +
		"tags:\n" +
		"  - js\n" +
		"author: bob\n" +
		"---\n" +
		"# Body\n\nText `tags: [x]` stays.\n"
	out, kind, err := RewriteFrontmatterTags(input, []string{"javascript"})
	if err != nil || kind != TagsSpanBlock {
		t.Fatalf("kind=%d err=%v", kind, err)
	}
	before := strings.Split(input, "\n")
	after := strings.Split(out, "\n")
	if len(before) != len(after) {
		t.Fatalf("line count changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i] == "  - js" {
			if after[i] != "  - javascript" {
				t.Fatalf("tags line = %q", after[i])
			}
			continue
		}
		if before[i] != after[i] {
			t.Fatalf("line %d changed unexpectedly:\nbefore %q\nafter  %q", i, before[i], after[i])
		}
	}
}
