package memory

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// CommitInfo is one commit touching a task.
type CommitInfo struct {
	SHA   string
	Files []string
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
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("memory: git log: %w", err)
	}
	return parseGitLog(string(out)), nil
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
