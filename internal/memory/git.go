package memory

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CommitInfo is one commit touching a task.
type CommitInfo struct {
	SHA   string
	Files []string
}

// CommitSubject is one commit with its full message and date, used by
// the tracker hygiene audit (card #855).
type CommitSubject struct {
	SHA     string
	Subject string
	Body    string
	Date    time.Time
}

// GitEnricher extracts commits that reference a task id in their message.
type GitEnricher interface {
	Commits(ctx context.Context, taskID int64) ([]CommitInfo, error)
}

// GitClient runs git log against a local repository clone.
type GitClient struct {
	// Repo is the filesystem path of the repository.
	Repo string
	// Bin is the git binary (default "git").
	Bin string
}

// NewGit creates a git enricher for a repository path.
func NewGit(repo string) *GitClient {
	return &GitClient{Repo: repo}
}

// taskGrepPattern builds the --grep regex matching the task id as
// #<id> or bare <id>, not as a substring of a longer number.
func taskGrepPattern(taskID int64) string {
	return fmt.Sprintf(`(^|[^0-9])#?%d([^0-9]|$)`, taskID)
}

// Commits implements GitEnricher.
func (g *GitClient) Commits(ctx context.Context, taskID int64) ([]CommitInfo, error) {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	args := []string{
		"-C", g.Repo,
		"log", "--all",
		"--extended-regexp",
		"--grep=" + taskGrepPattern(taskID),
		"--pretty=format:%H",
		"--name-only",
	}
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- bin comes from trusted config/env
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("memory: git log: %w", err)
	}
	return parseGitLog(string(out)), nil
}

// Subjects returns every commit (all branches) with its message and
// author date. since is a raw git --since expression ("" = all history).
// Used by the board_audit hygiene mode (card #855).
func (g *GitClient) Subjects(ctx context.Context, since string) ([]CommitSubject, error) {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	args := []string{
		"-C", g.Repo,
		"log", "--all",
		"--pretty=format:%H%x1f%cI%x1f%B%x1e",
	}
	if since != "" {
		args = append(args, "--since="+since)
	}
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- bin comes from trusted config/env
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("memory: git log: %w", err)
	}
	return parseGitSubjects(string(out)), nil
}

// parseGitSubjects splits %H%x1f%cI%x1f%B%x1e records.
func parseGitSubjects(out string) []CommitSubject {
	var commits []CommitSubject
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	acc := strings.Builder{}
	flush := func(rec string) {
		rec = strings.TrimRight(rec, "\x1e\r\n")
		if rec == "" {
			return
		}
		parts := strings.SplitN(rec, "\x1f", 3)
		if len(parts) < 3 || !isSHA(parts[0]) {
			return
		}
		cs := CommitSubject{SHA: parts[0]}
		if t, err := time.Parse(time.RFC3339, parts[1]); err == nil {
			cs.Date = t
		}
		cs.Body = strings.Trim(parts[2], "\r\n")
		if i := strings.IndexByte(cs.Body, '\n'); i >= 0 {
			cs.Subject = cs.Body[:i]
		} else {
			cs.Subject = cs.Body
		}
		commits = append(commits, cs)
	}
	for sc.Scan() {
		acc.WriteString(sc.Text())
		acc.WriteString("\n")
		if strings.Contains(sc.Text(), "\x1e") {
			flush(acc.String())
			acc.Reset()
		}
	}
	flush(acc.String())
	return commits
}

// parseGitLog splits `--pretty=format:%H --name-only` output into
// commit blocks separated by blank lines.
func parseGitLog(out string) []CommitInfo {
	var commits []CommitInfo
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var cur *CommitInfo
	seen := map[string]bool{}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			cur = nil
			continue
		}
		if isSHA(line) {
			if !seen[line] {
				seen[line] = true
				commits = append(commits, CommitInfo{SHA: line})
				cur = &commits[len(commits)-1]
			} else {
				cur = nil
				for i := range commits {
					if commits[i].SHA == line {
						cur = &commits[i]
						break
					}
				}
			}
			continue
		}
		if cur != nil {
			cur.Files = append(cur.Files, line)
		}
	}
	sort.Slice(commits, func(i, j int) bool { return commits[i].SHA < commits[j].SHA })
	return commits
}

func isSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	_, err := strconv.ParseUint(s, 16, 64)
	_ = err // hex-shaped string of len 7..40 is accepted as a sha
	return true
}

// shas returns the commit shas of enriched commits (payload.commits).
func shas(commits []CommitInfo) []string {
	out := make([]string, 0, len(commits))
	for _, c := range commits {
		out = append(out, c.SHA)
	}
	return out
}

// touchedFiles returns the deduplicated, sorted set of files touched by
// the given commits, capped at 50 entries.
func touchedFiles(commits []CommitInfo) []string {
	seen := map[string]bool{}
	for _, c := range commits {
		for _, f := range c.Files {
			seen[f] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}
