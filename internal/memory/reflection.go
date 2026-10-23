package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ReflectionRow is the minimal reflection journal record the indexer
// needs (card #853). It mirrors reflection.Record without importing the
// package: the journal (PostgreSQL) is canonical, the vector store only
// projects it.
type ReflectionRow struct {
	ID           int64
	TaskID       int64
	Repo         string
	Goals        map[string]float64
	GoalConflict string
	Resolution   string
	Outcome      string
	Confidence   float64
	CreatedAt    string
}

// ReflectionSource feeds reflection rows to the indexer; wired when a
// PostgreSQL DSN is configured. Rows are read once, by id cursor — the
// journal is append-only, so hash-compare makes repeat syncs free.
type ReflectionSource interface {
	ReflectionsSince(ctx context.Context, afterID int64, limit int) ([]ReflectionRow, error)
}

// ChunkTypeReflection marks a reflection chunk (goal-conflict outcome).
const ChunkTypeReflection ChunkType = "reflection"

// ChunkReflection builds one chunk per reflection record: the indexed
// text carries the conflict, the resolution and the outcome — the
// answer to "why was it built this way" — while goals stay in the
// payload for exact filtering.
func ChunkReflection(r ReflectionRow) Chunk {
	var sb strings.Builder
	sb.WriteString("[task #" + fmt.Sprint(r.TaskID) + " reflection]")
	if r.GoalConflict != "" {
		sb.WriteString("\ngoal_conflict: " + r.GoalConflict)
	}
	if r.Resolution != "" {
		sb.WriteString("\nresolution: " + r.Resolution)
	}
	if r.Outcome != "" {
		sb.WriteString("\noutcome: " + r.Outcome)
	}
	if r.Confidence > 0 {
		sb.WriteString(fmt.Sprintf("\nconfidence: %.2f", r.Confidence))
	}
	if len(r.Goals) > 0 {
		axes := make([]string, 0, len(r.Goals))
		for a := range r.Goals {
			axes = append(axes, a)
		}
		sort.Strings(axes)
		parts := make([]string, 0, len(axes))
		for _, a := range axes {
			parts = append(parts, fmt.Sprintf("%s=%.2f", a, r.Goals[a]))
		}
		sb.WriteString("\ngoals: " + strings.Join(parts, " "))
	}
	text := sb.String()
	payload := map[string]any{
		"task_id":       r.TaskID,
		"chunk_type":    string(ChunkTypeReflection),
		"reflection_id": r.ID,
		"snippet":       snippet(r.Resolution),
	}
	if r.CreatedAt != "" {
		payload["created_at"] = r.CreatedAt
	}
	if r.Outcome != "" {
		payload["outcome"] = r.Outcome
	}
	if r.Repo != "" {
		payload["repo"] = r.Repo
	}
	return Chunk{
		Key:     fmt.Sprintf("reflection-%d", r.ID),
		Text:    text,
		Type:    ChunkTypeReflection,
		TaskID:  r.TaskID,
		Payload: payload,
	}
}
