package shadow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// FindProjectByID locates a mirrored project directory by id.
func (s *Store) FindProjectByID(id int64) (ProjectRef, bool) {
	entries, err := os.ReadDir(s.projectsRoot())
	if err != nil {
		return ProjectRef{}, false
	}
	prefix := strconv.FormatInt(id, 10) + "-"
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		var ref ProjectRef
		data, err := os.ReadFile(filepath.Join(s.projectsRoot(), e.Name(), "project.json"))
		if err != nil {
			// Fall back to the directory name.
			title := strings.TrimPrefix(e.Name(), prefix)
			return ProjectRef{ID: id, Slug: e.Name(), Title: title}, true
		}
		if json.Unmarshal(data, &ref) == nil && ref.ID == id {
			return ref, true
		}
	}
	return ProjectRef{}, false
}

// TasksOfProject loads all task snapshots of a mirrored project.
func (s *Store) TasksOfProject(ref ProjectRef) []TaskSnapshot {
	keys, err := s.TaskKeys(ref.ID, ref.Title)
	if err != nil {
		return nil
	}
	var out []TaskSnapshot
	for _, k := range keys {
		var snap TaskSnapshot
		if s.LoadTask(ref.ID, ref.Title, k, &snap) == nil {
			out = append(out, snap)
		}
	}
	return out
}

// FindTask searches all mirrored projects for a task by remote id or shadow id.
func (s *Store) FindTask(idOrShadow string) (TaskSnapshot, int64, string, bool) {
	entries, err := os.ReadDir(s.projectsRoot())
	if err != nil {
		return TaskSnapshot{}, 0, "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var ref ProjectRef
		data, err := os.ReadFile(filepath.Join(s.projectsRoot(), e.Name(), "project.json"))
		if err == nil {
			_ = json.Unmarshal(data, &ref)
		}
		if ref.ID == 0 {
			if n, perr := strconv.ParseInt(e.Name(), 10, 64); perr == nil {
				ref.ID = n
			} else if idx := strings.Index(e.Name(), "-"); idx > 0 {
				ref.ID, _ = strconv.ParseInt(e.Name()[:idx], 10, 64)
				ref.Title = strings.TrimPrefix(e.Name(), e.Name()[:idx+1])
			}
		}
		var snap TaskSnapshot
		if s.LoadTask(ref.ID, ref.Title, idOrShadow, &snap) == nil {
			return snap, ref.ID, ref.Title, true
		}
		for _, k := range mustTaskKeys(s, ref) {
			if k == idOrShadow {
				continue
			}
		}
	}
	// Second pass: match by shadow id inside snapshots.
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ref, ok := dirRef(e.Name())
		if !ok {
			continue
		}
		for _, snap := range s.TasksOfProject(ref) {
			if snap.ShadowID == idOrShadow || fmt.Sprint(snap.ID) == idOrShadow {
				return snap, ref.ID, ref.Title, true
			}
		}
	}
	return TaskSnapshot{}, 0, "", false
}

func mustTaskKeys(s *Store, ref ProjectRef) []string {
	keys, _ := s.TaskKeys(ref.ID, ref.Title)
	return keys
}

func dirRef(name string) (ProjectRef, bool) {
	idx := strings.Index(name, "-")
	if idx <= 0 {
		return ProjectRef{}, false
	}
	id, err := strconv.ParseInt(name[:idx], 10, 64)
	if err != nil {
		return ProjectRef{}, false
	}
	return ProjectRef{ID: id, Slug: name, Title: strings.TrimPrefix(name, name[:idx+1])}, true
}
