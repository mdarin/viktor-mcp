package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VectorStore is the subset of Qdrant the indexer needs.
type VectorStore interface {
	CollectionExists(ctx context.Context, name string) (bool, error)
	// CollectionDim returns the vector size of an existing collection;
	// found=false when the collection does not exist (card #822).
	CollectionDim(ctx context.Context, name string) (int, bool, error)
	CreateCollection(ctx context.Context, name string, dim int) error
	Upsert(ctx context.Context, name string, points []Point) error
	DeletePoints(ctx context.Context, name string, ids []uint64) error
	Count(ctx context.Context, name string) (int64, error)
}

// Point is one Qdrant point: deterministic ID, embedding, payload.
type Point struct {
	ID      uint64
	Vector  []float32
	Payload map[string]any
}

// QdrantClient is a thin client for the Qdrant REST API.
type QdrantClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewQdrant creates a Qdrant client. apiKey may be empty for unsecured
// instances; when set it is sent as the `api-key` header (Qdrant cloud /
// Helm chart service_api_key).
func NewQdrant(baseURL, apiKey string) *QdrantClient {
	return &QdrantClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

type collectionInfo struct {
	Result struct {
		Status      string `json:"status"`
		PointsCount int64  `json:"points_count"`
		Config      struct {
			Params struct {
				Vectors struct {
					Size int `json:"size"`
				} `json:"vectors"`
			} `json:"params"`
		} `json:"config"`
	} `json:"result"`
}

func (c *QdrantClient) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("memory: qdrant %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("memory: qdrant %s: status %d: %s", path, resp.StatusCode, truncateBody(data))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// CollectionExists reports whether the collection exists.
func (c *QdrantClient) CollectionExists(ctx context.Context, name string) (bool, error) {
	var info collectionInfo
	if err := c.do(ctx, http.MethodGet, "/collections/"+name, nil, &info); err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return false, nil
		}
		return false, err
	}
	return info.Result.Status != "", nil
}

// CreateCollection creates a collection with the given vector size (Cosine).
func (c *QdrantClient) CreateCollection(ctx context.Context, name string, dim int) error {
	body := map[string]any{
		"vectors": map[string]any{"size": dim, "distance": "Cosine"},
	}
	return c.do(ctx, http.MethodPut, "/collections/"+name, body, nil)
}

// CollectionDim returns the configured vector size of an existing collection.
func (c *QdrantClient) CollectionDim(ctx context.Context, name string) (int, bool, error) {
	var info collectionInfo
	if err := c.do(ctx, http.MethodGet, "/collections/"+name, nil, &info); err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return 0, false, nil
		}
		return 0, false, err
	}
	return info.Result.Config.Params.Vectors.Size, true, nil
}

type upsertBody struct {
	Points []Point `json:"points"`
}

func (p Point) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"id":      p.ID,
		"vector":  p.Vector,
		"payload": p.Payload,
	})
}

// Upsert writes points (wait=true so the sync report reflects durable state).
func (c *QdrantClient) Upsert(ctx context.Context, name string, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	return c.do(ctx, http.MethodPut, "/collections/"+name+"/points?wait=true", upsertBody{Points: points}, nil)
}

type deleteBody struct {
	Points []uint64 `json:"points"`
}

// DeletePoints removes points by ID.
func (c *QdrantClient) DeletePoints(ctx context.Context, name string, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	return c.do(ctx, http.MethodPost, "/collections/"+name+"/points/delete", deleteBody{Points: ids}, nil)
}

// Count returns the number of points in the collection.
func (c *QdrantClient) Count(ctx context.Context, name string) (int64, error) {
	var info collectionInfo
	if err := c.do(ctx, http.MethodGet, "/collections/"+name, nil, &info); err != nil {
		return 0, err
	}
	return info.Result.PointsCount, nil
}

// SearchHit is one result of a vector search.
type SearchHit struct {
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

// SearchFilter is a Qdrant filter over point payloads (exported for
// reuse by the semantic verifier).
type SearchFilter struct {
	Must []map[string]any `json:"must,omitempty"`
}

type searchBody struct {
	Vector      []float32     `json:"vector"`
	Limit       int           `json:"limit"`
	WithPayload bool          `json:"with_payload"`
	Filter      *SearchFilter `json:"filter,omitempty"`
}

type searchResult struct {
	Result []SearchHit `json:"result"`
}

// SearchPoints runs a kNN search with an optional payload filter.
func (c *QdrantClient) SearchPoints(ctx context.Context, name string, vector []float32, k int, filter *SearchFilter) ([]SearchHit, error) {
	body := searchBody{Vector: vector, Limit: k, WithPayload: true, Filter: filter}
	var out searchResult
	if err := c.do(ctx, http.MethodPost, "/collections/"+name+"/points/search", body, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}
