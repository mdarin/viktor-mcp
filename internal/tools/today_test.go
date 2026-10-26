package tools

import (
	"strings"
	"testing"
)

func TestDeferMarker(t *testing.T) {
	c, d := parseDeferMarker("some text\n<!-- viktor:deferred=2 dread=1 -->")
	if c != 2 || d != 1 {
		t.Fatalf("parse = %d,%d", c, d)
	}
	out := setDeferMarker("some text\n<!-- viktor:deferred=2 dread=1 -->", 3, 2)
	if !strings.HasPrefix(out, "some text\n") {
		t.Fatalf("out = %q", out)
	}
	if strings.Count(out, "viktor:deferred") != 1 {
		t.Fatalf("out = %q", out)
	}
	c, d = parseDeferMarker(out)
	if c != 3 || d != 2 {
		t.Fatalf("reparse = %d,%d", c, d)
	}
}

func TestTodayActions(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["description"] = "<!-- viktor:deferred=2 dread=0 -->"
	out, err := callCRUD(t, session, "today_actions", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	intents := out["intents"].([]any)
	if len(intents) == 0 {
		t.Fatalf("no intents: %v", out)
	}
	total := 0
	for _, i := range intents {
		cluster := i.(map[string]any)
		items := cluster["items"].([]any)
		if len(items) == 0 {
			t.Fatalf("empty cluster %v", cluster)
		}
		total += len(items)
		row := items[0].(map[string]any)
		if _, ok := row["score"]; !ok {
			t.Fatalf("row missing score: %v", row)
		}
		if _, ok := row["why"]; !ok {
			t.Fatalf("row missing why: %v", row)
		}
	}
	if total != 2 {
		t.Fatalf("total = %d", total)
	}
	out, err = callCRUD(t, session, "today_actions", map[string]any{"intent_filter": "must clear"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range out["intents"].([]any) {
		if i.(map[string]any)["intent"] != "Must clear" {
			t.Fatalf("filter leaked: %v", out)
		}
	}
}

func TestTodayActionsGoalsMarker(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["description"] = "<!-- viktor:goals quality=1 -->"
	f.tasks[303]["description"] = "<!-- viktor:goals speed=1 style=fancy -->"
	out, err := callCRUD(t, session, "today_actions", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var got301, got302 bool
	for _, i := range out["intents"].([]any) {
		for _, it := range i.(map[string]any)["items"].([]any) {
			row := it.(map[string]any)
			id := int(row["task_id"].(float64))
			if id != 301 && id != 303 {
				continue
			}
			terms := row["terms"].([]any)
			hasGoals := false
			for _, tm := range terms {
				if tm.(map[string]any)["name"] == "goals" {
					hasGoals = true
				}
			}
			if !hasGoals {
				t.Fatalf("goals term missing in row: %v", row)
			}
			switch id {
			case 301:
				got301 = true
				if _, ok := row["goals_unknown_axes"]; ok {
					t.Fatalf("301 must have no unknown axes: %v", row)
				}
			case 303:
				got302 = true
				ax, ok := row["goals_unknown_axes"].([]any)
				if !ok || len(ax) != 1 || ax[0] != "style" {
					t.Fatalf("302 unknown axes: %v", row)
				}
			}
		}
	}
	if !got301 || !got302 {
		t.Fatalf("rows 301/302 missing: %v %v", got301, got302)
	}
}

func TestTodaySnoozeValidation(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "today_snooze", map[string]any{"task_id": 301}); err == nil {
		t.Fatal("schema must require reason")
	}
	out, err := callCRUD(t, session, "today_snooze", map[string]any{"task_id": 301, "reason": "dread"})
	if err != nil {
		t.Fatal(err)
	}
	if out["allowed"] != false || out["requires_date"] != true {
		t.Fatalf("dread without date = %v", out)
	}
	out, err = callCRUD(t, session, "today_snooze", map[string]any{"task_id": 301, "reason": "vacation"})
	if err != nil {
		t.Fatal(err)
	}
	if out["allowed"] != false || !strings.Contains(out["message"].(string), "taxonomy") {
		t.Fatalf("bad reason = %v", out)
	}
}

func TestTodaySnoozeOK(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "today_snooze", map[string]any{
		"task_id": 301, "reason": "waiting", "until": "2026-10-20T09:00:00Z", "comment": "check later",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["deferred_count"] != float64(1) || out["escalate"] != false {
		t.Fatalf("out = %v", out)
	}
	if f.tasks[301]["due_date"] != "2026-10-20T09:00:00Z" {
		t.Fatalf("due_date = %v", f.tasks[301]["due_date"])
	}
	desc := f.tasks[301]["description"].(string)
	c, d := parseDeferMarker(desc)
	if c != 1 || d != 0 {
		t.Fatalf("marker = %d,%d desc=%q", c, d, desc)
	}
	if len(f.comments[301]) != 1 || f.comments[301][0] != "check later" {
		t.Fatalf("comments = %v", f.comments)
	}
}

func TestTodaySnoozeDreadEscalation(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["description"] = "<!-- viktor:deferred=2 dread=2 -->"
	out, err := callCRUD(t, session, "today_snooze", map[string]any{
		"task_id": 301, "reason": "dread", "until": "2026-11-01T09:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["escalate"] != true || out["deferred_count"] != float64(3) || out["dread_count"] != float64(3) {
		t.Fatalf("out = %v", out)
	}
	c, d := parseDeferMarker(f.tasks[301]["description"].(string))
	if c != 3 || d != 3 {
		t.Fatalf("marker = %d,%d", c, d)
	}
}

func TestTodayReckoning(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["description"] = "<!-- viktor:deferred=4 dread=1 -->"
	f.tasks[303]["description"] = "<!-- viktor:deferred=3 dread=0 -->"
	f.tasks[302]["done"] = false
	f.tasks[302]["description"] = "<!-- viktor:deferred=5 dread=0 -->"
	out, err := callCRUD(t, session, "today_reckoning", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(3) {
		t.Fatalf("out = %v", out)
	}
	rows := out["reckoning"].([]any)
	first := rows[0].(map[string]any)
	if first["deferred_count"] != float64(5) {
		t.Fatalf("order = %v", rows)
	}
	out, err = callCRUD(t, session, "today_reckoning", map[string]any{"threshold": 4})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(2) {
		t.Fatalf("threshold out = %v", out)
	}
}

func TestTodayWeights(t *testing.T) {
	configFixture(t)
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "today_get_weights", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	w := out["weights"].(map[string]any)
	if w["overdue"] != float64(10) || w["quick_win"] != float64(3) {
		t.Fatalf("defaults = %v", w)
	}
	if out["custom"] != false {
		t.Fatalf("custom = %v", out)
	}
	out, err = callCRUD(t, session, "today_set_weights", map[string]any{
		"weights": map[string]any{"overdue": 15.0},
	})
	if err != nil || out["saved"] != true {
		t.Fatalf("set = %v err=%v", out, err)
	}
	w = out["weights"].(map[string]any)
	if w["overdue"] != float64(15) || w["priority"] != float64(8) {
		t.Fatalf("merged = %v", w)
	}
	out, err = callCRUD(t, session, "today_get_weights", map[string]any{})
	if err != nil || out["custom"] != true {
		t.Fatalf("get after set = %v err=%v", out, err)
	}
	if _, err := callCRUD(t, session, "today_set_weights", map[string]any{"weights": map[string]any{"bogus": 1.0}}); err == nil {
		t.Fatal("unknown weight must be rejected")
	}
	if _, err := callCRUD(t, session, "today_set_weights", map[string]any{"weights": map[string]any{"overdue": -1.0}}); err == nil {
		t.Fatal("negative weight must be rejected")
	}
}

func TestTodayActionsProjectFilter(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "today_actions", map[string]any{"project_id": 9, "limit": 50})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, i := range out["intents"].([]any) {
		for _, it := range i.(map[string]any)["items"].([]any) {
			total++
			if it.(map[string]any)["task_id"] != float64(303) {
				t.Fatalf("task from another project leaked in: %v", it)
			}
		}
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1 (task 303 only)", total)
	}
}

func TestTodayReckoningProjectFilter(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[303]["description"] = "<!-- viktor:deferred=3 dread=0 -->"
	out, err := callCRUD(t, session, "today_reckoning", map[string]any{"project_id": 9})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("reckoning for project 9 = %v", out)
	}
	out, err = callCRUD(t, session, "today_reckoning", map[string]any{"project_id": 8})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(0) {
		t.Fatalf("reckoning for project 8 = %v", out)
	}
}
