package tools

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/memory"
	"viktor/internal/shadow"
	"viktor/internal/verifier"
)

type VerifyReviewIn struct {
	TaskID   int    `json:"task_id"`
	Repo     string `json:"repo,omitempty" jsonschema:"path to the git repo with the submitted work (default: working directory)"`
	Instance string `json:"instance,omitempty"`
}

// handleVerifyReview runs the Semantic Verifier on demand: task anchors
// in Qdrant vs the git diff of the submitted work (card #57).
func handleVerifyReview(r *Registry) mcp.ToolHandlerFor[VerifyReviewIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in VerifyReviewIn) (*mcp.CallToolResult, any, error) {
		v, err := r.verifierFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		repo := in.Repo
		if repo == "" {
			repo, _ = os.Getwd()
		}
		verdict, err := v.VerifyRepo(ctx, in.TaskID, repo)
		if err != nil {
			return nil, nil, err
		}
		return nil, verdict, nil
	}
}

// verifierFor builds a verifier for an instance from its memory config.
// No memory section → explicit "not configured" (same contract as the
// memory tools, ADR 0003).
func (r *Registry) verifierFor(instance string) (*verifier.Verifier, error) {
	if instance == "" {
		instance = ""
	}
	cfg, ok := r.memoryFor(instance)
	if !ok {
		return nil, fmt.Errorf("verifier: no memory section for instance %q; add a \"memory\" section to the config (ADR 0003)", orDefault(instance, r.active))
	}
	api, err := r.clientFor(instance)
	if err != nil {
		return nil, err
	}
	sh := r.shadowStore
	if sh == nil {
		sh, err = shadow.NewStore("", orDefault(instance, r.active))
		if err != nil {
			return nil, fmt.Errorf("verifier: shadow store: %w", err)
		}
	}
	vcfg := verifier.Config{
		Instance:     orDefault(instance, r.active),
		QdrantURL:    cfg.QdrantURL,
		OllamaURL:    cfg.OllamaURL,
		EmbedModel:   cfg.EmbedModel,
		Collection:   cfg.Collection,
		QdrantAPIKey: cfg.QdrantAPIKey,
	}.WithDefaults()
	return verifier.New(vcfg, api, memory.NewOllama(vcfg.OllamaURL), memory.NewQdrant(vcfg.QdrantURL, vcfg.QdrantAPIKey), verifier.GitDiff, sh), nil
}

// verifyAsync is the background hook fired by submit_for_review: it
// verifies the submitted diff against the task anchors and queues a
// Shadow Task on drift. Every failure degrades to a log line — the
// review flow must never block on the verifier.
func (r *Registry) verifyAsync(instance string, taskID int, repo string) {
	if _, ok := r.memoryFor(instance); !ok {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		v, err := r.verifierFor(instance)
		if err != nil {
			r.logInfo("verifier: skipped", "task_id", taskID, "reason", err.Error())
			return
		}
		verdict, err := v.VerifyRepo(ctx, taskID, repo)
		if err != nil {
			r.logInfo("verifier: skipped", "task_id", taskID, "reason", err.Error())
			return
		}
		if verdict.Drift {
			r.logInfo("verifier: semantic drift detected — shadow correction task queued",
				"task_id", taskID, "semantic", verdict.Semantic, "exact", verdict.Exact, "shadow_id", verdict.ShadowID)
			return
		}
		r.logInfo("verifier: submitted diff is on-topic", "task_id", taskID, "semantic", verdict.Semantic, "exact", verdict.Exact)
	}()
}

func registerVerifier(r *Registry) {
	Add(r, "verify_review", "Verify the submitted git diff against the task's semantic anchors in Qdrant (hybrid: similarity + exact matches); drift queues a Shadow Task", handleVerifyReview(r))
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
