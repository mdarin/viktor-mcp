package verifier

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitDiff produces the diff text of a repo: uncommitted changes first,
// falling back to the last commit's patch when the tree is clean.
func GitDiff(ctx context.Context, repo string) (string, error) {
	if out, err := gitOutput(ctx, repo, "diff", "HEAD"); err == nil && strings.TrimSpace(out) != "" {
		return out, nil
	}
	out, err := gitOutput(ctx, repo, "show", "HEAD", "--format=")
	if err != nil {
		return "", fmt.Errorf("verifier: git diff: %w", err)
	}
	return out, nil
}

func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	// #nosec G204 — git and its arguments are fixed constants; only the
	// repo working directory varies and it is operator-provided.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(buf.String()), err)
	}
	return buf.String(), nil
}
