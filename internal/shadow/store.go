// Package shadow implements the offline fallback queue: when the Vikunja
// tracker is unreachable, write operations are recorded locally as a
// pending queue (queue.jsonl) plus a human-readable project mirror, and
// replayed onto the board once connectivity is restored.
package shadow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Entry is one queued operation in the journal.
type Entry struct {
	Seq         int64           `json:"seq"`
	TS          string          `json:"ts"`
	Tool        string          `json:"tool"`
	Op          string          `json:"op"` // create|update|complete|label|comment
	ShadowID    string          `json:"shadow_id,omitempty"`
	RemoteID    int64           `json:"remote_id,omitempty"`
	ProjectID   int64           `json:"project_id,omitempty"`
	Fingerprint string          `json:"fingerprint,omitempty"`
	Status      string          `json:"status"` // pending|synced|failed
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// Store is the on-disk shadow queue for one tracker instance.
type Store struct {
	root       string // <root>/<instance>
	mu         sync.Mutex
	nextSeq    int64
	nextShadow int64
}

// DefaultRoot returns the default shadow root directory
// (~/.local/state/viktor/shadow, honouring XDG_STATE_HOME).
func DefaultRoot() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return filepath.Join(os.TempDir(), "viktor-shadow")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "viktor", "shadow")
}

// NewStore opens (and creates) the shadow store for an instance.
func NewStore(root, instance string) (*Store, error) {
	if root == "" {
		root = DefaultRoot()
	}
	if instance == "" {
		instance = "default"
	}
	s := &Store{root: filepath.Join(root, instance)}
	for _, dir := range []string{s.root, s.projectsRoot()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("shadow: create %s: %w", dir, err)
		}
	}
	s.scanSeq()
	return s, nil
}

func (s *Store) projectsRoot() string { return filepath.Join(s.root, "projects") }

// Root returns the store directory.
func (s *Store) Root() string { return s.root }

func (s *Store) queuePath() string { return filepath.Join(s.root, "queue.jsonl") }

// scanSeq restores sequence counters after a restart.
func (s *Store) scanSeq() {
	entries, _ := s.ReadAll()
	for _, e := range entries {
		if e.Seq > s.nextSeq {
			s.nextSeq = e.Seq
		}
		if id, ok := parseShadowID(e.ShadowID); ok && id > s.nextShadow {
			s.nextShadow = id
		}
	}
}

func parseShadowID(id string) (int64, bool) {
	if !strings.HasPrefix(id, "s-") {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "s-"), 10, 64)
	return n, err == nil
}

// Append writes one journal entry atomically (single append under lock).
func (s *Store) Append(e *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339)
	}
	if e.Status == "" {
		e.Status = "pending"
	}
	s.nextSeq++
	e.Seq = s.nextSeq
	if e.ShadowID == "" {
		s.nextShadow++
		e.ShadowID = fmt.Sprintf("s-%d", s.nextShadow)
	}
	f, err := os.OpenFile(s.queuePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("shadow: open journal: %w", err)
	}
	defer func() { _ = f.Close() }()
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("shadow: marshal entry: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("shadow: append journal: %w", err)
	}
	return f.Sync()
}

// ReadAll returns every journal entry in order.
func (s *Store) ReadAll() ([]Entry, error) {
	f, err := os.Open(s.queuePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip torn trailing line
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Pending returns non-synced entries in order.
func (s *Store) Pending() []Entry {
	all, _ := s.ReadAll()
	var out []Entry
	for _, e := range all {
		if e.Status == "pending" {
			out = append(out, e)
		}
	}
	return out
}

// PendingCount returns the number of queued operations.
func (s *Store) PendingCount() int { return len(s.Pending()) }

// Update rewrites the journal with fn applied to each entry.
func (s *Store) Update(fn func(Entry) Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.ReadAll()
	if err != nil {
		return err
	}
	for i := range entries {
		entries[i] = fn(entries[i])
	}
	return s.rewrite(entries)
}

// MarkSynced stamps an entry with its remote task id.
func (s *Store) MarkSynced(shadowID string, remoteID int64) error {
	return s.Update(func(e Entry) Entry {
		if e.ShadowID == shadowID {
			e.Status = "synced"
			e.RemoteID = remoteID
		}
		return e
	})
}

// ClearSynced removes synced entries and keeps pending ones.
func (s *Store) ClearSynced() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.ReadAll()
	if err != nil {
		return err
	}
	var keep []Entry
	for _, e := range entries {
		if e.Status != "synced" {
			keep = append(keep, e)
		}
	}
	return s.rewrite(keep)
}

func (s *Store) rewrite(entries []Entry) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	tmp := s.queuePath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.queuePath())
}
