package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// syncState is the local index state for one instance: the newest task
// timestamp seen and per-chunk content hashes used for idempotent syncs
// (unchanged chunks produce zero upserts).
type syncState struct {
	Cursor string            `json:"cursor,omitempty"`
	Hashes map[string]string `json:"hashes"` // chunk key -> hash(text)
}

func loadState(path string) (*syncState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &syncState{Hashes: map[string]string{}}, nil
		}
		return nil, err
	}
	var st syncState
	if err := json.Unmarshal(data, &st); err != nil {
		// A corrupt state file restarts indexing from scratch; never fatal.
		return &syncState{Hashes: map[string]string{}}, nil
	}
	if st.Hashes == nil {
		st.Hashes = map[string]string{}
	}
	return &st, nil
}

func (s *syncState) save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DefaultStateRoot returns the memory state root
// (~/.local/state/viktor, honouring XDG_STATE_HOME) — the same base the
// shadow queue uses.
func DefaultStateRoot() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return filepath.Join(os.TempDir(), "viktor-memory")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "viktor")
}

// DefaultStatePath returns the sync state file path for an instance.
func DefaultStatePath(instance string) string {
	if instance == "" {
		instance = "default"
	}
	return statePath(DefaultStateRoot(), instance)
}
