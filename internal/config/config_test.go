package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(mapEntries ...string) []string {
	return append([]string(nil), mapEntries...)
}

func TestLoadEnvDefaultInstance(t *testing.T) {
	cfg, err := load(env("VIKUNJA_URL=http://localhost:13456/", "VIKUNJA_TOKEN= tk_abc "), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultInstance != "default" {
		t.Errorf("default instance = %q", cfg.DefaultInstance)
	}
	inst := cfg.Instances["default"]
	if inst == nil || inst.URL != "http://localhost:13456" || inst.Token != "tk_abc" {
		t.Errorf("instance default = %+v", inst)
	}
	if inst.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", inst.Timeout)
	}
}

func TestLoadEnvNamedInstance(t *testing.T) {
	cfg, err := load(env(
		"VIKUNJA_URL=http://main:13456", "VIKUNJA_TOKEN=tk_main",
		"VIKUNJA_WORK__URL=http://work:3456", "VIKUNJA_WORK__TOKEN=tk_work",
		"VIKUNJA_WORK__RATE_LIMIT=12.5",
	), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	work := cfg.Instances["work"]
	if work == nil || work.URL != "http://work:3456" || work.Token != "tk_work" || work.RateLimit != 12.5 {
		t.Errorf("instance work = %+v", work)
	}
}

func TestLoadFileAndEnvPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
		"default_instance": "work",
		"instances": {
			"work": {"url": "http://file:1", "token": "tk_file", "timeout": "45s", "rate_limit": 3}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(env(
		"VIKUNJA_WORK__URL=http://env:2", "VIKUNJA_WORK__TOKEN=tk_env",
	), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	work := cfg.Instances["work"]
	if work.URL != "http://env:2" {
		t.Errorf("url = %q, env must override file", work.URL)
	}
	if work.Token != "tk_env" {
		t.Errorf("token = %q, env must override file", work.Token)
	}
	if work.Timeout != 45*time.Second {
		t.Errorf("timeout = %v, file value should survive", work.Timeout)
	}
	if work.RateLimit != 3 {
		t.Errorf("rate_limit = %v, file value should survive", work.RateLimit)
	}
	if cfg.DefaultInstance != "work" {
		t.Errorf("default instance = %q", cfg.DefaultInstance)
	}
}

func TestLoadFileBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(env("VIKUNJA_URL=http://x", "VIKUNJA_TOKEN=tk"), path, nil); err == nil {
		t.Error("want error for invalid JSON")
	}
}

func TestLoadFilePermsWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"instances": {"default": {"url": "http://x", "token": "tk"}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var warn strings.Builder
	if _, err := load(env(), path, &warn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "WARNING") {
		t.Errorf("expected perms warning, got %q", warn.String())
	}
	var quiet strings.Builder
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(env(), path, &quiet); err != nil {
		t.Fatal(err)
	}
	if quiet.String() != "" {
		t.Errorf("no warning expected for 0600, got %q", quiet.String())
	}
}

func TestLoadNoInstances(t *testing.T) {
	if _, err := load(env(), "", nil); err == nil {
		t.Error("want error when no instances configured")
	}
}

func TestLoadInstanceMissingToken(t *testing.T) {
	_, err := load(env("VIKUNJA_WORK__URL=http://work"), "", nil)
	if err == nil || !strings.Contains(err.Error(), "work") {
		t.Errorf("want error for instance without token, got %v", err)
	}
}

func TestLoadBadTimeout(t *testing.T) {
	if _, err := load(env("VIKUNJA_URL=http://x", "VIKUNJA_TOKEN=tk", "VIKUNJA_TIMEOUT=bogus"), "", nil); err == nil {
		t.Error("want error for bad timeout")
	}
}

func TestLoadBadRateLimit(t *testing.T) {
	if _, err := load(env("VIKUNJA_URL=http://x", "VIKUNJA_TOKEN=tk", "VIKUNJA_RATE_LIMIT=zzz"), "", nil); err == nil {
		t.Error("want error for bad rate limit")
	}
}

func TestLoadDefaultInstanceFromEnv(t *testing.T) {
	cfg, err := load(env(
		"VIKUNJA_URL=http://main", "VIKUNJA_TOKEN=tk_main",
		"VIKUNJA_WORK__URL=http://work", "VIKUNJA_WORK__TOKEN=tk_work",
		"VIKUNJA_DEFAULT_INSTANCE=work",
	), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultInstance != "work" {
		t.Errorf("default instance = %q, want work", cfg.DefaultInstance)
	}
}

func TestLoadDefaultInstanceUnknown(t *testing.T) {
	if _, err := load(env(
		"VIKUNJA_URL=http://main", "VIKUNJA_TOKEN=tk_main",
		"VIKUNJA_DEFAULT_INSTANCE=nope",
	), "", nil); err == nil {
		t.Error("want error for unknown default instance")
	}
}

func TestLoadMemorySection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
		"default_instance": "work",
		"instances": {"work": {"url": "http://x", "token": "tk"}},
		"memory": {
			"qdrant_url": "http://q:6333",
			"ollama_url": "http://o:11434",
			"embed_model": "bge-m3",
			"collection": "viktor_tasks",
			"git_enrich": {"work": "/repo/work", "other": "/repo/other"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(env("VIKUNJA_URL=http://x", "VIKUNJA_TOKEN=tk"), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Memory
	if m == nil {
		t.Fatal("memory section must be parsed")
	}
	if m.QdrantURL != "http://q:6333" || m.OllamaURL != "http://o:11434" || m.EmbedModel != "bge-m3" || m.Collection != "viktor_tasks" {
		t.Fatalf("memory = %+v", m)
	}
	if m.RepoFor("work") != "/repo/work" || m.RepoFor("missing") != "" {
		t.Fatalf("git_enrich = %v", m.GitEnrich)
	}
}

func TestLoadMemorySectionAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"instances": {"default": {"url": "http://x", "token": "tk"}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(env("VIKUNJA_URL=http://x", "VIKUNJA_TOKEN=tk"), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory != nil {
		t.Fatalf("absent section must stay nil, got %+v", cfg.Memory)
	}
	// Nil section is safe to query.
	if cfg.Memory.Qdrant() != "" || cfg.Memory.RepoFor("any") != "" {
		t.Fatal("nil section accessors must be zero")
	}
}

func TestAgentTokenSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
		"default_instance": "work",
		"instances": {
			"work": {"url": "http://w:1", "token": "tk_operator",
				"agents": {"opencode": "tk_bot_opencode", "crewai": "tk_bot_crewai"}}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(env("VIKUNJA_AGENT=opencode"), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent != "opencode" {
		t.Fatalf("agent = %q", cfg.Agent)
	}
	inst := cfg.Instances["work"]
	if got := inst.TokenFor(cfg.Agent); got != "tk_bot_opencode" {
		t.Errorf("TokenFor(opencode) = %q", got)
	}
	if got := inst.TokenFor("unknown-agent"); got != "tk_operator" {
		t.Errorf("TokenFor(unknown) = %q, want operator fallback", got)
	}
	if got := inst.TokenFor(""); got != "tk_operator" {
		t.Errorf("TokenFor(\"\") = %q, want operator token", got)
	}

	cfg2, err := load(env(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Agent != "" {
		t.Fatalf("agent without env = %q, want empty", cfg2.Agent)
	}
	if got := cfg2.Instances["work"].TokenFor(""); got != "tk_operator" {
		t.Errorf("no agent configured: TokenFor = %q", got)
	}
}

func TestApplyBotFromEnv(t *testing.T) {
	cfg, err := load(env(
		"VIKUNJA_URL=http://localhost:13456", "VIKUNJA_TOKEN=tk_operator",
		"VIKTOR_BOT_NAME=bot-viktor-mcp", "VIKTOR_BOT_TOKEN=tk_bot_secret",
	), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent != "" {
		t.Fatalf("agent = %q, want empty: bot identity is never auto-selected", cfg.Agent)
	}
	inst := cfg.Instances["default"]
	if got := inst.TokenFor("bot-viktor-mcp"); got != "tk_bot_secret" {
		t.Errorf("TokenFor(bot) = %q", got)
	}
	if got := inst.TokenFor(""); got != "tk_operator" {
		t.Errorf("operator token = %q", got)
	}

	// Explicit VIKUNJA_AGENT wins over the bot default.
	cfg2, err := load(env(
		"VIKUNJA_URL=http://localhost:13456", "VIKUNJA_TOKEN=tk_operator",
		"VIKTOR_BOT_NAME=bot-x", "VIKTOR_BOT_TOKEN=tk_bot",
		"VIKUNJA_AGENT=crewai-agent",
	), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Agent != "crewai-agent" {
		t.Fatalf("agent = %q, explicit VIKUNJA_AGENT must win", cfg2.Agent)
	}
	if got := cfg2.Instances["default"].TokenFor(cfg2.Agent); got != "tk_operator" {
		t.Errorf("TokenFor(crewai-agent) = %q, want operator fallback", got)
	}
}

func TestLoadRepowiseSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
		"default_instance": "default",
		"instances": {"default": {"url": "http://x:1", "token": "tk"}},
		"repowise": {"cli": "/usr/local/bin/repowise", "repo": "/repo", "timeout": "30s"}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(env(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repowise == nil {
		t.Fatal("repowise section must load")
	}
	if cfg.Repowise.CLI != "/usr/local/bin/repowise" || cfg.Repowise.Repo != "/repo" || cfg.Repowise.Timeout != 30*time.Second {
		t.Fatalf("repowise = %+v", cfg.Repowise)
	}

	// Absent section → off.
	path2 := filepath.Join(t.TempDir(), "config2.json")
	if err := os.WriteFile(path2, []byte(`{"instances": {"default": {"url": "http://x:1", "token": "tk"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = load(env(), path2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repowise != nil {
		t.Fatal("missing section must keep repowise off")
	}
}
