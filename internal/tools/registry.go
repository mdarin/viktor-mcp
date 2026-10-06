package tools

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type toolStats struct {
	calls    atomic.Uint64
	failures atomic.Uint64
	nanos    atomic.Int64
}

type StatsRow struct {
	Tool     string
	Calls    uint64
	Failures uint64
	TotalMS  int64
}

type Registry struct {
	server      *mcp.Server
	apiPtr      atomic.Pointer[client.Client]
	names       []string
	mu          sync.RWMutex
	clients     map[string]*client.Client
	active      string
	ctxProj     int
	statsMu     sync.Mutex
	stats       map[string]*toolStats
	logger      *slog.Logger
	toolTimeout time.Duration
}

func NewRegistry(server *mcp.Server, api *client.Client) *Registry {
	r := &Registry{server: server}
	r.apiPtr.Store(api)
	r.clients = map[string]*client.Client{"default": api}
	r.active = "default"
	return r
}

func (r *Registry) SetLogger(l *slog.Logger) {
	r.logger = l
}

func (r *Registry) SetToolTimeout(d time.Duration) {
	r.toolTimeout = d
}

func (r *Registry) statsFor(name string) *toolStats {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	if r.stats == nil {
		r.stats = map[string]*toolStats{}
	}
	st, ok := r.stats[name]
	if !ok {
		st = &toolStats{}
		r.stats[name] = st
	}
	return st
}

func (r *Registry) StatsSnapshot() []StatsRow {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	names := make([]string, 0, len(r.stats))
	for name := range r.stats {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]StatsRow, 0, len(names))
	for _, name := range names {
		st := r.stats[name]
		rows = append(rows, StatsRow{
			Tool:     name,
			Calls:    st.calls.Load(),
			Failures: st.failures.Load(),
			TotalMS:  st.nanos.Load() / int64(time.Millisecond),
		})
	}
	return rows
}

func (r *Registry) DumpStats(l *slog.Logger) {
	if l == nil {
		return
	}
	rows := r.StatsSnapshot()
	for _, row := range rows {
		l.Info("tool stats",
			slog.String("tool", row.Tool),
			slog.Uint64("calls", row.Calls),
			slog.Uint64("failures", row.Failures),
			slog.Int64("total_ms", row.TotalMS),
		)
	}
}

func (r *Registry) SetInstances(clients map[string]*client.Client, active string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients = clients
	r.active = active
	if c, ok := clients[active]; ok {
		r.apiPtr.Store(c)
	}
}

func (r *Registry) api() *client.Client {
	return r.apiPtr.Load()
}

func (r *Registry) clientFor(instance string) (*client.Client, error) {
	if instance == "" {
		return r.api(), nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[instance]
	if !ok {
		return nil, fmt.Errorf("unknown instance %q", instance)
	}
	return c, nil
}

func (r *Registry) allClients() (string, map[string]*client.Client) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*client.Client, len(r.clients))
	for k, v := range r.clients {
		out[k] = v
	}
	return r.active, out
}

func (r *Registry) switchTo(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.clients[name]
	if !ok {
		return fmt.Errorf("unknown instance %q", name)
	}
	r.active = name
	r.apiPtr.Store(c)
	return nil
}

func Add[In, Out any](r *Registry, name, description string, h mcp.ToolHandlerFor[In, Out]) {
	r.names = append(r.names, name)
	mcp.AddTool(r.server, &mcp.Tool{Name: name, Description: description}, instrument(r, name, h))
}

func instrument[In, Out any](r *Registry, name string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		if r.toolTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, r.toolTimeout)
			defer cancel()
		}
		start := time.Now()
		st := r.statsFor(name)
		st.calls.Add(1)
		result, out, err := h(ctx, req, in)
		elapsed := time.Since(start)
		st.nanos.Add(int64(elapsed))
		status := "ok"
		if err != nil || (result != nil && result.IsError) {
			status = "error"
			st.failures.Add(1)
		}
		if r.logger != nil {
			attrs := []slog.Attr{
				slog.String("tool", name),
				slog.Int64("duration_ms", elapsed.Milliseconds()),
				slog.String("status", status),
			}
			if err != nil {
				attrs = append(attrs, slog.String("error", err.Error()))
			}
			r.logger.LogAttrs(ctx, slog.LevelInfo, "tool call", attrs...)
		}
		return result, out, err
	}
}

func (r *Registry) Names() []string {
	return append([]string(nil), r.names...)
}

func (r *Registry) RegisterAll() {
	registerToday(r)
	registerTask(r)
	registerProject(r)
	registerBatch(r)
	registerKanban(r)
	registerView(r)
	registerLabel(r)
	registerComment(r)
	registerSearch(r)
	registerInstance(r)
	registerConfig(r)
	registerCalendar(r)
	registerAssign(r)
	registerTeams(r)
}
