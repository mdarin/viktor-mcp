package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// maxFindings caps the change-risk findings appended to the digest.
const maxFindings = 5

// maxNoteLen caps the failure note in an "unavailable" digest.
const maxNoteLen = 300

// RiskFinding is one change-risk finding (top-N of health_delta).
type RiskFinding struct {
	ID       string `json:"id,omitempty"`
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

// RiskDigest is the optional repowise change-risk appendix to a Verdict
// (card #823). Strictly non-blocking: the verdict's drift fields and the
// review gate are never influenced by it.
type RiskDigest struct {
	// Status is "ok" or "unavailable" (CLI missing, no index, timeout,
	// unparseable output); Note carries the reason when unavailable.
	Status         string        `json:"status"`
	Note           string        `json:"note,omitempty"`
	Revspec        string        `json:"revspec,omitempty"`
	RiskPercentile float64       `json:"risk_percentile,omitempty"`
	ReviewPriority string        `json:"review_priority,omitempty"`
	Classification string        `json:"classification,omitempty"`
	Score          float64       `json:"score,omitempty"`
	Findings       []RiskFinding `json:"findings,omitempty"`
}

// ChangeRisker runs the change-risk analysis for a repo ("" revspec =
// uncommitted work, else a commit or base..head range). It never fails:
// CLI problems degrade to a Status "unavailable" digest, keeping the
// verdict untouched.
type ChangeRisker func(ctx context.Context, repo, revspec string) *RiskDigest

// RepowiseRunner shells out to the local repowise CLI:
// `repowise risk --format json [--path DIR] [REVSPEC]` (card #823).
// No MCP calls, no embedded client — a plain process with a timeout.
type RepowiseRunner struct {
	CLI     string
	Repo    string        // default --path; a per-call repo overrides it
	Timeout time.Duration // default 90s
}

type repowiseRiskOut struct {
	RiskPercentile *float64 `json:"risk_percentile"`
	ReviewPriority string   `json:"review_priority"`
	Classification string   `json:"classification"`
	Score          *float64 `json:"score"`
	HealthDelta    struct {
		Findings []struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
			Summary  string `json:"summary"`
		} `json:"findings"`
	} `json:"health_delta"`
}

// Run executes the CLI and returns the digest; all failures produce a
// Status "unavailable" digest, never an error and never a panic.
func (rw RepowiseRunner) Run(ctx context.Context, repo, revspec string) *RiskDigest {
	cli := rw.CLI
	if cli == "" {
		cli = "repowise"
	}
	timeout := rw.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path := rw.Repo
	if path == "" {
		path = repo
	}
	args := []string{"risk", "--format", "json"}
	if path != "" {
		args = append(args, "--path", path)
	}
	if revspec != "" {
		args = append(args, revspec)
	}

	d := &RiskDigest{Status: "unavailable", Revspec: revspec}
	// #nosec G204 — cli comes from the trusted config (repowise section),
	// args are fixed constants plus the repo path and revspec.
	out, err := exec.CommandContext(cctx, cli, args...).Output()
	if err != nil {
		d.Note = noteFromErr(err)
		return d
	}
	var payload repowiseRiskOut
	if err := json.Unmarshal(out, &payload); err != nil {
		d.Note = "unparseable CLI output"
		return d
	}
	d.Status = "ok"
	if payload.RiskPercentile != nil {
		d.RiskPercentile = *payload.RiskPercentile
	}
	if payload.Score != nil {
		d.Score = *payload.Score
	}
	d.ReviewPriority = payload.ReviewPriority
	d.Classification = payload.Classification
	for i, f := range payload.HealthDelta.Findings {
		if i == maxFindings {
			break
		}
		d.Findings = append(d.Findings, RiskFinding{ID: f.ID, Severity: f.Severity, Summary: f.Summary})
	}
	return d
}

func noteFromErr(err error) string {
	msg := err.Error()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		msg += ": " + string(ee.Stderr)
	}
	if strings.HasPrefix(msg, "signal: killed") {
		msg = "timed out"
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > maxNoteLen {
		msg = msg[:maxNoteLen] + "…"
	}
	return msg
}
