package importer

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNormalizeMode(t *testing.T) {
	cases := []struct {
		in   ImportMode
		want ImportMode
		err  bool
	}{
		{"", ModeLegacy, false},
		{ModeLegacy, ModeLegacy, false},
		{ModeManaged, ModeManaged, false},
		{"surprise", "", true},
	}
	for _, tc := range cases {
		got, err := normalizeMode(tc.in)
		if tc.err {
			if !errors.Is(err, ErrUnknownImportMode) {
				t.Errorf("normalizeMode(%q) err = %v, want ErrUnknownImportMode", tc.in, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("normalizeMode(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestCreateImportPlanFromFolderMode_UnknownMode(t *testing.T) {
	is := newServiceWithFakeWiki(t, &fakeWiki{})
	if _, err := is.CreateImportPlanFromFolderMode(t.TempDir(), "", ImportMode("nope"), ""); !errors.Is(err, ErrUnknownImportMode) {
		t.Fatalf("err = %v, want ErrUnknownImportMode", err)
	}
}

func TestPlanOptions_LegacyModePersistsAndBackwardCompatible(t *testing.T) {
	opts := PlanOptions{SourceBasePath: "/tmp/src", TargetBasePath: "docs", Mode: ModeLegacy}
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Plans written by older builds have no mode/collection fields: they must
	// deserialize as the zero value and resolve to the legacy strategy.
	var legacyJSON PlanOptions
	if err := json.Unmarshal([]byte(`{"source_base_path":"/tmp/src","target_base_path":"docs"}`), &legacyJSON); err != nil {
		t.Fatalf("unmarshal old json: %v", err)
	}
	if legacyJSON.Mode != "" || legacyJSON.Collection != "" {
		t.Fatalf("old plan json = %#v, want zero mode/collection", legacyJSON)
	}
	if mode, err := normalizeMode(legacyJSON.Mode); err != nil || mode != ModeLegacy {
		t.Fatalf("zero mode resolves = %q, %v; want legacy", mode, err)
	}

	var roundtrip PlanOptions
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundtrip.Mode != ModeLegacy {
		t.Fatalf("roundtrip mode = %q, want legacy", roundtrip.Mode)
	}
}

// A plan blocked by conflicts must never start: both start paths return
// ErrImportPlanBlocked, the status stays "planned", and nothing executes.
func TestExecuteCurrentPlan_BlockedPlanRejected(t *testing.T) {
	wiki := &fakeWiki{}
	is := newServiceWithFakeWiki(t, wiki)

	ws := t.TempDir()
	mustWrite(t, ws, "Page.md", "# Page\n")
	if _, err := is.CreateImportPlanFromFolder(ws, ""); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// Mark the stored plan blocked, as the managed planner would.
	sp, err := is.planStore.Get()
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	sp.Plan.Blocked = true
	if err := is.planStore.Set(sp); err != nil {
		t.Fatalf("set blocked plan: %v", err)
	}

	if _, err := is.ExecuteCurrentPlan("user1"); !errors.Is(err, ErrImportPlanBlocked) {
		t.Fatalf("sync execute err = %v, want ErrImportPlanBlocked", err)
	}
	if _, _, err := is.StartCurrentPlanExecution("user1"); !errors.Is(err, ErrImportPlanBlocked) {
		t.Fatalf("async start err = %v, want ErrImportPlanBlocked", err)
	}

	state, err := is.GetCurrentPlan()
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.ExecutionStatus != ExecutionStatusPlanned {
		t.Fatalf("status = %q, want planned (blocked plans must not start)", state.ExecutionStatus)
	}
	if wiki.ensureCalls != 0 {
		t.Fatalf("blocked plan performed %d writes; want zero", wiki.ensureCalls)
	}
}
