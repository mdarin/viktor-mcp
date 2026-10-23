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
	// Agents maps agent identity (opencode, crewai, ...) to that
	// agent's bot token on this instance (card #616). The human
	// operator's Token stays the admin path for bot_* management.
	Agents map[string]string
}

// TokenFor resolves the token for an agent identity: the per-agent bot
// token when configured, otherwise the operator token. An empty agent
// name means the operator identity.
func (i *Instance) TokenFor(agent string) string {
	if agent != "" && i.Agents != nil {
		if t, ok := i.Agents[agent]; ok && t != "" {
			return t
		}
	}
	return i.Token
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
	// FullSyncHours is how often a full rescan (the only pass that can
	// see soft-deleted tasks) should run; 0 → built-in default of 24h
	// (card #836: Vikunja has no deleted_at filter, ghosts die on the
	// periodic full sync).
	FullSyncHours int
	// FilePatterns selects repo files for memory indexing (card #842);
	// empty → "*.md".
	FilePatterns []string
	// GitEnrich maps instance name → path to the git repo whose commit
	// history enriches that instance's task memory. Per-instance by
	// design: multi-instance setups track different repos.
	GitEnrich map[string]string
	// HybridPolicy selects search_hybrid merge ordering (card #841):
	// "strict-slots" | "memory-first"; empty → strict-slots.
	HybridPolicy string
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

// FullSyncHoursOrDefault returns the configured full-sync cadence in
// hours; a nil section or 0 means the built-in default of 24.
func (m *MemorySection) FullSyncHoursOrDefault() int {
	if m == nil || m.FullSyncHours <= 0 {
		return 24
	}
	return m.FullSyncHours
}

// RepowiseSection enables the optional repowise change-risk digest in
// verify_review (card #823). Absent section → the feature is off and
// verify_review behaves exactly as before. The digest is strictly
// non-blocking: CLI failures degrade to an "unavailable" line.
type RepowiseSection struct {
	CLI     string
	Repo    string
	Timeout time.Duration
}

// CLIPath resolves the repowise binary (PATH when unset).
func (s *RepowiseSection) CLIPath() string {
	if s == nil || s.CLI == "" {
		return "repowise"
	}
	return s.CLI
}

// TimeoutFor resolves the CLI timeout (90s when unset).
func (s *RepowiseSection) TimeoutFor() time.Duration {
	if s == nil || s.Timeout <= 0 {
		return 90 * time.Second
	}
	return s.Timeout
}

// BoardSection defines the default kanban board profile (card #845): the
// column titles kanban_setup creates and board_audit checks for. Resolution
// order: explicit tool argument > ./.viktor.json (board.profile) > global
// config board.profile > builtin default.
type BoardSection struct {
	Profile []string
}

// DefaultBoardProfile is the builtin column set.
var DefaultBoardProfile = []string{"todo", "in-progress", "done"}

// ProfileFor returns the effective profile (builtin default when unset).
func (s *BoardSection) ProfileFor() []string {
	if s == nil || len(s.Profile) == 0 {
		return append([]string(nil), DefaultBoardProfile...)
	}
	return s.Profile
}

// LoadRepoBoard reads board settings from a repo-local .viktor.json:
// {"board": {"profile": ["todo", "doing", "done"]}}. Missing file → nil, nil.
func LoadRepoBoard(dir string) (*BoardSection, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".viktor.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var fc struct {
		Board *struct {
			Profile []string `json:"profile"`
		} `json:"board"`
	}
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf(".viktor.json: invalid JSON: %w", err)
	}
	if fc.Board == nil {
		return nil, nil
	}
	return &BoardSection{Profile: fc.Board.Profile}, nil
}

type Config struct {
	Path            string
	DefaultInstance string
	Instances       map[string]*Instance
	Memory          *MemorySection
	// Repowise is the optional change-risk digest config (card #823);
	// nil means the feature is off.
	Repowise *RepowiseSection
	// Board is the optional kanban board profile config (card #845); nil
	// means the builtin default profile applies.
	Board *BoardSection
	// Agent is the identity this server run serves (env VIKUNJA_AGENT);
	// empty means the operator token is used everywhere.
	Agent string
}

type fileConfig struct {
	DefaultInstance string                    `json:"default_instance"`
	Instances       map[string]fileInstanceIn `json:"instances"`
	Memory          *fileMemoryIn             `json:"memory"`
	Repowise        *fileRepowiseIn           `json:"repowise"`
	Board           *BoardSection             `json:"board"`
}

type fileRepowiseIn struct {
	CLI     string `json:"cli"`
	Repo    string `json:"repo"`
	Timeout string `json:"timeout"`
}

type fileMemoryIn struct {
	QdrantURL    string `json:"qdrant_url"`
	OllamaURL    string `json:"ollama_url"`
	EmbedModel   string `json:"embed_model"`
	Collection   string `json:"collection"`
	QdrantAPIKey string `json:"qdrant_api_key"`
	// FullSyncHours paces the periodic full rescan (ghost cleanup);
	// number of hours, 0 = default (24).
	FullSyncHours int `json:"full_sync_hours"`
	// FilePatterns selects repo markdown files for indexing; empty →
	// ["*.md"].
	FilePatterns []string          `json:"file_patterns"`
	HybridPolicy string            `json:"hybrid_policy"`
	GitEnrich    map[string]string `json:"git_enrich"`
}

type fileInstanceIn struct {
	URL       string            `json:"url"`
	Token     string            `json:"token"`
	Timeout   string            `json:"timeout"`
	RateLimit float64           `json:"rate_limit"`
	API       string            `json:"api_version"`
	Agents    map[string]string `json:"agents"`
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
	cfg.applyBot(envVals)
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
		return nil, true, friendlyJSON(err)
	}
	return &fc, true, nil
}

// friendlyJSON rewrites encoding/json type errors into actionable
// messages: a bare "cannot unmarshal number into Go struct field
// fileRepowiseIn.repowise.timeout of type string" names neither the
// config file nor the fix. The known trap (card #839) is a duration
// written as a number ("timeout": 90) — durations must be strings
// like "90s".
func friendlyJSON(err error) error {
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	expected := te.Type.Kind().String()
	if strings.HasSuffix(strings.ToLower(te.Field), "timeout") {
		expected = `duration string (e.g. "90s")`
	}
	return fmt.Errorf("field %q: expected %s, got %s", te.Field, expected, te.Value)
}

func (c *Config) applyFile(fc *fileConfig) error {
	names := make([]string, 0, len(fc.Instances))
	for name := range fc.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fi := fc.Instances[name]
		inst := &Instance{URL: strings.TrimRight(fi.URL, "/"), Token: fi.Token, RateLimit: fi.RateLimit, API: fi.API, Agents: fi.Agents}
		if fi.Timeout != "" {
			d, err := time.ParseDuration(fi.Timeout)
			if err != nil {
				return fmt.Errorf("instance %q: timeout: %w (durations are strings like \"90s\")", name, err)
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
			QdrantURL:     fc.Memory.QdrantURL,
			OllamaURL:     fc.Memory.OllamaURL,
			EmbedModel:    fc.Memory.EmbedModel,
			Collection:    fc.Memory.Collection,
			QdrantAPIKey:  fc.Memory.QdrantAPIKey,
			GitEnrich:     fc.Memory.GitEnrich,
			FullSyncHours: fc.Memory.FullSyncHours,
			FilePatterns:  fc.Memory.FilePatterns,
			HybridPolicy:  fc.Memory.HybridPolicy,
		}
	}
	if fc.Repowise != nil {
		rw := &RepowiseSection{CLI: fc.Repowise.CLI, Repo: fc.Repowise.Repo}
		if t := strings.TrimSpace(fc.Repowise.Timeout); t != "" {
			d, err := time.ParseDuration(t)
			if err != nil {
				return fmt.Errorf("repowise: timeout: %w (durations are strings like \"90s\")", err)
			}
			rw.Timeout = d
		}
		c.Repowise = rw
	}
	if fc.Board != nil {
		c.Board = fc.Board
	}
	return nil
}

func (c *Config) applyEnv(env map[string]string) error {
	if v := env["VIKUNJA_DEFAULT_INSTANCE"]; v != "" {
		c.DefaultInstance = v
	}
	if v := env["VIKUNJA_AGENT"]; v != "" {
		c.Agent = strings.TrimSpace(v)
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

// applyBot wires the agent bot identity from the environment
// (card #616): VIKTOR_BOT_NAME/VIKTOR_BOT_TOKEN (the .env convention,
// VIKUNJA_BOT_* aliases accepted) register a per-instance bot token.
// The identity is only registered, never auto-selected: maintenance
// CLI paths (memory sync, watchdog) load the same .env and must keep
// running under the operator token — a bot sees only its shared
// projects, so an implicit switch would silently index an empty board.
// Acting as the bot requires an explicit VIKUNJA_AGENT=<bot name>.
func (c *Config) applyBot(env map[string]string) {
	name := env["VIKTOR_BOT_NAME"]
	tok := env["VIKTOR_BOT_TOKEN"]
	if name == "" || tok == "" {
		name = env["VIKUNJA_BOT_NAME"]
		tok = env["VIKUNJA_BOT_TOKEN"]
	}
	if name == "" || tok == "" {
		return
	}
	instName := c.DefaultInstance
	if v := env["VIKUNJA_BOT_INSTANCE"]; v != "" {
		instName = v
	}
	inst, ok := c.Instances[instName]
	if !ok {
		return
	}
	if inst.Agents == nil {
		inst.Agents = map[string]string{}
	}
	inst.Agents[name] = tok
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
