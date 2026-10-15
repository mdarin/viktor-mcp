package memory

import (
	"fmt"
	"sort"
	"strings"
)

// Label is a label attached to a task.
type Label struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// Task is the minimal Vikunja task shape needed for chunking.
type Task struct {
	ID          int64   `json:"id"`
	ProjectID   int64   `json:"project_id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Done        bool    `json:"done"`
	Updated     string  `json:"updated"`
	Labels      []Label `json:"labels"`
}

// Comment is a task comment.
type Comment struct {
	ID     int64  `json:"id"`
	TaskID int64  `json:"task_id"`
	Text   string `json:"comment"`
	Author string `json:"author"`
}

// ChunkType distinguishes task chunks from comment chunks.
type ChunkType string

const (
	ChunkTypeTask    ChunkType = "task"
	ChunkTypeComment ChunkType = "comment"
)

// Chunk is one indexed unit: the whole task or a single comment.
// Key is stable across syncs ("<chunk-type>-<id>").
type Chunk struct {
	Key     string
	Text    string
	Type    ChunkType
	TaskID  int64
	Payload map[string]any
}

func labelTitles(labels []Label) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if t := strings.TrimSpace(l.Title); t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func status(done bool) string {
	if done {
		return "done"
	}
	return "open"
}

// ChunkTask splits a task into one task chunk plus one chunk per comment.
// Both chunk types carry task_id in the payload — the join key to
// viktor task_get and git log --grep.
func ChunkTask(t Task, comments []Comment, commits []string) []Chunk {
	labels := labelTitles(t.Labels)

	text := strings.TrimSpace(t.Title)
	if d := strings.TrimSpace(t.Description); d != "" {
		text += "\n\n" + d
	}
	if len(labels) > 0 {
		text += "\nlabels: " + strings.Join(labels, ", ")
	}

	taskChunk := Chunk{
		Key:    fmt.Sprintf("task-%d", t.ID),
		Text:   text,
		Type:   ChunkTypeTask,
		TaskID: t.ID,
		Payload: map[string]any{
			"task_id":    t.ID,
			"project_id": t.ProjectID,
			"title":      t.Title,
			"status":     status(t.Done),
			"labels":     labels,
			"updated_at": t.Updated,
			"chunk_type": string(ChunkTypeTask),
		},
	}
	if len(commits) > 0 {
		taskChunk.Payload["commits"] = commits
	}

	chunks := []Chunk{taskChunk}
	title := strings.TrimSpace(t.Title)
	for _, c := range comments {
		ct := strings.TrimSpace(c.Text)
		if ct == "" {
			continue
		}
		by := c.Author
		header := "[task: " + title + "]"
		if by != "" {
			header += " @" + by + ":"
		}
		chunks = append(chunks, Chunk{
			Key:    fmt.Sprintf("comment-%d", c.ID),
			Text:   header + " " + ct,
			Type:   ChunkTypeComment,
			TaskID: t.ID,
			Payload: map[string]any{
				"task_id":    t.ID,
				"project_id": t.ProjectID,
				"title":      t.Title,
				"status":     status(t.Done),
				"labels":     labels,
				"updated_at": t.Updated,
				"chunk_type": string(ChunkTypeComment),
			},
		})
	}
	return chunks
}

// ChunkTypeCommit marks a commit chunk (repo history projection).
const ChunkTypeCommit ChunkType = "commit"

// maxCommitFiles caps files listed in a commit chunk.
const maxCommitFiles = 20

// ChunkCommit builds one chunk per commit. Commits are immutable, so the
// key is the sha and the hash-compare makes repeat syncs free. Task ids
// referenced in the message land in task_ids — the bridge to the board.
func ChunkCommit(c CommitRecord) Chunk {
	files := c.Files
	if len(files) > maxCommitFiles {
		files = files[:maxCommitFiles]
	}
	text := c.Subject
	if len(files) > 0 {
		text += "\nfiles: " + strings.Join(files, ", ")
	}
	payload := map[string]any{
		"title":      c.Subject,
		"sha":        c.SHA,
		"author":     c.Author,
		"date":       c.Date,
		"chunk_type": string(ChunkTypeCommit),
	}
	if len(files) > 0 {
		payload["files"] = files
	}
	if len(c.TaskIDs) > 0 {
		payload["task_ids"] = c.TaskIDs
	}
	return Chunk{
		Key:     "commit-" + c.SHA,
		Text:    text,
		Type:    ChunkTypeCommit,
		TaskID:  0, // a commit may reference many tasks; see payload.task_ids
		Payload: payload,
	}
}
