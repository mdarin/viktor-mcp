package memory

import (
	"testing"
	"time"
)

func TestParseGitSubjects(t *testing.T) {
	out := "aaaa1111\x1f2026-10-20T10:00:00Z\x1ffeat: covered (card #901)\n\nlong body\n\x1e\n" +
		"bbbb2222\x1f2026-10-21T11:00:00Z\x1fchore: unreferenced commit\x1e\n"
	got := parseGitSubjects(out)
	if len(got) != 2 {
		t.Fatalf("want 2 commits, got %d: %+v", len(got), got)
	}
	c0 := got[0]
	if c0.SHA != "aaaa1111" {
		t.Errorf("sha = %q", c0.SHA)
	}
	if c0.Subject != "feat: covered (card #901)" {
		t.Errorf("subject = %q", c0.Subject)
	}
	want, _ := time.Parse(time.RFC3339, "2026-10-20T10:00:00Z")
	if !c0.Date.Equal(want) {
		t.Errorf("date = %v, want %v", c0.Date, want)
	}
	if c0.Body != "feat: covered (card #901)\n\nlong body" {
		t.Errorf("body = %q", c0.Body)
	}
	if got[1].Body != "chore: unreferenced commit" {
		t.Errorf("single-line body = %q", got[1].Body)
	}
}

func TestParseGitSubjectsSkipsGarbage(t *testing.T) {
	got := parseGitSubjects("not-a-record\n\n\x1e\n")
	if len(got) != 0 {
		t.Errorf("want 0 commits, got %d", len(got))
	}
}
