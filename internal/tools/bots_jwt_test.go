package tools

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func TestBotJWTRequired(t *testing.T) {
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	api := client.New("http://localhost", "tk_live_probe", 0)
	r := NewRegistry(srvMCP, api)
	err := jwtRequired(r)
	if err == nil {
		t.Fatal("API token (tk_) must be rejected with an explanation, not a raw upstream 401")
	}
	if want := "JWT"; !containsStr(err.Error(), want) {
		t.Fatalf("error %q must mention %q", err.Error(), want)
	}
	api2 := client.New("http://localhost", "eyJhbGciOi.payload.sig", 0)
	r2 := NewRegistry(srvMCP, api2)
	if err := jwtRequired(r2); err != nil {
		t.Fatalf("JWT must pass: %v", err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
