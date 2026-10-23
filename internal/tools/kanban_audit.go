package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
	"viktor/internal/memory"
)

type BoardAuditIn struct {
	ProjectID int `json:"project_id"`
	ViewID    int `json:"view_id,omitempty"`
	// Profile overrides the configured board profile (card #845).
	Profile []string `json:"profile,omitempty"`
	// Hygiene enables the tracker↔git consistency audit (card #855):
	// done cards without a `card #N` commit, and recent commits without
	// any card reference. Best-effort: no git → git_checked:false.
	Hygiene bool `json:"hygiene,omitempty"`
	// HygieneWindowDays bounds the commits_without_card check;
	// 0 → 30 days.
	HygieneWindowDays int `json:"hygiene_window_days,omitempty"`
}

type boardAuditBucket struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
}

type hygieneReport struct {
	GitChecked   bool                  `json:"git_checked"`
	GitRepo      string                `json:"git_repo,omitempty"`
	GitError     string                `json:"git_error,omitempty"`
	WindowDays   int                   `json:"window_days"`
	DoneChecked  int                   `json:"done_tasks_checked"`
	ClosedWO     []closedWithoutCommit `json:"closed_without_commit"`
	CommitsWO    []commitWithoutCard   `json:"commits_without_card"`
	CheckedComms int                   `json:"commits_checked"`
}

type closedWithoutCommit struct {
	TaskID int    `json:"task_id"`
	Title  string `json:"title"`
}

type commitWithoutCard struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

var cardRefRe = regexp.MustCompile(`#[0-9]+`)

// cardRefs extracts the card ids a commit message references
// ("card #854", "(#853)") with word boundaries: "#855" does not match
// card 85, and bare numbers without '#' do not count.
func cardRefs(msg string) map[int]bool {
	out := map[int]bool{}
	for _, m := range cardRefRe.FindAllString(msg, -1) {
		if id, err := strconv.Atoi(m[1:]); err == nil {
			out[id] = true
		}
	}
	return out
}

// auditHygiene cross-checks done cards against git history. tasks are
// live done tasks (id/title), subjects the full commit log. Pure and
// unit-testable (card #855).
func auditHygiene(tasks []map[string]any, subjects []memory.CommitSubject, windowDays int, now time.Time) hygieneReport {
	if windowDays <= 0 {
		windowDays = 30
	}
	rep := hygieneReport{
		GitChecked:  true,
		WindowDays:  windowDays,
		DoneChecked: len(tasks),
		ClosedWO:    []closedWithoutCommit{},
		CommitsWO:   []commitWithoutCard{},
	}
	referenced := map[int]bool{}
	for _, c := range subjects {
		for id := range cardRefs(c.Body) {
			referenced[id] = true
		}
	}
	for _, t := range tasks {
		id := asInt(t["id"])
		if id == 0 || referenced[id] {
			continue
		}
		title, _ := t["title"].(string)
		rep.ClosedWO = append(rep.ClosedWO, closedWithoutCommit{TaskID: id, Title: title})
	}
	sort.Slice(rep.ClosedWO, func(i, j int) bool {
		return rep.ClosedWO[i].TaskID < rep.ClosedWO[j].TaskID
	})
	cutoff := now.AddDate(0, 0, -windowDays)
	for _, c := range subjects {
		rep.CheckedComms++
		if c.Date.IsZero() || c.Date.Before(cutoff) {
			continue
		}
		if len(cardRefs(c.Body)) == 0 {
			rep.CommitsWO = append(rep.CommitsWO, commitWithoutCard{SHA: shortSHA(c.SHA), Subject: c.Subject})
		}
	}
	sort.Slice(rep.CommitsWO, func(i, j int) bool {
		return rep.CommitsWO[i].SHA < rep.CommitsWO[j].SHA
	})
	return rep
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func handleBoardAudit(r *Registry) mcp.ToolHandlerFor[BoardAuditIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BoardAuditIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		viewID := in.ViewID
		if viewID == 0 {
			vid, err := findKanbanViewID(ctx, r, in.ProjectID)
			if err != nil {
				return nil, nil, err
			}
			viewID = vid
		}
		var board []map[string]any
		if err := r.api().ViewTasks(ctx, in.ProjectID, viewID, &board); err != nil {
			return nil, nil, fmt.Errorf("read board: %w", err)
		}
		profile := in.Profile
		if len(profile) == 0 {
			profile = r.boardProfile()
		}
		want := map[string]bool{}
		for _, t := range profile {
			want[strings.ToLower(t)] = true
		}
		have := map[string]bool{}
		buckets := []boardAuditBucket{}
		for _, b := range board {
			title, _ := b["title"].(string)
			have[strings.ToLower(title)] = true
			tasks, _ := b["tasks"].([]any)
			count := len(tasks)
			// The board endpoint caps embedded tasks (default 50) but
			// reports the real total in `count` — prefer it, otherwise
			// done-buckets over 50 look drained (QA #848 wave).
			if n, ok := b["count"].(float64); ok {
				count = int(n)
			}
			buckets = append(buckets, boardAuditBucket{ID: asInt(b["id"]), Title: title, Count: count})
		}
		missing := []string{}
		for _, t := range profile {
			if !have[strings.ToLower(t)] {
				missing = append(missing, t)
			}
		}
		extra := []string{}
		for _, b := range board {
			title, _ := b["title"].(string)
			if !want[strings.ToLower(title)] {
				extra = append(extra, title)
			}
		}
		doneBucketID := 0
		var views []map[string]any
		if err := r.api().ListViews(ctx, in.ProjectID, &views); err == nil {
			for _, v := range views {
				if asInt(v["id"]) == viewID {
					doneBucketID = asInt(v["done_bucket_id"])
				}
			}
		}
		ok := len(missing) == 0 && len(extra) == 0 && doneBucketID != 0
		out := map[string]any{
			"ok":             ok,
			"project_id":     in.ProjectID,
			"view_id":        viewID,
			"profile":        profile,
			"missing":        missing,
			"extra":          extra,
			"done_bucket_id": doneBucketID,
			"buckets":        buckets,
		}
		if in.Hygiene {
			out["hygiene"] = runHygieneAudit(ctx, r, in)
		}
		return nil, out, nil
	}
}

// runHygieneAudit fetches done tasks and the git log and cross-checks
// them. Every failure degrades to a report field, never an error
// (best-effort pattern, card #838).
func runHygieneAudit(ctx context.Context, r *Registry, in BoardAuditIn) hygieneReport {
	rep := hygieneReport{
		WindowDays: in.HygieneWindowDays,
		ClosedWO:   []closedWithoutCommit{},
		CommitsWO:  []commitWithoutCard{},
	}
	if rep.WindowDays <= 0 {
		rep.WindowDays = 30
	}
	doneTasks, err := fetchDoneTasks(ctx, r, in.ProjectID)
	if err != nil {
		rep.GitError = "fetch done tasks: " + err.Error()
		return rep
	}
	rep.DoneChecked = len(doneTasks)
	cfg, ok := r.memoryFor("")
	if !ok || cfg.GitRepo == "" {
		rep.GitError = "no git repo configured (memory.git_enrich for the active instance)"
		return rep
	}
	rep.GitRepo = cfg.GitRepo
	subjects, err := memory.NewGit(cfg.GitRepo).Subjects(ctx, "")
	if err != nil {
		rep.GitError = err.Error()
		return rep
	}
	h := auditHygiene(doneTasks, subjects, in.HygieneWindowDays, time.Now())
	h.GitRepo = rep.GitRepo
	h.GitError = ""
	return h
}

func fetchDoneTasks(ctx context.Context, r *Registry, projectID int) ([]map[string]any, error) {
	var tasks []map[string]any
	if err := r.api().ProjectTasks(ctx, projectID, client.ListParams{Filter: "done = true"}, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}
