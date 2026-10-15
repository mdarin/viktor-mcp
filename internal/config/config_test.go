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
