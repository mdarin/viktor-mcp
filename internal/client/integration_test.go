package client

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveInfo(t *testing.T) {
	base := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if base == "" || token == "" {
		t.Skip("VIKUNJA_URL/VIKUNJA_TOKEN not set")
	}
	c := New(base, token, 5*time.Second)
	h, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Version == "" {
		t.Error("empty version")
	}
	t.Logf("vikunja version: %s", h.Version)
}

func TestLiveListProjects(t *testing.T) {
	base := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if base == "" || token == "" {
		t.Skip("VIKUNJA_URL/VIKUNJA_TOKEN not set")
	}
	c := New(base, token, 5*time.Second)
	var projects []map[string]any
	if err := c.ListProjects(context.Background(), ListParams{}, &projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) == 0 {
		t.Error("no projects returned")
	}
	t.Logf("projects: %d", len(projects))
}
