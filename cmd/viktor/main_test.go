package main

import (
	"os"
	"testing"
)

func TestLoadDotenvInlineComment(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"
	content := "VIKTOR_BOT_TOKEN=tk_abc123 # expires 20 january 2028\nVIKTOR_PLAIN=tk_def # comment\nVIKTOR_QUOTED=\"tk_g # not comment\"\n# full line comment\nVIKTOR_EMPTY=\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIKTOR_BOT_TOKEN", "")
	t.Setenv("VIKTOR_PLAIN", "")
	t.Setenv("VIKTOR_QUOTED", "")
	t.Setenv("VIKTOR_EMPTY", "")
	loadDotenv(path)
	if got := os.Getenv("VIKTOR_BOT_TOKEN"); got != "tk_abc123" {
		t.Errorf("VIKTOR_BOT_TOKEN = %q, want clean token", got)
	}
	if got := os.Getenv("VIKTOR_PLAIN"); got != "tk_def" {
		t.Errorf("VIKTOR_PLAIN = %q", got)
	}
	if got := os.Getenv("VIKTOR_QUOTED"); got != "tk_g # not comment" {
		t.Errorf("VIKTOR_QUOTED = %q", got)
	}
}
