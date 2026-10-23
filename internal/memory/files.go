package memory

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// File-indexing limits (card #842): guard the indexer against a
// monorepo blowup. Oversized files and collections beyond the cap are
// skipped and counted in the sync report.
const (
	maxFileBytes = 128 * 1024
	maxFiles     = 1000
	// maxFileChunkChars caps one file chunk; longer documents split
	// into numbered sections at markdown headings.
	maxFileChunkChars = 4000
)

// defaultSkipDirs are never walked when indexing repo files.
var defaultSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
}

// FileChunk is one indexable piece of a repo document.
type FileChunk struct {
	Key     string
	Text    string
	Payload map[string]any
}

// WalkRepoFiles collects markdown (or pattern-matched) files under root
// and splits each into deterministic chunks. Patterns match the slash-
// separated repo-relative path; a pattern without "/" matches the base
// name at any depth (so "*.md" covers kb/x.md). Returns chunks plus the
// number of skipped files (oversized or over the file cap).
func WalkRepoFiles(root string, patterns []string) ([]FileChunk, int, error) {
	if len(patterns) == 0 {
		patterns = []string{"*.md"}
	}
	var files []string
	skipped := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		name := d.Name()
		if p != root && (d.IsDir() || strings.HasPrefix(name, ".")) {
			if d.IsDir() && (defaultSkipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasPrefix(name, ".") {
				return nil // hidden files are not indexed
			}
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if len(files) >= maxFiles {
			skipped++
			return nil
		}
		if !matchAny(rel, name, patterns) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxFileBytes {
			skipped++
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("memory: walk repo files: %w", err)
	}
	sort.Strings(files)

	repo := filepath.Base(root)
	var out []FileChunk
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for i, sec := range splitMarkdown(string(data)) {
			text := strings.TrimSpace(sec)
			if text == "" {
				continue
			}
			key := "file-" + rel
			if i > 0 {
				key = fmt.Sprintf("file-%s#s%d", rel, i)
			}
			out = append(out, FileChunk{
				Key:  key,
				Text: text,
				Payload: map[string]any{
					"chunk_type": "file",
					"path":       rel,
					"repo":       repo,
					"title":      rel,
					"snippet":    snippet(text),
				},
			})
		}
	}
	return out, skipped, nil
}

// matchAny reports whether a file matches any pattern. A pattern with
// "/" matches the rel path via path.Match; otherwise it matches the
// base name at any depth.
func matchAny(rel, base string, patterns []string) bool {
	for _, pat := range patterns {
		if strings.Contains(pat, "/") {
			if ok, err := path.Match(pat, rel); err == nil && ok {
				return true
			}
			continue
		}
		if ok, err := filepath.Match(pat, base); err == nil && ok {
			return true
		}
	}
	return false
}

// splitMarkdown splits a document at markdown headings into
// deterministic sections each under maxFileChunkChars where possible.
func splitMarkdown(doc string) []string {
	if len(doc) <= maxFileChunkChars {
		return []string{doc}
	}
	lines := strings.Split(doc, "\n")
	var sections []string
	var cur []string
	curLen := 0
	flush := func() {
		if len(cur) > 0 {
			sections = append(sections, strings.Join(cur, "\n"))
			cur = nil
			curLen = 0
		}
	}
	for _, ln := range lines {
		isHeading := strings.HasPrefix(ln, "#")
		if isHeading && curLen > maxFileChunkChars {
			flush()
		}
		cur = append(cur, ln)
		curLen += len(ln) + 1
	}
	flush()
	return sections
}
