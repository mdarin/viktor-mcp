// Package memory implements the semantic memory layer (ADR 0003):
// an incremental indexer that pulls tasks and comments from the tracker,
// chunks them, embeds them via Ollama and upserts them into Qdrant so a
// stateless agent can recover project context with one semantic search.
//
// Qdrant is only a semantic projection: Vikunja, git and repowise remain
// the sources of truth. The index is write-only from the indexer; agents
// never mutate it.
package memory

import (
	"fmt"
	"time"
)

// Config holds the memory layer settings for one tracker instance.
type Config struct {
	Instance   string // tracker instance name (used to namespace point IDs)
	QdrantURL  string // default http://localhost:6333
	OllamaURL  string // default http://localhost:11434
	EmbedModel string // default bge-m3
	Collection string // default viktor_tasks
	// QdrantAPIKey enables the `api-key` header for secured Qdrant
	// instances; empty = no header.
	QdrantAPIKey string
	GitRepo      string // optional per-instance repo path for commit enrichment
	// FullSyncMaxAge is how long a full rescan may be overdue before
	// status reports the index as stale (card #836). 0 → 24h.
	FullSyncMaxAge time.Duration
	// FilePatterns selects repo files for indexing (card #842); a
	// pattern without "/" matches the base name at any depth.
	// Empty → ["*.md"].
	FilePatterns []string
	// HybridPolicy selects the search_hybrid merge ordering (card
	// #841): "strict-slots" (default) appends memory rows after the
	// exact rows; "memory-first" puts them at the head of the merged
	// list. Empty → strict-slots.
	HybridPolicy string
}

const (
	HybridPolicyStrict      = "strict-slots"
	HybridPolicyMemoryFirst = "memory-first"
)

// HybridPolicyOrDefault resolves the merge policy; anything unknown
// falls back to strict-slots so hand-edited configs can't break merge.
func (c Config) HybridPolicyOrDefault() string {
	if c.HybridPolicy == HybridPolicyMemoryFirst {
		return HybridPolicyMemoryFirst
	}
	return HybridPolicyStrict
}

func (c *Config) normalize() {
	if c.QdrantURL == "" {
		c.QdrantURL = "http://localhost:6333"
	}
	if c.OllamaURL == "" {
		c.OllamaURL = "http://localhost:11434"
	}
	if c.EmbedModel == "" {
		c.EmbedModel = "bge-m3"
	}
	if c.Collection == "" {
		c.Collection = "viktor_tasks"
	}
	if c.Instance == "" {
		c.Instance = "default"
	}
	if c.FullSyncMaxAge <= 0 {
		c.FullSyncMaxAge = 24 * time.Hour
	}
}

// WithDefaults returns a normalized copy — exported for tool handlers
// and server wiring that build a Config straight from user config.
func (c Config) WithDefaults() Config {
	c.normalize()
	return c
}

func (c *Config) validate() error {
	if c.Collection == "" {
		return fmt.Errorf("memory: collection name is required")
	}
	return nil
}
