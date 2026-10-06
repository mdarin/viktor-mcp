package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"viktor/internal/client"
	"viktor/internal/config"
	"viktor/internal/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultToolTimeout = 60 * time.Second

func buildLogger() *slog.Logger {
	level := slog.LevelInfo
	if v := os.Getenv("VIKUNJA_LOG_LEVEL"); v != "" {
		var lv slog.Level
		if err := lv.UnmarshalText([]byte(v)); err == nil {
			level = lv
		}
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func toolTimeout() time.Duration {
	d := defaultToolTimeout
	if v := os.Getenv("VIKUNJA_TOOL_TIMEOUT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			d = time.Duration(parsed) * time.Second
		}
	}
	return d
}

func Build(_ context.Context, stdout io.Writer) (*mcp.Server, *tools.Registry, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}

	clients := make(map[string]*client.Client, len(cfg.Instances))
	var active *client.Client
	for name, inst := range cfg.Instances {
		c := client.New(inst.URL, inst.Token, inst.Timeout).SetRateLimit(inst.RateLimit)
		clients[name] = c
		if name == cfg.DefaultInstance {
			active = c
		}
	}

	impl := &mcp.Implementation{
		Name:    "vikunja-mcp",
		Version: client.Version,
	}
	server := mcp.NewServer(impl, nil)

	registry := tools.NewRegistry(server, active)
	registry.SetInstances(clients, cfg.DefaultInstance)
	registry.SetLogger(buildLogger())
	registry.SetToolTimeout(toolTimeout())
	registry.RegisterAll()

	defaultURL := cfg.Instances[cfg.DefaultInstance].URL
	_, _ = fmt.Fprintf(stdout, "viktor %s: connecting to %s (instance %q, %d configured)\n", client.Version, defaultURL, cfg.DefaultInstance, len(cfg.Instances))
	return server, registry, nil
}

func Run(ctx context.Context, stdout io.Writer) error {
	logger := buildLogger()
	server, registry, err := Build(ctx, stdout)
	if err != nil {
		return err
	}
	transport := &mcp.StdioTransport{}
	runErr := server.Run(ctx, transport)
	registry.DumpStats(logger)
	logger.Info("viktor shutdown", slog.Bool("clean", runErr == nil))
	return runErr
}
