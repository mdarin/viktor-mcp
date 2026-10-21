package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultInstanceEnvPrefix = "VIKUNJA_"

type Instance struct {
	URL       string
	Token     string
	Timeout   time.Duration
	RateLimit float64
	// API is the URL prefix version: "v2" (default) or "v1" rollback
	// for legacy instances (card #59).
	API string
}

// MemorySection is the semantic memory layer configuration (ADR 0003).
// Absent section → memory tools answer "not configured" and the CLI
// sync fails with a hint; nil is a valid, degraded state.
type MemorySection struct {
	QdrantURL  string
	OllamaURL  string
	EmbedModel string
	Collection string
	// QdrantAPIKey enables the `api-key` header for secured Qdrant
	// instances (Helm chart service_api_key). Empty = no header.
	QdrantAPIKey string
	// GitEnrich maps instance name → path to the git repo whose commit
	// history enriches that instance's task memory. Per-instance by
	// design: multi-instance setups track different repos.
	GitEnrich map[string]string
}

// Qdrant returns the Qdrant URL of the memory section; a nil section
// means "memory not configured" and yields empty values, letting callers
// chain config → env → defaults.
func (m *MemorySection) Qdrant() string {
	if m == nil {
		return ""
	}
	return m.QdrantURL
}

func (m *MemorySection) Ollama() string {
	if m == nil {
		return ""
	}
	return m.OllamaURL
}

func (m *MemorySection) Model() string {
	if m == nil {
		return ""
	}
	return m.EmbedModel
}

func (m *MemorySection) Coll() string {
	if m == nil {
		return ""
	}
	return m.Collection
}

// QdrantKey returns the Qdrant API key of the memory section.
func (m *MemorySection) QdrantKey() string {
	if m == nil {
		return ""
	}
	return m.QdrantAPIKey
}

func (m *MemorySection) RepoFor(instance string) string {
	if m == nil {
		return ""
	}
	return m.GitEnrich[instance]
}

type Config struct {
	Path            string
	DefaultInstance string
	Instances       map[string]*Instance
	Memory          *MemorySection
}

type fileConfig struct {
	DefaultInstance string                    `json:"default_instance"`
	Instances       map[string]fileInstanceIn `json:"instances"`
	Memory          *fileMemoryIn             `json:"memory"`
}

type fileMemoryIn struct {
	QdrantURL  string            `json:"qdrant_url"`
	OllamaURL  string            `json:"ollama_url"`
	EmbedModel string            `json:"embed_model"`
	Collection string            `json:"collection"`
	QdrantAPIKey string          `json:"qdrant_api_key"`
	GitEnrich  map[string]string `json:"git_enrich"`
}

type fileInstanceIn struct {
	URL       string  `json:"url"`
	Token     string  `json:"token"`
	Timeout   string  `json:"timeout"`
	RateLimit float64 `json:"rate_limit"`
	API       string  `json:"api_version"`
}

func DefaultPath() string {
	if p := os.Getenv("VIKUNJA_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "viktor.json"
	}
	return filepath.Join(home, ".config", "viktor", "config.json")
}

func Load() (*Config, error) {
	path := DefaultPath()
	env := os.Environ()
	var warn io.Writer = os.Stderr
	if os.Getenv("VIKUNJA_QUIET") != "" {
		warn = io.Discard
	}
	return load(env, path, warn)
}

func load(env []string, path string, warn io.Writer) (*Config, error) {
	cfg := &Config{Path: path, Instances: map[string]*Instance{}}

	file, hasFile, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if hasFile {
		if err := cfg.applyFile(file); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	envVals := map[string]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			envVals[k] = v
		}
	}
	if err := cfg.applyEnv(envVals); err != nil {
		return nil, err
	}

	cfg.pickDefault()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	for _, inst := range cfg.Instances {
		if inst.Timeout == 0 {
			inst.Timeout = 30 * time.Second
		}
	}
	if hasFile {
		cfg.warnPerms(warn)
	}
	return cfg, nil
}

func readFile(path string) (*fileConfig, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, true, fmt.Errorf("invalid JSON: %w", err)
	}
	return &fc, true, nil
}

func (c *Config) applyFile(fc *fileConfig) error {
	names := make([]string, 0, len(fc.Instances))
	for name := range fc.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fi := fc.Instances[name]
		inst := &Instance{URL: strings.TrimRight(fi.URL, "/"), Token: fi.Token, RateLimit: fi.RateLimit, API: fi.API}
		if fi.Timeout != "" {
			d, err := time.ParseDuration(fi.Timeout)
			if err != nil {
				return fmt.Errorf("instance %q: timeout: %w", name, err)
			}
			inst.Timeout = d
		}
		c.Instances[name] = inst
	}
	if fc.DefaultInstance != "" && c.DefaultInstance == "" {
		c.DefaultInstance = fc.DefaultInstance
	}
	if fc.Memory != nil {
		c.Memory = &MemorySection{
			QdrantURL:    fc.Memory.QdrantURL,
			OllamaURL:    fc.Memory.OllamaURL,
			EmbedModel:   fc.Memory.EmbedModel,
			Collection:   fc.Memory.Collection,
			QdrantAPIKey: fc.Memory.QdrantAPIKey,
			GitEnrich:    fc.Memory.GitEnrich,
		}
	}
	return nil
}

func (c *Config) applyEnv(env map[string]string) error {
	if v := env["VIKUNJA_DEFAULT_INSTANCE"]; v != "" {
		c.DefaultInstance = v
	}
	get := func(inst, key string) (string, bool) {
		if inst == "default" {
			if v, ok := env[defaultInstanceEnvPrefix+key]; ok {
				return v, true
			}
			return "", false
		}
		v, ok := env[defaultInstanceEnvPrefix+strings.ToUpper(inst)+"__"+key]
		return v, ok
	}
	names := map[string]bool{}
	for name := range c.Instances {
		names[name] = true
	}
	for k := range env {
		if rest, ok := strings.CutPrefix(k, defaultInstanceEnvPrefix); ok {
			if inst, key, ok := strings.Cut(rest, "__"); ok && isValidEnvKey(key) {
				names[strings.ToLower(inst)] = true
			} else if isValidEnvKey(rest) {
				names["default"] = true
			}
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		inst := c.Instances[name]
		if inst == nil {
			inst = &Instance{}
			c.Instances[name] = inst
		}
		if v, ok := get(name, "URL"); ok && v != "" {
			inst.URL = strings.TrimRight(v, "/")
		}
		if v, ok := get(name, "TOKEN"); ok && v != "" {
			inst.Token = strings.TrimSpace(v)
		}
		if v, ok := get(name, "TIMEOUT"); ok && v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("instance %q: VIKUNJA timeout: %w", name, err)
			}
			inst.Timeout = d
		}
		if v, ok := get(name, "RATE_LIMIT"); ok && v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return fmt.Errorf("instance %q: RATE_LIMIT: invalid value %q", name, v)
			}
			inst.RateLimit = f
		}
		if v, ok := get(name, "API"); ok && v != "" {
			inst.API = strings.TrimSpace(v)
		}
	}
	return nil
}

func isValidEnvKey(key string) bool {
	switch key {
	case "URL", "TOKEN", "TIMEOUT", "RATE_LIMIT", "API":
		return true
	}
	return false
}

func (c *Config) pickDefault() {
	if c.DefaultInstance != "" {
		return
	}
	if _, ok := c.Instances["default"]; ok {
		c.DefaultInstance = "default"
		return
	}
	names := make([]string, 0, len(c.Instances))
	for name := range c.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		c.DefaultInstance = names[0]
	}
}

func (c *Config) validate() error {
	if len(c.Instances) == 0 {
		return errors.New("VIKUNJA_URL is required (or provide a config file with instances)")
	}
	if c.DefaultInstance == "" {
		return errors.New("no default instance: set VIKUNJA_URL or VIKUNJA_DEFAULT_INSTANCE")
	}
	if _, ok := c.Instances[c.DefaultInstance]; !ok {
		return fmt.Errorf("default instance %q is not configured", c.DefaultInstance)
	}
	names := make([]string, 0, len(c.Instances))
	for name := range c.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		inst := c.Instances[name]
		if inst.URL == "" || inst.Token == "" {
			return fmt.Errorf("instance %q: URL and TOKEN are required", name)
		}
		switch inst.API {
		case "", "v1", "v2":
		default:
			return fmt.Errorf("instance %q: api_version must be \"v1\" or \"v2\", got %q", name, inst.API)
		}
	}
	return nil
}

func (c *Config) warnPerms(warn io.Writer) {
	if warn == nil {
		return
	}
	info, err := os.Stat(c.Path)
	if err != nil {
		return
	}
	if info.Mode().Perm()&0o077 != 0 {
		_, _ = fmt.Fprintf(warn, "viktor: WARNING config file %s is readable by group/others (mode %04o); tokens may leak\n", c.Path, info.Mode().Perm())
	}
}
