package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func newBatchSession(t *testing.T, url string) (*mcp.ClientSession, error) {
	t.Helper()
	api := client.New(url, "test-token", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	NewRegistry(srvMCP, api).RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srvMCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, nil
}

func callBatchGet(t *testing.T, session *mcp.ClientSession, ids []int) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "task_batch_get",
		Arguments: map[string]any{"task_ids": ids},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out == nil {
		t.Fatal("no structured content")
	}
	return out
}

func TestBatchGetOrderAndConcurrency(t *testing.T) {
	var inflight, peak int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inflight++
		if inflight > peak {
			peak = inflight
		}
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":1,"title":"t"}`)
		mu.Lock()
		inflight--
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	session, _ := newBatchSession(t, srv.URL)

	start := time.Now()
	res := callBatchGet(t, session, []int{7, 3, 5, 1, 9, 2})
	elapsed := time.Since(start)

	tasks := res["tasks"].([]any)
	if len(tasks) != 6 {
		t.Fatalf("got %d tasks, want 6", len(tasks))
	}
	if len(res["errors"].([]any)) != 0 {
		t.Fatalf("unexpected errors: %v", res["errors"])
	}
	if peak < 3 {
		t.Errorf("peak concurrency %d, expected parallel execution", peak)
	}
	if elapsed >= 6*60*time.Millisecond {
		t.Errorf("elapsed %v looks sequential", elapsed)
	}
}

func TestBatchGetPartialErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/tasks/404" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"code":404,"message":"not found"}`)
			return
		}
		fmt.Fprint(w, `{"id":1,"title":"ok"}`)
	}))
	t.Cleanup(srv.Close)
	session, _ := newBatchSession(t, srv.URL)

	res := callBatchGet(t, session, []int{1, 404, 2})
	if got := len(res["tasks"].([]any)); got != 2 {
		t.Errorf("tasks = %d, want 2", got)
	}
	errs := res["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %d, want 1", len(errs))
	}
	e := errs[0].(map[string]any)
	if e["task_id"].(float64) != 404 {
		t.Errorf("error task_id = %v", e["task_id"])
	}
}
