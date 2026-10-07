package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	httpurl "net/url"
	"syscall"
	"testing"
)

func unreachableServer(t *testing.T) string {
	t.Helper()
	// Port 1 on localhost: connection refused.
	return "http://127.0.0.1:1"
}

func TestDoMarksConnectionRefusedAsUnreachable(t *testing.T) {
	c := New(unreachableServer(t), "tok", 0)
	err := c.Do(context.Background(), request{method: http.MethodGet, path: "/api/v1/info"})
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
}

func TestDoDoesNotMarkAPIErrorAsUnreachable(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"message":"nope"}`))
	})
	err := c.Do(context.Background(), request{method: http.MethodGet, path: "/api/v1/x"})
	if errors.Is(err, ErrUnreachable) {
		t.Fatal("API error must not be unreachable")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		t.Fatalf("want APIError 404, got %v", err)
	}
}

func TestCallerCancelledContextNotUnreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New("http://127.0.0.1:1", "tok", 0)
	err := c.Do(ctx, request{method: http.MethodGet, path: "/x"})
	if errors.Is(err, ErrUnreachable) {
		t.Fatal("caller-cancelled context must not be unreachable")
	}
}

func TestIsUnreachableVariants(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&httpurl.Error{Op: "Get", URL: "http://x", Err: syscall.ECONNREFUSED}, true},
		{&httpurl.Error{Op: "Get", URL: "http://x", Err: &net.DNSError{Err: "no such host", Name: "x"}}, true},
		{&httpurl.Error{Op: "Get", URL: "http://x", Err: errors.New("unexpected")}, false},
		{errors.New("bad request"), false},
		{&UnreachableError{Err: errors.New("x")}, true},
	}
	for i, c := range cases {
		if got := IsUnreachable(c.err); got != c.want {
			t.Errorf("case %d: IsUnreachable(%v) = %v, want %v", i, c.err, got, c.want)
		}
	}
}
