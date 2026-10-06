package tools

import (
	"testing"
)

func TestTriageParkRoundTrip(t *testing.T) {
	t.Setenv("VIKTOR_SETTINGS", t.TempDir()+"/settings.json")
	f, session := newCRUDFixture(t)
	f.projects[320] = map[string]any{"id": float64(320), "title": "spec", "is_archived": false}
	out, err := callCRUD(t, session, "triage_park", map[string]any{"project_id": 320, "reason": "maybe later"})
	if err != nil {
		t.Fatal(err)
	}
	if out["archived"] != true || out["already_parked"] != false || out["reason"] != "maybe later" {
		t.Fatalf("park = %v", out)
	}
	if f.projects[320]["is_archived"] != true {
		t.Fatalf("project not archived: %v", f.projects[320])
	}
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s[triageParkedKey] == "" {
		t.Fatal("marker not saved")
	}
	out, err = callCRUD(t, session, "triage_park", map[string]any{"project_id": 320})
	if err != nil {
		t.Fatal(err)
	}
	if out["already_parked"] != true {
		t.Fatalf("second park = %v", out)
	}
}

func TestTriageParkedListAndPrune(t *testing.T) {
	t.Setenv("VIKTOR_SETTINGS", t.TempDir()+"/settings.json")
	f, session := newCRUDFixture(t)
	f.projects[320] = map[string]any{"id": float64(320), "title": "spec", "is_archived": true}
	f.projects[322] = map[string]any{"id": float64(322), "title": "revived", "is_archived": false}
	saveParked(map[string]parkedEntry{
		"320": {Reason: "r1", At: "2026-10-01T00:00:00Z"},
		"321": {Reason: "r2", At: "2026-10-02T00:00:00Z"},
		"322": {Reason: "r3", At: "2026-10-03T00:00:00Z"},
	})
	out, err := callCRUD(t, session, "triage_parked", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	rows := out["parked"].([]any)
	if out["count"] != float64(1) || len(rows) != 1 {
		t.Fatalf("parked = %v", out)
	}
	row := rows[0].(map[string]any)
	if row["project_id"] != float64(320) || row["reason"] != "r1" {
		t.Fatalf("row = %v", row)
	}
	if len(out["restored"].([]any)) != 1 || len(out["missing"].([]any)) != 1 {
		t.Fatalf("prune lists = %v", out)
	}
	p, err := loadParked()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 1 {
		t.Fatalf("registry not pruned: %v", p)
	}
	out, err = callCRUD(t, session, "triage_parked", map[string]any{"project_id": 999})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(0) {
		t.Fatalf("filter = %v", out)
	}
}

func TestTriageParkMissingProject(t *testing.T) {
	t.Setenv("VIKTOR_SETTINGS", t.TempDir()+"/settings.json")
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "triage_park", map[string]any{"project_id": 999}); err == nil {
		t.Fatal("missing project must fail")
	}
}
