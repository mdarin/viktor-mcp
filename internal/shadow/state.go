package shadow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ProjectRef identifies a mirrored project.
type ProjectRef struct {
	ID    int64  `json:"id"`
	Slug  string `json:"slug"` // "<id>-<title-slug>"
	Title string `json:"title"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug builds a filesystem-safe "<id>-<title>" directory name.
func Slug(id int64, title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	t = slugRe.ReplaceAllString(t, "-")
	t = strings.Trim(t, "-")
	if len(t) > 60 {
		t = t[:60]
	}
	if t == "" {
		t = "untitled"
	}
	return fmt.Sprintf("%d-%s", id, t)
}

// ProjectDir returns the directory of a mirrored project.
func (s *Store) ProjectDir(id int64, title string) string {
	return filepath.Join(s.projectsRoot(), Slug(id, title))
}

// SaveProject writes the project record.
func (s *Store) SaveProject(p ProjectRef) error {
	dir := s.ProjectDir(p.ID, p.Title)
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(filepath.Join(dir, "project.json"), data, 0o600)
}

// SaveTask writes a task snapshot (full Vikunja-shaped JSON) into the mirror.
func (s *Store) SaveTask(projectID int64, projectTitle, key string, task any) error {
	if key == "" {
		return fmt.Errorf("shadow: empty task key")
	}
	dir := s.ProjectDir(projectID, projectTitle)
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tasks", key+".json"), data, 0o600)
}

// LoadTask reads a task snapshot.
func (s *Store) LoadTask(projectID int64, projectTitle, key string, out any) error {
	data, err := os.ReadFile(filepath.Join(s.ProjectDir(projectID, projectTitle), "tasks", key+".json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// TaskKeys lists task snapshot keys of a mirrored project.
func (s *Store) TaskKeys(projectID int64, projectTitle string) ([]string, error) {
	dir := filepath.Join(s.ProjectDir(projectID, projectTitle), "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		keys = append(keys, strings.TrimSuffix(name, ".json"))
	}
	return keys, nil
}

// DeleteProject removes a mirrored project directory (after successful sync).
func (s *Store) DeleteProject(id int64, title string) error {
	return os.RemoveAll(s.ProjectDir(id, title))
}
