package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"viktor/internal/client"
	"viktor/internal/config"
	"viktor/internal/memory"
	"viktor/internal/server"
	"viktor/internal/shadow"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "memory":
			if err := runMemory(ctx, os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "viktor memory:", err)
				os.Exit(1)
			}
			return
		case "version", "--version":
			fmt.Println("viktor", client.Version)
			return
		}
	}

	if err := server.Run(ctx, os.Stderr); err != nil {
		os.Exit(1)
	}
}

// runMemory implements `viktor memory sync` — the CLI entry point of the
// semantic memory indexer (ADR 0003). Settings come from flags with env
// fallbacks (.env in the working directory is loaded first, tcu-book
// convention); the config file section lands with the memory tools (M3).
func runMemory(ctx context.Context, args []string) error {
	loadDotenv(".env")
	fs := flag.NewFlagSet("memory", flag.ContinueOnError)
	sync := fs.Bool("sync", false, "run one sync pass (primary operation)")
	instance := fs.String("instance", "", "tracker instance name (default: config default)")
	qdrant := fs.String("qdrant", envOr("VIKTOR_MEMORY_QDRANT_URL", "http://localhost:6333"), "Qdrant base URL")
	ollama := fs.String("ollama", envOr("VIKTOR_MEMORY_OLLAMA_URL", "http://localhost:11434"), "Ollama base URL")
	model := fs.String("model", envOr("VIKTOR_MEMORY_EMBED_MODEL", "bge-m3"), "embedding model")
	collection := fs.String("collection", envOr("VIKTOR_MEMORY_COLLECTION", "viktor_tasks"), "Qdrant collection")
	repo := fs.String("repo", os.Getenv("VIKTOR_MEMORY_GIT_REPO"), "path to git repo to index (default: current directory; per-instance option)")
	status := fs.Bool("status", false, "print index status and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		switch fs.Arg(0) {
		case "sync":
			*sync = true
		case "status":
			*status = true
		default:
			return fmt.Errorf("unknown memory command %q", fs.Arg(0))
		}
	}
	if !*sync && !*status {
		return fmt.Errorf("usage: viktor memory sync|status [-instance NAME] [-repo PATH]")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	instName := *instance
	if instName == "" {
		instName = cfg.DefaultInstance
	}
	inst, ok := cfg.Instances[instName]
	if !ok {
		return fmt.Errorf("instance %q is not configured", instName)
	}

	memCfg := memory.Config{
		Instance:   instName,
		QdrantURL:  *qdrant,
		OllamaURL:  *ollama,
		EmbedModel: *model,
		Collection: *collection,
		GitRepo:    *repo,
	}
	if memCfg.GitRepo == "" {
		// "Index the current repository": repo history joins the board
		// projection by default.
		if wd, err := os.Getwd(); err == nil {
			memCfg.GitRepo = wd
		}
	}

	api := client.New(inst.URL, inst.Token, inst.Timeout).SetRateLimit(inst.RateLimit)
	store, err := shadow.NewStore("", instName)
	if err != nil {
		return fmt.Errorf("shadow store: %w", err)
	}

	ix, err := memory.NewIndexer(memCfg, memory.NewSource(api, store), memory.NewOllama(memCfg.OllamaURL), memory.NewQdrant(memCfg.QdrantURL), gitEnricher(memCfg), memory.DefaultStatePath(instName))
	if err != nil {
		return err
	}

	if *status {
		st, err := ix.Status(ctx)
		if err != nil {
			return err
		}
		return printJSON(st)
	}

	rep, err := ix.Sync(ctx)
	if err != nil {
		// Connection-level failure with an empty mirror is the one case
		// where neither source can answer.
		if errors.Is(err, client.ErrUnreachable) && strings.Contains(err.Error(), "fetch tasks") {
			return fmt.Errorf("tracker unreachable and shadow mirror has no tasks: run sync while online first")
		}
		return err
	}
	return printJSON(rep)
}

func gitEnricher(cfg memory.Config) memory.GitEnricher {
	if cfg.GitRepo == "" {
		return nil
	}
	return memory.NewGit(cfg.GitRepo)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadDotenv applies KEY=VALUE pairs from a .env file to the environment
// without overriding variables that are already set. Malformed lines are
// skipped; this is a convenience, not a config parser.
func loadDotenv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k == "" || os.Getenv(k) != "" {
			continue
		}
		_ = os.Setenv(k, v)
	}
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
