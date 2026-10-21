package memory

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// CommitRecord is one repository commit as seen by the indexer.
type CommitRecord struct {
	SHA     string
	Author  string
	Date    string
	Subject string
	Files   []string
	TaskIDs []int64
}

// CommitSource enumerates repository commits for commit chunks.
type CommitSource interface {
	AllCommits(ctx context.Context) ([]CommitRecord, error)
}

var taskRefRe = regexp.MustCompile(`(?:#\s*(\d{1,9})\b|(?:card|task|shadow|issue)\s+#?(\d{1,9})\b)`)

// extractTaskRefs pulls task ids referenced in a commit message: any
// "#<id>" plus word forms like "card 45", "shadow 360", "task 42", in
// positional order, duplicates removed, capped at 10.
func extractTaskRefs(subject string) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, m := range taskRefRe.FindAllStringSubmatch(subject, -1) {
		group := m[1]
		if group == "" {
			group = m[2]
		}
		id, err := strconv.ParseInt(group, 10, 64)
		if err != nil || id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// AllCommits implements CommitSource: the full history of all branches.
// Empty repos yield no commits and no error.
func (g *GitClient) AllCommits(ctx context.Context) ([]CommitRecord, error) {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	args := []string{
		"-C", g.Repo,
		"log", "--all",
		"--pretty=format:%H%x1f%an%x1f%aI%x1f%s%x1e",
		"--name-only",
	}
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- bin comes from trusted config/env
	out, err := cmd.Output()
	if err != nil {
		// A repo with no commits yet exits 128 ("does not have any commits").
		var exitErr *exec.ExitError
		if ee, ok := err.(*exec.ExitError); ok {
			exitErr = ee
			_ = exitErr
			if strings.Contains(string(ee.Stderr), "does not have any commits") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("memory: git log: %w", err)
	}
	return parseCommitLog(string(out)), nil
}

// parseCommitLog splits output where every commit header line ends with
// \x1e and fields are separated by \x1f; the lines between header lines
// are the touched files.
func parseCommitLog(out string) []CommitRecord {
	var commits []CommitRecord
	var cur *CommitRecord
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasSuffix(line, "\x1e") {
			parts := strings.Split(strings.TrimSuffix(line, "\x1e"), "\x1f")
			if len(parts) < 4 {
				cur = nil
				continue
			}
			sha := strings.TrimSpace(parts[0])
			if !isSHA(sha) {
				cur = nil
				continue
			}
			commits = append(commits, CommitRecord{
				SHA:     sha,
				Author:  parts[1],
				Date:    parts[2],
				Subject: parts[3],
			})
			cur = &commits[len(commits)-1]
			cur.TaskIDs = extractTaskRefs(cur.Subject)
			continue
		}
		if cur != nil {
			if f := strings.TrimSpace(line); f != "" {
				cur.Files = append(cur.Files, f)
			}
		}
	}
	return commits
}
