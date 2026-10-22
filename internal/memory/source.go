package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"

	"viktor/internal/client"
	"viktor/internal/shadow"
)

// TaskBatch is the result of one scan. Complete reports whether the
// scan covered the whole board: only a complete scan may drive ghost
// deletion, because on a filtered delta scan an absent task means
// "unchanged", not "deleted" (card #821).
type TaskBatch struct {
	Tasks    []Task
	Complete bool
}

// TaskSource abstracts where tasks come from: the live Vikunja API or the
// shadow mirror when the tracker is unreachable (ADR 0003 fallback).
type TaskSource interface {
	// AllTasks returns tasks across all projects. updatedSince is an
	// RFC3339 instant; "" requests a full scan, non-empty asks the
	// source to return only tasks updated at or after it. Sources that
	// cannot filter return everything and mark the batch Complete.
	AllTasks(ctx context.Context, updatedSince string) (TaskBatch, error)
	// Comments returns the comments of one task.
	Comments(ctx context.Context, taskID int64) ([]Comment, error)
	// Name describes the source in the sync report.
	Name() string
}

// APISource reads tasks and comments from the live Vikunja API.
type APISource struct {
	api *client.Client
}

// NewAPISource wraps a Vikunja client.
func NewAPISource(api *client.Client) *APISource {
	return &APISource{api: api}
}

// Name implements TaskSource.
func (s *APISource) Name() string { return "api" }

// AllTasks implements TaskSource. With updatedSince set, the board is
// fetched with the server-side filter `updated >= <since>` so an
// incremental sync transfers only the delta (card #821). The window is
// closed (`>=`, not `>`) to also cover tasks updated in the same second
// as the cursor; unchanged chunks are skipped by hash comparison. If
// the tracker rejects the filter, the unfiltered scan runs as fallback.
func (s *APISource) AllTasks(ctx context.Context, updatedSince string) (TaskBatch, error) {
	if updatedSince != "" {
		var delta []Task
		p := client.ListParams{Filter: "updated >= '" + updatedSince + "'"}
		if err := s.api.ListTasks(ctx, p, &delta); err == nil {
			return TaskBatch{Tasks: delta}, nil
		}
	}
	var tasks []Task
	if err := s.api.ListTasks(ctx, client.ListParams{}, &tasks); err != nil {
		return TaskBatch{}, err
	}
	return TaskBatch{Tasks: tasks, Complete: true}, nil
}

// Comments implements TaskSource.
func (s *APISource) Comments(ctx context.Context, taskID int64) ([]Comment, error) {
	var out []Comment
	if err := s.api.TaskComments(ctx, int(taskID), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ShadowSource reads task snapshots from the local shadow mirror.
// The mirror has no comments and no updated timestamps; chunking
// falls back to title+description+labels only.
type ShadowSource struct {
	store *shadow.Store
}

// NewShadowSource wraps a shadow store.
func NewShadowSource(store *shadow.Store) *ShadowSource {
	return &ShadowSource{store: store}
}

// Name implements TaskSource.
func (s *ShadowSource) Name() string { return "shadow-mirror" }

// AllTasks implements TaskSource by loading every task snapshot from
// every mirrored project. The mirror cannot filter by update time, so
// every scan is complete.
func (s *ShadowSource) AllTasks(_ context.Context, _ string) (TaskBatch, error) {
	var out []Task
	for _, ref := range s.store.Projects() {
		for _, snap := range s.store.TasksOfProject(ref) {
			t := Task{
				ID:          snap.ID,
				Title:       snap.Title,
				Description: snap.Description,
				Done:        snap.Done,
			}
			if t.ID == 0 {
				// Shadow-only task: derive a stable pseudo id from the shadow id.
				t.ID = pseudoID(snap.ShadowID)
			}
			for _, l := range snap.Labels {
				t.Labels = append(t.Labels, Label{ID: l.ID, Title: l.Title})
			}
			out = append(out, t)
		}
	}
	return TaskBatch{Tasks: out, Complete: true}, nil
}

// Comments implements TaskSource: the mirror holds no comments.
func (s *ShadowSource) Comments(_ context.Context, _ int64) ([]Comment, error) {
	return nil, nil
}

// pseudoID hashes a shadow id into a stable positive int64.
func pseudoID(shadowID string) int64 {
	if shadowID == "" {
		return 0
	}
	return int64(fnv64a("shadow/"+shadowID) & 0x7FFFFFFFFFFFFFFF)
}

// NewSource returns the API source with shadow-mirror fallback:
// when the tracker is unreachable (connection-level failure only; HTTP
// errors from the tracker are real answers, not offline), tasks come
// from the local shadow mirror.
func NewSource(api *client.Client, store *shadow.Store) TaskSource {
	return &fallbackSource{api: NewAPISource(api), shadow: store}
}

type fallbackSource struct {
	api    *APISource
	shadow *shadow.Store
}

// Name implements TaskSource.
func (s *fallbackSource) Name() string { return "api" }

// AllTasks implements TaskSource with shadow fallback on ErrUnreachable.
func (s *fallbackSource) AllTasks(ctx context.Context, updatedSince string) (TaskBatch, error) {
	batch, err := s.api.AllTasks(ctx, updatedSince)
	if err == nil {
		return batch, nil
	}
	if errors.Is(err, client.ErrUnreachable) {
		return NewShadowSource(s.shadow).AllTasks(ctx, "")
	}
	return TaskBatch{}, err
}

// Comments implements TaskSource with shadow fallback on ErrUnreachable.
func (s *fallbackSource) Comments(ctx context.Context, taskID int64) ([]Comment, error) {
	comments, err := s.api.Comments(ctx, taskID)
	if err == nil {
		return comments, nil
	}
	if errors.Is(err, client.ErrUnreachable) {
		return nil, nil
	}
	return nil, err
}

// fnv64a is the FNV-1a hash used for deterministic point IDs and text hashes.
func fnv64a(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

// pointID derives a deterministic Qdrant point id from the instance and
// chunk key, so re-indexing the same chunk replaces the same point.
func pointID(instance, chunkKey string) uint64 {
	return fnv64a(instance + "/" + chunkKey)
}

// hashText hashes chunk text for change detection.
func hashText(text string) string {
	return strconv.FormatUint(fnv64a(text), 16)
}

// statePath returns <state-root>/memory/<instance>/sync.json.
func statePath(root, instance string) string {
	return filepath.Join(root, "memory", instance, "sync.json")
}

// FNV64a is the exported stable 64-bit hash used for deterministic
// Qdrant point IDs.
func FNV64a(s string) uint64 { return fnv64a(s) }
