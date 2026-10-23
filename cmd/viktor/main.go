package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"log"
	"time"

	"viktor/internal/client"
	"viktor/internal/config"
	"viktor/internal/fsmstate"
	"viktor/internal/memory"
	"viktor/internal/server"
	"viktor/internal/shadow"
	"viktor/internal/tools"
	"viktor/internal/watchdog"
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
		case "watchdog":
			if err := runWatchdog(ctx, os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "viktor watchdog:", err)
				os.Exit(1)
			}
			return
		case "version", "--version":
			fmt.Println("viktor", client.Version)
			return
		}
	}

	// The MCP serve path reads VIKTOR_* / VIKUNJA_* from .env too
	// (bot identity, card #616); CLI subcommands load it themselves.
	loadDotenv(".env")
	if err := server.Run(ctx, os.Stderr); err != nil {
		// Fail fast with the reason on stderr (card #839): an
		// exit code alone made config errors look like silent
		// tool outages to the MCP host.
		fmt.Fprintln(os.Stderr, "viktor:", err)
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
	full := fs.Bool("full", false, "force a full rescan (also cleans ghost points)")
	instance := fs.String("instance", "", "tracker instance name (default: config default)")
	qdrant := fs.String("qdrant", envOr("VIKTOR_MEMORY_QDRANT_URL", "http://localhost:6333"), "Qdrant base URL")
	ollama := fs.String("ollama", envOr("VIKTOR_MEMORY_OLLAMA_URL", "http://localhost:11434"), "Ollama base URL")
	model := fs.String("model", envOr("VIKTOR_MEMORY_EMBED_MODEL", "bge-m3"), "embedding model")
	collection := fs.String("collection", envOr("VIKTOR_MEMORY_COLLECTION", "viktor_tasks"), "Qdrant collection")
	repo := fs.String("repo", os.Getenv("VIKTOR_MEMORY_GIT_REPO"), "path to git repo to index (default: current directory; per-instance option)")
	status := fs.Bool("status", false, "print index status and exit")
	doctor := fs.Bool("doctor", false, "sweep the collection for orphan points and print a report (card #837)")
	prune := fs.Bool("prune", false, "with -doctor: delete the orphan points found (default: dry run)")
	watch := fs.Bool("watch", false, "keep running: a full rescan (ghost cleanup) every -interval")
	interval := fs.Duration("interval", 24*time.Hour, "with -watch: time between full rescans")
	// Strip a leading subcommand so flags after it still parse:
	// `viktor memory sync -full` (Go's flag stops at the first
	// positional argument).
	if len(args) > 0 && (args[0] == "sync" || args[0] == "status" || args[0] == "doctor" || args[0] == "watch") {
		switch args[0] {
		case "sync":
			*sync = true
		case "status":
			*status = true
		case "doctor":
			*doctor = true
		case "watch":
			*watch = true
		}
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown memory command %q", fs.Arg(0))
	}
	if !*sync && !*status && !*doctor && !*watch {
		return fmt.Errorf("usage: viktor memory sync|status|doctor|watch [-full] [-prune] [-interval DUR] [-instance NAME] [-repo PATH]")
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

	// Precedence: explicit flag > VIKTOR_MEMORY_* env > config "memory"
	// section > built-in defaults. git_enrich in the section is the only
	// way to point a non-default instance at its repo.
	explicit := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "qdrant":
			explicit["qdrant"] = *qdrant
		case "ollama":
			explicit["ollama"] = *ollama
		case "model":
			explicit["model"] = *model
		case "collection":
			explicit["collection"] = *collection
		case "repo":
			explicit["repo"] = *repo
		}
	})
	var memSection *config.MemorySection
	if cfg.Memory != nil {
		memSection = cfg.Memory
	}
	pick := func(flagName, envKey, cfgVal string) string {
		if v, ok := explicit[flagName]; ok {
			return v
		}
		if v := os.Getenv(envKey); v != "" {
			return v
		}
		return cfgVal
	}
	memCfg := memory.Config{
		Instance:       instName,
		QdrantURL:      pick("qdrant", "VIKTOR_MEMORY_QDRANT_URL", memSection.Qdrant()),
		OllamaURL:      pick("ollama", "VIKTOR_MEMORY_OLLAMA_URL", memSection.Ollama()),
		EmbedModel:     pick("model", "VIKTOR_MEMORY_EMBED_MODEL", memSection.Model()),
		Collection:     pick("collection", "VIKTOR_MEMORY_COLLECTION", memSection.Coll()),
		QdrantAPIKey:   pick("apikey", "VIKTOR_MEMORY_QDRANT_API_KEY", memSection.QdrantKey()),
		GitRepo:        pick("repo", "VIKTOR_MEMORY_GIT_REPO", memSection.RepoFor(instName)),
		FullSyncMaxAge: time.Duration(memSection.FullSyncHoursOrDefault()) * time.Hour,
		FilePatterns:   memSection.FilePatterns,
		HybridPolicy:   memSection.HybridPolicy,
	}
	if memCfg.GitRepo == "" {
		// "Index the current repository": repo history joins the board
		// projection by default.
		if wd, err := os.Getwd(); err == nil {
			memCfg.GitRepo = wd
		}
	}
	memCfg = memCfg.WithDefaults()

	// Card #47 contract: no memory section and no env/flag overrides →
	// a clear error with a hint, not a silent localhost run.
	if cfg.Memory == nil && len(explicit) == 0 && !hasMemoryEnv() {
		return fmt.Errorf("memory is not configured: add a \"memory\" section to %s (see .env.example) or set VIKTOR_MEMORY_* env / pass -qdrant/-ollama flags", config.DefaultPath())
	}

	api := client.NewWithAPI(inst.URL, inst.TokenFor(cfg.Agent), inst.Timeout, inst.API).SetRateLimit(inst.RateLimit)
	store, err := shadow.NewStore("", instName)
	if err != nil {
		return fmt.Errorf("shadow store: %w", err)
	}

	ix, err := memory.NewIndexer(memCfg, memory.NewSource(api, store), memory.NewOllama(memCfg.OllamaURL), memory.NewQdrant(memCfg.QdrantURL, memCfg.QdrantAPIKey), gitEnricher(memCfg), memory.DefaultStatePath(instName))
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

	if *doctor {
		rep, err := ix.Doctor(ctx, *prune)
		if err != nil {
			return err
		}
		return printJSON(rep)
	}

	if *watch {
		// Periodic ghost cleanup (card #836): Vikunja hides soft-deleted
		// tasks from the API, so only a complete rescan can drop their
		// points. Each tick is a full rescan; reports go to stdout.
		for {
			rep, err := ix.SyncFull(ctx)
			if err != nil {
				return err
			}
			if err := printJSON(rep); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(*interval):
			}
		}
	}

	var rep *memory.Report
	if *full {
		rep, err = ix.SyncFull(ctx)
	} else {
		rep, err = ix.Sync(ctx)
	}
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

// runWatchdog implements `viktor watchdog` — the CLI entry point of the
// stalled-task sentinel (card #58). With -once it runs a single scan pass
// and prints the result; otherwise it loops on a ticker until interrupted.
func runWatchdog(ctx context.Context, args []string) error {
	loadDotenv(".env")
	fs := flag.NewFlagSet("watchdog", flag.ContinueOnError)
	once := fs.Bool("once", false, "run one scan pass and exit")
	threshold := fs.Duration("threshold", watchdog.DefaultThreshold, "idle time after which an in_progress task stalls")
	interval := fs.Duration("interval", 15*time.Minute, "scan interval in loop mode")
	instance := fs.String("instance", "", "tracker instance name (default: config default)")
	if err := fs.Parse(args); err != nil {
		return err
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

	api := client.NewWithAPI(inst.URL, inst.TokenFor(cfg.Agent), inst.Timeout, inst.API).SetRateLimit(inst.RateLimit)
	store, err := fsmstate.Open(tools.DefaultFSMPath(instName))
	if err != nil {
		return fmt.Errorf("fsm store: %w", err)
	}
	defer func() { _ = store.Close() }()

	w := watchdog.New(store, instName, api)
	w.Threshold = *threshold

	logger := log.New(os.Stderr, "", log.LstdFlags)
	if *once {
		res, err := w.Scan(ctx)
		if err != nil {
			return err
		}
		return printJSON(res)
	}
	err = watchdog.Loop(ctx, w, *interval, logger)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
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

func hasMemoryEnv() bool {
	for _, e := range os.Environ() {
		if k, _, ok := strings.Cut(e, "="); ok && strings.HasPrefix(k, "VIKTOR_MEMORY_") {
			return true
		}
	}
	return false
}

// loadDotenv applies KEY=VALUE pairs from a .env file to the environment
// without overriding variables that are already set. Malformed lines are
// skipped; this is a convenience, not a config parser.
// envRef matches ${VAR} and ${VAR:-default} references; default may be
// empty (${VAR:-} means "unset VAR becomes empty string").
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnv resolves ${VAR} references: an already-set environment
// variable wins (env before file), otherwise the :- default applies.
func expandEnv(v string) string {
	return envRef.ReplaceAllStringFunc(v, func(m string) string {
		sub := envRef.FindStringSubmatch(m)
		if val := os.Getenv(sub[1]); val != "" {
			return val
		}
		return sub[2]
	})
}

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
		// Shell-style "export K=V" is accepted; the prefix is dropped.
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Unquoted values may carry an inline comment; a double-quoted
		// value keeps everything up to the closing quote and gets ${VAR}
		// expansion; a single-quoted value stays literal (no expansion).
		expand := true
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
			expand = false
		} else if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		} else if strings.HasPrefix(v, "#") {
			continue
		}
		if expand {
			v = expandEnv(v)
		}
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
