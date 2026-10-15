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
)

// Config holds the memory layer settings for one tracker instance.
type Config struct {
	Instance   string // tracker instance name (used to namespace point IDs)
	QdrantURL  string // default http://localhost:6333
	OllamaURL  string // default http://localhost:11434
	EmbedModel string // default bge-m3
	Collection string // default viktor_tasks
	GitRepo    string // optional per-instance repo path for commit enrichment
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
