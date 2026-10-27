package tools

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateDoc = flag.Bool("update-doc", false, "rewrite tools table in README.md")

const readmeMarkersError = "README.md has no tools table markers"

func readmePath() string {
	for _, rel := range []string{"../../README.md", "../README.md", "README.md"} {
		if _, err := os.Stat(rel); err == nil {
			return rel
		}
	}
	return ""
}

func TestToolsDocTable(t *testing.T) {
	readme := readmePath()
	if readme == "" {
		t.Skip("README.md not found")
	}
	content, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	// Normalize CRLF: a windows checkout (core.autocrlf=true) leaves
	// \r\n on disk, which must not count as table drift.
	doc := strings.ReplaceAll(string(content), "\r\n", "\n")
	begin := "<!-- tools:begin -->"
	end := "<!-- tools:end -->"
	bi := strings.Index(doc, begin)
	ei := strings.Index(doc, end)
	if bi < 0 || ei < 0 || ei < bi {
		t.Fatal(readmeMarkersError)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(golden, &tools); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("| Инструмент | Описание |\n|---|---|\n")
	for _, tl := range tools {
		desc := strings.ReplaceAll(tl.Description, "|", "\\|")
		fmt.Fprintf(&b, "| `%s` | %s |\n", tl.Name, desc)
	}
	want := b.String()
	actual := doc[bi+len(begin) : ei]

	if *updateDoc {
		updated := doc[:bi+len(begin)] + "\n" + strings.TrimRight(want, "\n") + "\n" + doc[ei:]
		if err := os.WriteFile(readme, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if strings.TrimSpace(actual) != strings.TrimSpace(want) {
		t.Fatalf("tools table in README.md drifted from golden.json; regenerate: go test ./internal/tools -run TestToolsDocTable -update-doc")
	}
}
