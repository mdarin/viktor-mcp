package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"

	"viktor/internal/client"
	"viktor/internal/shadow"
)

// TaskSource abstracts where tasks come from: the live Vikunja API or the
// shadow mirror when the tracker is unreachable (ADR 0003 fallback).
type TaskSource interface {
	// AllTasks returns every task across all projects.
	AllTasks(ctx context.Context) ([]Task, error)
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

// AllTasks implements TaskSource.
func (s *APISource) AllTasks(ctx context.Context) ([]Task, error) {
	var tasks []Task
	if err := s.api.ListTasks(ctx, client.ListParams{}, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
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
// every mirrored project.
func (s *ShadowSource) AllTasks(ctx context.Context) ([]Task, error) {
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
	return out, nil
}

// Comments implements TaskSource: the mirror holds no comments.
func (s *ShadowSource) Comments(ctx context.Context, taskID int64) ([]Comment, error) {
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
func (s *fallbackSource) AllTasks(ctx context.Context) ([]Task, error) {
	tasks, err := s.api.AllTasks(ctx)
	if err == nil {
		return tasks, nil
	}
	if errors.Is(err, client.ErrUnreachable) {
		return NewShadowSource(s.shadow).AllTasks(ctx)
	}
	return nil, err
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
