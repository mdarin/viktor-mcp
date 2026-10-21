package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func newObservedFixture(t *testing.T, delay, toolTimeout time.Duration) (*mcp.ClientSession, *Registry, *bytes.Buffer) {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		time.Sleep(delay)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v2/tasks/404" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"code":404,"message":"nope"}`)
			return
		}
		fmt.Fprint(w, `{"id":1,"title":"ok"}`)
	}))
	t.Cleanup(srv.Close)

	api := client.New(srv.URL, "tk", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	registry := NewRegistry(srvMCP, api)
	var buf bytes.Buffer
	registry.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	registry.SetToolTimeout(toolTimeout)
	registry.RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "tc", Version: "0.0.0"}, nil)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srvMCP.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	session, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, registry, &buf
}

func firstLogRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.Split(strings.TrimSpace(buf.String()), "\n")[0]
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("log line is not JSON: %q", line)
	}
	return rec
}

func TestInstrumentLogsAndCounts(t *testing.T) {
	session, registry, buf := newObservedFixture(t, 0, 0)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "task_batch_get",
		Arguments: map[string]any{"task_ids": []int{1}},
	})
	if err != nil || res.IsError {
		t.Fatalf("call failed: %v %v", err, res.IsError)
	}
	rec := firstLogRecord(t, buf)
	if rec["msg"] != "tool call" || rec["tool"] != "task_batch_get" || rec["status"] != "ok" {
		t.Errorf("log record = %v", rec)
	}
	if _, ok := rec["duration_ms"]; !ok {
		t.Error("duration_ms missing")
	}

	snap := registry.StatsSnapshot()
	if len(snap) != 1 || snap[0].Tool != "task_batch_get" || snap[0].Calls != 1 || snap[0].Failures != 0 {
		t.Errorf("stats = %+v", snap)
	}
}

func TestInstrumentCountsFailures(t *testing.T) {
	session, registry, buf := newObservedFixture(t, 0, 0)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "task_batch_get",
		Arguments: map[string]any{"task_ids": []int{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected tool error for empty task_ids")
	}
	if !strings.Contains(buf.String(), `"status":"error"`) {
		t.Errorf("no error status logged: %s", buf.String())
	}
	snap := registry.StatsSnapshot()
	if len(snap) != 1 || snap[0].Calls != 1 || snap[0].Failures != 1 {
		t.Errorf("stats = %+v", snap)
	}
}

func TestInstrumentSlowToolLogged(t *testing.T) {
	session, _, buf := newObservedFixture(t, 120*time.Millisecond, 0)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "task_batch_get",
		Arguments: map[string]any{"task_ids": []int{1}},
	})
	if err != nil || res.IsError {
		t.Fatalf("call failed: %v %v", err, res.IsError)
	}
	rec := firstLogRecord(t, buf)
	ms, _ := rec["duration_ms"].(float64)
	if ms < 100 {
		t.Errorf("duration_ms = %v, want >= 100", rec["duration_ms"])
	}
}

func TestToolTimeout(t *testing.T) {
	session, _, _ := newObservedFixture(t, 500*time.Millisecond, 50*time.Millisecond)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "task_batch_get",
		Arguments: map[string]any{"task_ids": []int{1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out == nil {
		t.Fatal("no structured content")
	}
	errs := out["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want 1 deadline entry", errs)
	}
	if s := fmt.Sprint(errs[0]); !strings.Contains(s, "deadline") && !strings.Contains(s, "timeout") {
		t.Errorf("unexpected error entry: %s", s)
	}
}
