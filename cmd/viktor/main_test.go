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

func TestLoadDotenvExpansion(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"

	// VIKTOR_DOTENV_PRESET is set in the environment before the file is
	// read: env wins over the file's redefinition, and its value is what
	// ${VIKTOR_DOTENV_PRESET} expands to.
	t.Setenv("VIKTOR_DOTENV_PRESET", "from-env")
	t.Setenv("VIKTOR_DOTENV_KEEP", "")      // file may define
	t.Setenv("VIKTOR_DOTENV_EXP", "")       // ${VAR}
	t.Setenv("VIKTOR_DOTENV_DEF", "")       // ${VAR:-default}
	t.Setenv("VIKTOR_DOTENV_DEF_EMPTY", "") // ${VAR:-} with unset VAR
	t.Setenv("VIKTOR_DOTENV_LIT", "")       // single quotes stay literal
	t.Setenv("VIKTOR_DOTENV_EXP_FILE", "")  // ${VAR} referencing a file var

	content := "" +
		"export VIKTOR_DOTENV_EXPORT=tk_exported\n" +
		"VIKTOR_DOTENV_PRESET=file-loses\n" +
		"VIKTOR_DOTENV_EXP=${VIKTOR_DOTENV_PRESET}\n" +
		"VIKTOR_DOTENV_DEF=${VIKTOR_DOTENV_MISSING:-fallback}\n" +
		"VIKTOR_DOTENV_DEF_EMPTY=${VIKTOR_DOTENV_MISSING:-}\n" +
		"VIKTOR_DOTENV_LIT='${VIKTOR_DOTENV_MISSING:-no}'\n" +
		"VIKTOR_DOTENV_EXP_FILE=${VIKTOR_DOTENV_EXPORT}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	loadDotenv(path)

	cases := map[string]string{
		"VIKTOR_DOTENV_EXPORT":    "tk_exported",                  // export prefix dropped
		"VIKTOR_DOTENV_PRESET":    "from-env",                     // env before file
		"VIKTOR_DOTENV_EXP":       "from-env",                     // ${VAR} → env value
		"VIKTOR_DOTENV_DEF":       "fallback",                     // ${VAR:-default}
		"VIKTOR_DOTENV_DEF_EMPTY": "",                             // ${VAR:-} with unset VAR
		"VIKTOR_DOTENV_LIT":       "${VIKTOR_DOTENV_MISSING:-no}", // single quotes: literal
		"VIKTOR_DOTENV_EXP_FILE":  "tk_exported",                  // ${VAR} → earlier file var
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
