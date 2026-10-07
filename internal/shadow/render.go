package shadow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TaskSnapshot is the minimal task shape used for the MD mirror.
type TaskSnapshot struct {
	ID          int64           `json:"id"`
	ShadowID    string          `json:"shadow_id,omitempty"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Done        bool            `json:"done"`
	Priority    int64           `json:"priority"`
	DueDate     string          `json:"due_date"`
	Labels      []LabelSnapshot `json:"labels,omitempty"`
	BucketID    int64           `json:"bucket_id,omitempty"`
}

// LabelSnapshot is a label reference on a mirrored task.
type LabelSnapshot struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// RenderProject writes the human-readable project.md mirror.
func (s *Store) RenderProject(projectID int64, projectTitle string) error {
	dir := s.ProjectDir(projectID, projectTitle)
	keys, err := s.TaskKeys(projectID, projectTitle)
	if err != nil {
		return err
	}
	type row struct {
		snap TaskSnapshot
		key  string
	}
	var rows []row
	for _, k := range keys {
		var snap TaskSnapshot
		if err := s.LoadTask(projectID, projectTitle, k, &snap); err != nil {
			continue
		}
		rows = append(rows, row{snap: snap, key: k})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].snap.Title < rows[j].snap.Title })

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", projectTitle)
	fmt.Fprintf(&b, "> Shadow mirror generated %s. NOT the source of truth — state lives in tasks/*.json.\n\n", time.Now().UTC().Format(time.RFC3339))
	open, done := 0, 0
	for _, r := range rows {
		if r.snap.Done {
			done++
		} else {
			open++
		}
	}
	fmt.Fprintf(&b, "Tasks: %d (open %d, done %d)\n\n", len(rows), open, done)

	if len(rows) == 0 {
		b.WriteString("(no cards)\n")
	}
	for _, r := range rows {
		status := "open"
		if r.snap.Done {
			status = "done"
		}
		fmt.Fprintf(&b, "## %s\n\n", r.snap.Title)
		fmt.Fprintf(&b, "- status: %s\n", status)
		if r.snap.ShadowID != "" {
			fmt.Fprintf(&b, "- shadow: %s (pending sync)\n", r.snap.ShadowID)
		}
		if r.snap.ID != 0 {
			fmt.Fprintf(&b, "- remote id: %d\n", r.snap.ID)
		}
		if r.snap.DueDate != "" && !strings.HasPrefix(r.snap.DueDate, "0001") {
			fmt.Fprintf(&b, "- due: %s\n", r.snap.DueDate)
		}
		if r.snap.Priority != 0 {
			fmt.Fprintf(&b, "- priority: %d\n", r.snap.Priority)
		}
		if len(r.snap.Labels) > 0 {
			var titles []string
			for _, l := range r.snap.Labels {
				titles = append(titles, l.Title)
			}
			fmt.Fprintf(&b, "- labels: %s\n", strings.Join(titles, ", "))
		}
		if d := strings.TrimSpace(r.snap.Description); d != "" {
			b.WriteString("\n" + d + "\n")
		}
		b.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(dir, "project.md"), []byte(b.String()), 0o600)
}

// LoadMirrorTask is a convenience helper for read-tool middleware: loads a
// task snapshot by numeric key (remote id) or shadow id ("s-N").
func (s *Store) LoadMirrorTask(projectID int64, projectTitle, key string, out any) bool {
	if err := s.LoadTask(projectID, projectTitle, key, out); err == nil {
		return true
	}
	return false
}

// ParseTaskKey converts "123" or "s-7" to a filesystem-safe key.
func ParseTaskKey(id string) (string, error) {
	if strings.HasPrefix(id, "s-") {
		n, err := strconv.ParseInt(strings.TrimPrefix(id, "s-"), 10, 64)
		if err != nil || n <= 0 {
			return "", fmt.Errorf("bad shadow id %q", id)
		}
		return id, nil
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return "", fmt.Errorf("bad task id %q", id)
	}
	return id, nil
}

var _ = json.Marshal // keep encoding/json for snapshots
