package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "tk_test", 2*time.Second)
	c.backoff = time.Millisecond
	return c, srv
}

func TestBearerAuthHeader(t *testing.T) {
	var got string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	if err := c.Get(context.Background(), "/info", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer tk_test" {
		t.Errorf("Authorization = %q, want Bearer tk_test", got)
	}
}

func TestRetryOn500ThenSuccess(t *testing.T) {
	calls := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]int{"id": 1})
	})
	var out struct {
		ID int `json:"id"`
	}
	if err := c.Get(context.Background(), "/tasks/1", nil, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if out.ID != 1 {
		t.Errorf("id = %d, want 1", out.ID)
	}
}

func TestNoRetryOn400(t *testing.T) {
	calls := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	})
	err := c.Get(context.Background(), "/tasks/1", nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRetryAfterHeaderOn429(t *testing.T) {
	calls := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c.maxRetries = 1
	start := time.Now()
	err := c.Get(context.Background(), "/tasks/1", nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("did not honor Retry-After, elapsed = %v", elapsed)
	}
}

func TestReadableErrorWithCode(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"code": 1005, "message": "The user does not exist"})
	})
	err := c.Get(context.Background(), "/projects/1/users", nil, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != 1005 {
		t.Errorf("code = %d, want 1005", apiErr.Code)
	}
	if apiErr.Error() != "1005: The user does not exist" {
		t.Errorf("Error() = %q, want %q", apiErr.Error(), "1005: The user does not exist")
	}
}

func TestReadableErrorRawBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("no such task"))
	})
	err := c.Get(context.Background(), "/tasks/99", nil, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T", err)
	}
	if apiErr.Error() != "vikunja api error 404: no such task" {
		t.Errorf("Error() = %q", apiErr.Error())
	}
}

func TestPaginationAggregates(t *testing.T) {
	pages := map[int][]map[string]int{
		1: {{"id": 1}, {"id": 2}},
		2: {{"id": 3}},
	}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = parseInt(p)
		}
		if page > 2 {
			w.Header().Set("x-pagination-total-pages", "2")
			w.Write([]byte("[]"))
			return
		}
		w.Header().Set("x-pagination-total-pages", "2")
		json.NewEncoder(w).Encode(pages[page])
	})
	var out []struct {
		ID int `json:"id"`
	}
	if err := c.List(context.Background(), "/tasks/all", ListOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Errorf("got %d items, want 3", len(out))
	}
}

func TestPaginationRespectsLimit(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-pagination-total-pages", "10")
		json.NewEncoder(w).Encode([]map[string]int{{"id": 1}, {"id": 2}, {"id": 3}, {"id": 4}, {"id": 5}})
	})
	var out []struct {
		ID int `json:"id"`
	}
	if err := c.List(context.Background(), "/tasks/all", ListOptions{Limit: 7}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 7 {
		t.Errorf("got %d items, want 7", len(out))
	}
}

func TestContextCancel(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Get(ctx, "/tasks/1", nil, nil); err == nil {
		t.Fatal("want context error")
	}
}

func TestPostSendsJSONBody(t *testing.T) {
	var body map[string]any
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	})
	if err := c.Post(context.Background(), "/projects", map[string]string{"title": "test"}, nil); err != nil {
		t.Fatal(err)
	}
	if body["title"] != "test" {
		t.Errorf("body = %v", body)
	}
}

func parseInt(s string) (int, error) {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errBadInt
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}

var errBadInt = &intError{}

type intError struct{}

func (*intError) Error() string { return "bad int" }

func TestRateLimitSpacing(t *testing.T) {
	var count int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&count, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", 0).SetRateLimit(20)
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := c.Get(context.Background(), "/ping", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("4 requests at 20 rps in %v, expected >= 150ms spacing", elapsed)
	}
	if n := atomic.LoadInt64(&count); n != 4 {
		t.Errorf("requests = %d, want 4", n)
	}
}

func TestRateLimitDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", 0).SetRateLimit(0)
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := c.Get(context.Background(), "/ping", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("unlimited client should not wait")
	}
}

func TestV2PrefixAndEnvelope(t *testing.T) {
	var gotQuery, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]int{{"id": 1}}, "total": 1,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "t", 0)
	var out []struct {
		ID int `json:"id"`
	}
	if err := c.List(context.Background(), "/tasks", ListOptions{PerPage: 7}, &out); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v2/tasks" || !strings.Contains(gotQuery, "per_page=7") {
		t.Errorf("path = %s, query = %s, want /api/v2/tasks with per_page=7", gotPath, gotQuery)
	}
	if len(out) != 1 || out[0].ID != 1 {
		t.Errorf("envelope items not decoded: %+v", out)
	}
}

func TestV1RollbackFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks" {
			t.Errorf("path = %s, want /api/v1/tasks", r.URL.Path)
		}
		if r.URL.Query().Get("page_size") != "3" {
			t.Errorf("query = %s, want page_size=3", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode([]map[string]int{{"id": 1}})
	}))
	defer srv.Close()

	c := NewWithAPI(srv.URL, "t", 0, "v1")
	var out []struct {
		ID int `json:"id"`
	}
	if err := c.List(context.Background(), "/tasks", ListOptions{PerPage: 3, Page: 1}, &out); err != nil {
		t.Fatal(err)
	}
	if c.APIVersion() != "v1" {
		t.Errorf("api version = %s, want v1", c.APIVersion())
	}
}
