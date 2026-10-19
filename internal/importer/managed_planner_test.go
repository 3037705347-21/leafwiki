package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/perber/wiki/internal/core/tree"
)

type managedFixture struct {
	t        *testing.T
	ws       string
	wiki     *fakeWiki
	store    *CollectionStore
	strategy *managedStrategy
}

func newManagedFixture(t *testing.T) *managedFixture {
	t.Helper()
	wiki := &fakeWiki{
		lookups:   map[string]*tree.PathLookup{},
		nodesByID: map[string]*tree.PageNode{},
		pagesByID: map[string]*tree.Page{},
	}
	store := NewCollectionStore(filepath.Join(t.TempDir(), "managed"))
	planner := NewPlanner(wiki, tree.NewSlugService(), "")
	return &managedFixture{
		t:        t,
		ws:       t.TempDir(),
		wiki:     wiki,
		store:    store,
		strategy: newManagedStrategy(planner, store),
	}
}

func (f *managedFixture) writeSource(rel, body string) {
	f.t.Helper()
	abs := filepath.Join(f.ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// chainNode builds a node at targetPath with synthetic parents so
// CalculatePath() reproduces the path.
func chainNode(id, targetPath string, kind tree.NodeKind, title string) *tree.PageNode {
	segs := strings.Split(strings.Trim(targetPath, "/"), "/")
	var parent *tree.PageNode
	for i, s := range segs {
		nodeID := id + "-anc-" + s
		nodeKind := tree.NodeKindSection
		if i == len(segs)-1 {
			nodeID = id
			nodeKind = kind
		}
		n := &tree.PageNode{ID: nodeID, Slug: s, Title: s, Kind: nodeKind, Parent: parent}
		if i == len(segs)-1 {
			n.Title = title
			return n
		}
		parent = n
	}
	return nil
}

// addPage registers a managed page: node chain, raw content and a path
// lookup resolving to id.
func (f *managedFixture) addPage(id, targetPath string, kind tree.NodeKind, title, raw string, children ...*tree.PageNode) *tree.PageNode {
	f.t.Helper()
	node := chainNode(id, targetPath, kind, title)
	if node == nil {
		f.t.Fatalf("cannot add root node %q", id)
	}
	node.Children = children
	for _, c := range children {
		c.Parent = node
	}
	f.wiki.nodesByID[id] = node
	f.wiki.pagesByID[id] = &tree.Page{PageNode: node, Content: raw, RawContent: raw}

	segs := strings.Split(strings.Trim(targetPath, "/"), "/")
	lookupSegs := make([]tree.PathSegment, len(segs))
	for i, s := range segs {
		idCopy := id
		kindCopy := kind
		lookupSegs[i] = tree.PathSegment{Slug: s, Exists: true, ID: &idCopy, Kind: &kindCopy}
	}
	f.wiki.lookups[targetPath] = &tree.PathLookup{Path: targetPath, Exists: true, Segments: lookupSegs}
	return node
}

func (f *managedFixture) saveManifest(name, base string, entries map[string]ManifestEntry) {
	f.t.Helper()
	m := &CollectionManifest{
		SchemaVersion:  ManifestSchemaVersion,
		Collection:     name,
		TargetBasePath: base,
		Entries:        entries,
	}
	if m.Entries == nil {
		m.Entries = map[string]ManifestEntry{}
	}
	if err := f.store.Save(m); err != nil {
		f.t.Fatalf("save manifest: %v", err)
	}
}

func (f *managedFixture) plan(name, base string, rels ...string) *PlanResult {
	f.t.Helper()
	entries := make([]ImportMDFile, len(rels))
	for i, rel := range rels {
		entries[i] = ImportMDFile{SourcePath: rel}
	}
	res, err := f.strategy.CreatePlan(entries, PlanOptions{
		SourceBasePath: f.ws,
		TargetBasePath: base,
		Mode:           ModeManaged,
		Collection:     name,
	})
	if err != nil {
		f.t.Fatalf("CreatePlan: %v", err)
	}
	return res
}

func (f *managedFixture) planErr(name, base string, rels ...string) error {
	f.t.Helper()
	entries := make([]ImportMDFile, len(rels))
	for i, rel := range rels {
		entries[i] = ImportMDFile{SourcePath: rel}
	}
	_, err := f.strategy.CreatePlan(entries, PlanOptions{
		SourceBasePath: f.ws, TargetBasePath: base, Mode: ModeManaged, Collection: name,
	})
	return err
}

func findItem(plan *PlanResult, source string) *PlanItem {
	for i := range plan.Items {
		if plan.Items[i].SourcePath == source {
			return &plan.Items[i]
		}
	}
	return nil
}

func hasConflict(item *PlanItem, code string) bool {
	if item == nil {
		return false
	}
	for _, c := range item.Conflicts {
		if c == code {
			return true
		}
	}
	return false
}

func TestManagedPlanner_FirstRun_AllCreate(t *testing.T) {
	f := newManagedFixture(t)
	f.writeSource("intro/install.md", "# Installation\n\nbody\n")
	f.writeSource("intro/index.md", "# Intro\n")

	plan := f.plan("manual", "", "intro/index.md", "intro/install.md")
	if plan.Blocked || plan.ConflictCount != 0 {
		t.Fatalf("first run blocked=%v count=%d", plan.Blocked, plan.ConflictCount)
	}
	if item := findItem(plan, "intro/install.md"); item == nil || item.Action != PlanActionCreate || item.DesiredFingerprint == "" {
		t.Fatalf("install item = %#v", item)
	}
	if item := findItem(plan, "intro/index.md"); item == nil || item.Action != PlanActionCreate || item.Kind != tree.NodeKindSection {
		t.Fatalf("index item = %#v", item)
	}
}

func TestManagedPlanner_RerunAndUpdateMatrix(t *testing.T) {
	const pageID = "p1"
	const title = "Installation"
	const raw = "---\nleafwiki_id: p1\nleafwiki_title: Installation\n---\n# Installation\n\nbody\n"

	// entry builds a manifest entry whose applied fingerprint matches the
	// given raw page state (page untouched since that import).
	entry := func(rel, target string, kind tree.NodeKind, appliedTitle, appliedRaw string) ManifestEntry {
		return ManifestEntry{
			SourcePath: rel, TargetPath: target, PageID: pageID, Kind: kind,
			Title: appliedTitle, AppliedFingerprint: fp(appliedTitle, appliedRaw),
		}
	}

	t.Run("identical rerun skips", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install.md", "# Installation\n\nbody\n")
		first := f.plan("manual", "", "install.md")
		desired := findItem(first, "install.md").DesiredFingerprint
		if desired != fp(title, raw) {
			t.Fatalf("desired fingerprint mismatch with canonical live form")
		}
		f.addPage(pageID, "install", tree.NodeKindPage, title, raw)
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"install.md": {SourcePath: "install.md", TargetPath: "install", PageID: pageID, Kind: tree.NodeKindPage, Title: title, AppliedFingerprint: desired},
		})

		plan := f.plan("manual", "", "install.md")
		if plan.Blocked {
			t.Fatalf("identical rerun must not block: %#v", plan.Items)
		}
		if item := findItem(plan, "install.md"); item.Action != PlanActionSkip {
			t.Fatalf("action = %q, want skip", item.Action)
		}
	})

	t.Run("source changed and page untouched updates", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install.md", "# Installation\n\nbody v2\n")
		f.addPage(pageID, "install", tree.NodeKindPage, title, raw)
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"install.md": entry("install.md", "install", tree.NodeKindPage, title, raw),
		})

		plan := f.plan("manual", "", "install.md")
		item := findItem(plan, "install.md")
		if plan.Blocked || item.Action != PlanActionUpdate {
			t.Fatalf("blocked=%v item=%#v", plan.Blocked, item)
		}
	})

	t.Run("source changed and page edited blocks", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install.md", "# Installation\n\nbody v2\n")
		drifted := "---\nleafwiki_id: p1\n---\n# Installation\n\nlocal edit\n"
		f.addPage(pageID, "install", tree.NodeKindPage, title, drifted)
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"install.md": entry("install.md", "install", tree.NodeKindPage, title, raw),
		})

		plan := f.plan("manual", "", "install.md")
		item := findItem(plan, "install.md")
		if !plan.Blocked || !hasConflict(item, ConflictLocalModified) {
			t.Fatalf("blocked=%v item=%#v", plan.Blocked, item)
		}
	})

	t.Run("source unchanged but page edited skips with drift note", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install.md", "# Installation\n\nbody\n")
		drifted := "---\nleafwiki_id: p1\n---\n# Installation\n\nlocal edit\n"
		f.addPage(pageID, "install", tree.NodeKindPage, title, drifted)
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"install.md": entry("install.md", "install", tree.NodeKindPage, title, raw),
		})

		plan := f.plan("manual", "", "install.md")
		item := findItem(plan, "install.md")
		if plan.Blocked || item.Action != PlanActionSkip {
			t.Fatalf("blocked=%v item=%#v", plan.Blocked, item)
		}
		found := false
		for _, n := range item.Notes {
			if n == noteDriftIgnored {
				found = true
			}
		}
		if !found {
			t.Fatalf("drift note missing: %#v", item.Notes)
		}
	})
}

func TestManagedPlanner_ConflictRows(t *testing.T) {
	t.Run("managed page deleted in wiki", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install.md", "# Installation\n\nbody\n")
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"install.md": {SourcePath: "install.md", TargetPath: "install", PageID: "gone", Kind: tree.NodeKindPage, AppliedFingerprint: "sha256:old"},
		})
		plan := f.plan("manual", "", "install.md")
		if item := findItem(plan, "install.md"); !plan.Blocked || !hasConflict(item, ConflictManagedMissing) {
			t.Fatalf("item=%#v blocked=%v", item, plan.Blocked)
		}
	})

	t.Run("manual page occupies target", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install.md", "# Installation\n\nbody\n")
		f.addPage("manual-page", "install", tree.NodeKindPage, "Install", "# Installation\n\nbody\n")
		plan := f.plan("manual", "", "install.md")
		if item := findItem(plan, "install.md"); !plan.Blocked || !hasConflict(item, ConflictUnmanagedOccupant) {
			t.Fatalf("item=%#v blocked=%v", item, plan.Blocked)
		}
	})

	t.Run("kind change unsupported", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install/index.md", "# Install\n\nbody\n")
		f.addPage("p1", "install", tree.NodeKindPage, "Install", "# Install\n\nbody\n")
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"install/index.md": {SourcePath: "install/index.md", TargetPath: "install", PageID: "p1", Kind: tree.NodeKindSection, AppliedFingerprint: "sha256:old"},
		})
		plan := f.plan("manual", "", "install/index.md")
		if item := findItem(plan, "install/index.md"); !plan.Blocked || !hasConflict(item, ConflictKindChangeUnsupported) {
			t.Fatalf("item=%#v", item)
		}
	})

	t.Run("page moved to another path", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("install.md", "# Installation\n\nbody v2\n")
		// Lookup says target free, but node still exists elsewhere.
		f.addPage("p1", "elsewhere/install", tree.NodeKindPage, "Install", "# Installation\n\nbody v2\n")
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"install.md": {SourcePath: "install.md", TargetPath: "install", PageID: "p1", Kind: tree.NodeKindPage, AppliedFingerprint: "sha256:old"},
		})
		plan := f.plan("manual", "", "install.md")
		if item := findItem(plan, "install.md"); !plan.Blocked || !hasConflict(item, ConflictManagedMissing) {
			t.Fatalf("item=%#v", item)
		}
	})
}

func TestManagedPlanner_DeleteSweep(t *testing.T) {
	const rawSection = "---\nleafwiki_id: sec\n---\n# Sec\n"

	t.Run("clean delete", func(t *testing.T) {
		f := newManagedFixture(t)
		f.writeSource("gone.md", "# Gone\n\nbody\n")
		f.writeSource("keep.md", "# Keep\n")
		// First bundle creates both; capture the applied fingerprint.
		oldPlan := f.plan("manual", "", "gone.md", "keep.md")
		goneDesired := findItem(oldPlan, "gone.md").DesiredFingerprint
		// Register the gone page with state matching the applied content.
		goneRaw := "---\nleafwiki_id: gone\nleafwiki_title: Gone\n---\n# Gone\n\nbody\n"
		if fp("Gone", goneRaw) != goneDesired {
			t.Fatalf("test fixture: applied raw does not match desired fingerprint")
		}
		f.addPage("gone", "gone", tree.NodeKindPage, "Gone", goneRaw)
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"gone.md": {SourcePath: "gone.md", TargetPath: "gone", PageID: "gone", Kind: tree.NodeKindPage, Title: "Gone", AppliedFingerprint: goneDesired},
		})

		plan := f.plan("manual", "", "keep.md")
		if plan.Blocked {
			t.Fatalf("clean delete blocked: %#v", plan.Items)
		}
		if item := findItem(plan, "gone.md"); item == nil || item.Action != PlanActionDelete {
			t.Fatalf("gone item = %#v, want delete", item)
		}
		if item := findItem(plan, "keep.md"); item == nil || item.Action != PlanActionCreate {
			t.Fatalf("keep item = %#v", item)
		}
	})

	t.Run("drifted page whose source vanished blocks delete", func(t *testing.T) {
		f := newManagedFixture(t)
		f.addPage("gone", "gone", tree.NodeKindPage, "Gone", "# Gone\nlocal edit\n")
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"gone.md": {SourcePath: "gone.md", TargetPath: "gone", PageID: "gone", Kind: tree.NodeKindPage, Title: "Gone", AppliedFingerprint: "sha256:never-matches"},
		})
		f.writeSource("keep.md", "# Keep\n")
		plan := f.plan("manual", "", "keep.md")
		if item := findItem(plan, "gone.md"); !plan.Blocked || !hasConflict(item, ConflictDeleteDrifted) {
			t.Fatalf("item=%#v blocked=%v", item, plan.Blocked)
		}
	})

	t.Run("section with surviving manual child blocks", func(t *testing.T) {
		f := newManagedFixture(t)
		manualChild := &tree.PageNode{ID: "manual-child", Slug: "child", Title: "Child", Kind: tree.NodeKindPage}
		f.addPage("sec", "sec", tree.NodeKindSection, "Sec", rawSection, manualChild)
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"sec/index.md": {SourcePath: "sec/index.md", TargetPath: "sec", PageID: "sec", Kind: tree.NodeKindSection, Title: "Sec", AppliedFingerprint: fp("Sec", rawSection)},
		})
		f.writeSource("keep.md", "# Keep\n")
		plan := f.plan("manual", "", "keep.md")
		if item := findItem(plan, "sec/index.md"); !plan.Blocked || !hasConflict(item, ConflictSectionHasChildren) {
			t.Fatalf("item=%#v", item)
		}
	})

	t.Run("already missing page is pruned as skip", func(t *testing.T) {
		f := newManagedFixture(t)
		f.saveManifest("manual", "", map[string]ManifestEntry{
			"gone.md": {SourcePath: "gone.md", TargetPath: "gone", PageID: "gone", Kind: tree.NodeKindPage, AppliedFingerprint: "sha256:x"},
		})
		f.writeSource("keep.md", "# Keep\n")
		plan := f.plan("manual", "", "keep.md")
		item := findItem(plan, "gone.md")
		if plan.Blocked || item == nil || item.Action != PlanActionSkip {
			t.Fatalf("blocked=%v item=%#v", plan.Blocked, item)
		}
	})
}

func TestManagedPlanner_PlanLevelErrors(t *testing.T) {
	f := newManagedFixture(t)

	if err := f.planErr("bad/name", "", "a.md"); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("invalid collection err = %v", err)
	}

	if err := f.planErr("manual", ""); err != ErrManagedEmptyBundle {
		t.Fatalf("empty bundle err = %v", err)
	}

	// Base path pinning.
	f.writeSource("a.md", "# A\n")
	f.saveManifest("manual", "docs", map[string]ManifestEntry{})
	if err := f.planErr("manual", "other", "a.md"); !errors.Is(err, ErrManagedBasePathMismatch) {
		t.Fatalf("mismatch err = %v", err)
	}

	// Another collection pinned at a containing base rejects the new one.
	f.saveManifest("big", "docs", map[string]ManifestEntry{})
	if err := f.planErr("small", "docs/v2", "a.md"); !errors.Is(err, ErrManagedBasePathOverlap) {
		t.Fatalf("overlap err = %v", err)
	}
}

func TestManagedStrategy_ServiceOptionDispatch(t *testing.T) {
	wiki := &fakeWiki{lookups: map[string]*tree.PathLookup{}}
	planner := NewPlanner(wiki, tree.NewSlugService(), "")
	store := NewCollectionStore(filepath.Join(t.TempDir(), "managed"))
	is := NewImporterService(planner, NewPlanStore(), t.TempDir(), 0, WithManagedCollections(store))

	ws := t.TempDir()
	mustWrite(t, ws, "a.md", "# A\n")
	plan, err := is.CreateImportPlanFromFolderMode(ws, "", ModeManaged, "manual")
	if err != nil {
		t.Fatalf("managed plan: %v", err)
	}
	if plan.Blocked || len(plan.Items) != 1 || plan.Items[0].Action != PlanActionCreate {
		t.Fatalf("plan = %#v", plan)
	}
	sp, err := is.planStore.Get()
	if err != nil {
		t.Fatalf("stored plan: %v", err)
	}
	if sp.PlanOptions.Mode != ModeManaged || sp.PlanOptions.Collection != "manual" {
		t.Fatalf("stored options = %#v", sp.PlanOptions)
	}
}

// fp is the plain canonical fingerprint for fixtures without asset/link
// rewrites (transformer would leave the body unchanged).
func fp(title, raw string) string {
	v, err := ManagedContentFingerprint(title, raw)
	if err != nil {
		panic(err)
	}
	return v
}
