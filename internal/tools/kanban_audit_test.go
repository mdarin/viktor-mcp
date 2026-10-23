package tools

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"viktor/internal/memory"
)

func TestCardRefs(t *testing.T) {
	cases := []struct {
		msg  string
		want map[int]bool
	}{
		{"feat(scoring): goals marker (card #854)", map[int]bool{854: true}},
		{"docs: bump refs (card #853, card #854)", map[int]bool{853: true, 854: true}},
		{"fix: bug in #851 module", map[int]bool{851: true}},
		{"merge #855 into main", map[int]bool{855: true}},
		{"body ref\n\ncard #836 in body", map[int]bool{836: true}},
		{"chore: no card reference", map[int]bool{}},
		{"revert 854 broken build", map[int]bool{}}, // bare number, no '#'
		{"see #851 in the #85 list", map[int]bool{851: true, 85: true}},
	}
	for _, c := range cases {
		got := cardRefs(c.msg)
		if len(got) != len(c.want) {
			t.Errorf("cardRefs(%q) = %v, want %v", c.msg, got, c.want)
			continue
		}
		for id := range c.want {
			if !got[id] {
				t.Errorf("cardRefs(%q) missing %d: %v", c.msg, id, got)
			}
		}
	}
}

func TestAuditHygiene(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	subjects := []memory.CommitSubject{
		{SHA: "aaaaaaaaaaaaaaaa", Subject: "feat: card #901", Body: "feat: card #901", Date: now.Add(-time.Hour)},
		{SHA: "bbbbbbbbbbbbbbbb", Subject: "chore: no ref", Body: "chore: no ref", Date: now.Add(-2 * time.Hour)},
		{SHA: "cccccccccccccccc", Subject: "old: card #902", Body: "old: card #902", Date: now.AddDate(0, 0, -60)},
		{SHA: "dddddddddddddddd", Subject: "recent no ref", Body: "recent no ref", Date: now.Add(-3 * time.Hour)},
		{SHA: "eeeeeeeeeeeeeeee", Subject: "no date commit", Body: "no date commit", Date: time.Time{}},
	}
	tasks := []map[string]any{
		{"id": float64(901), "title": "covered"},
		{"id": float64(902), "title": "closed silently"},
		{"id": float64(903), "title": "also silent"},
	}
	rep := auditHygiene(tasks, subjects, 30, now)
	if !rep.GitChecked {
		t.Fatal("git_checked must be true")
	}
	if rep.DoneChecked != 3 {
		t.Errorf("done_checked = %d, want 3", rep.DoneChecked)
	}
	if len(rep.ClosedWO) != 1 || rep.ClosedWO[0].TaskID != 903 {
		t.Errorf("closed_without_commit = %v, want [903] (902 is referenced by its old commit)", rep.ClosedWO)
	}
	if rep.ClosedWO[0].Title != "also silent" {
		t.Errorf("title = %q", rep.ClosedWO[0].Title)
	}
	// "old: card #902" is referenced, so task 902 is not flagged even
	// though its commit sits outside the window; commits_without_card
	// counts only recent unreferenced commits.
	if len(rep.CommitsWO) != 2 {
		t.Fatalf("commits_without_card = %v, want 2 recent unreferenced", rep.CommitsWO)
	}
	if rep.CommitsWO[0].SHA != "bbbbbbbbbbbb" || rep.CommitsWO[1].SHA != "dddddddddddd" {
		t.Errorf("commits_without_card shas = %v", rep.CommitsWO)
	}
	if rep.CheckedComms != 5 {
		t.Errorf("commits_checked = %d, want 5", rep.CheckedComms)
	}
}

func TestAuditHygieneDefaultWindow(t *testing.T) {
	rep := auditHygiene(nil, nil, 0, time.Now())
	if rep.WindowDays != 30 {
		t.Errorf("window = %d, want default 30", rep.WindowDays)
	}
}

func TestBoardAuditHygieneNoMemoryConfig(t *testing.T) {
	f, session := boardFixture(t)
	f.doneTasks = []map[string]any{{"id": float64(50), "title": "c"}}
	out := callToolMust(t, session, "board_audit", map[string]any{"project_id": 9, "hygiene": true})
	h, ok := out["hygiene"].(map[string]any)
	if !ok {
		t.Fatalf("no hygiene block: %v", out)
	}
	if h["git_checked"] != false {
		t.Errorf("git_checked = %v, want false", h["git_checked"])
	}
	if h["git_error"] == "" {
		t.Error("git_error must explain the degradation")
	}
	if h["done_tasks_checked"] != float64(1) {
		t.Errorf("done_tasks_checked = %v, want 1", h["done_tasks_checked"])
	}
	if _, has := h["closed_without_commit"]; !has {
		t.Error("closed_without_commit must always be present")
	}
}

func TestBoardAuditHygieneGitFailure(t *testing.T) {
	setMemoryConfigsForTest(t, t.TempDir()) // empty dir: not a git repo
	f, session := boardFixture(t)
	f.doneTasks = []map[string]any{{"id": float64(50), "title": "c"}}
	out := callToolMust(t, session, "board_audit", map[string]any{"project_id": 9, "hygiene": true})
	h := out["hygiene"].(map[string]any)
	if h["git_checked"] != false {
		t.Errorf("git_checked = %v, want false", h["git_checked"])
	}
	if h["git_error"] == "" {
		t.Error("git_error must carry the git failure")
	}
	if h["done_tasks_checked"] != float64(1) {
		t.Errorf("done_tasks_checked = %v, want 1", h["done_tasks_checked"])
	}
}

func TestBoardAuditHygieneWithGit(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(repo+"/"+name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "one\n")
	git("add", ".")
	git("commit", "-q", "-m", "feat: covered work (card #901)")
	write("b.txt", "two\n")
	git("add", ".")
	git("commit", "-q", "-m", "chore: unreferenced commit")

	setMemoryConfigsForTest(t, repo)
	f, session := boardFixture(t)
	f.doneTasks = []map[string]any{
		{"id": float64(901), "title": "covered"},
		{"id": float64(902), "title": "closed silently"},
	}

	out := callToolMust(t, session, "board_audit", map[string]any{
		"project_id": 9, "hygiene": true, "hygiene_window_days": 365,
	})
	h := out["hygiene"].(map[string]any)
	if h["git_checked"] != true {
		t.Fatalf("git_checked = %v (git_error=%v)", h["git_checked"], h["git_error"])
	}
	if h["done_tasks_checked"] != float64(2) {
		t.Errorf("done_tasks_checked = %v", h["done_tasks_checked"])
	}
	closed, _ := h["closed_without_commit"].([]any)
	if len(closed) != 1 || closed[0].(map[string]any)["task_id"] != float64(902) {
		t.Errorf("closed_without_commit = %v, want [902]", closed)
	}
	wo, _ := h["commits_without_card"].([]any)
	if len(wo) != 1 || wo[0].(map[string]any)["subject"] != "chore: unreferenced commit" {
		t.Errorf("commits_without_card = %v, want the chore commit", wo)
	}
}

// setMemoryConfigsForTest wires git repo settings into the registry the
// next newKanbanFixture creates (the registry is not reachable after
// the MCP session connects).
func setMemoryConfigsForTest(t *testing.T, repo string) {
	t.Helper()
	testMemoryCfgs = map[string]memory.Config{"default": {GitRepo: repo}}
	t.Cleanup(func() { testMemoryCfgs = nil })
}
