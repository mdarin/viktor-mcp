package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	httpurl "net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	Version           = "0.8.1"
	defaultUserAgent  = "viktor/" + Version
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 2
	defaultBackoff    = 500 * time.Millisecond
)

type Client struct {
	baseURL    string
	token      string
	http       *http.Client
	maxRetries int
	backoff    time.Duration
	limiter    *rate.Limiter
	// api is the URL prefix version: "v2" (default, upstream target —
	// v1 is frozen) or "v1" (config rollback flag, card #59).
	api string
}

type APIError struct {
	StatusCode int
	Code       int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = strings.TrimSpace(e.Body)
	}
	if e.Code != 0 {
		return fmt.Sprintf("%d: %s", e.Code, msg)
	}
	return fmt.Sprintf("vikunja api error %d: %s", e.StatusCode, msg)
}

// New builds a client against the instance base URL. The default API
// prefix is /api/v2 (Vikunja ≥ 2.4 dual-serves CRUD routes under both
// prefixes); use NewWithAPI("v1") for legacy instances.
func New(baseURL, token string, timeout time.Duration) *Client {
	return NewWithAPI(baseURL, token, timeout, "v2")
}

func NewWithAPI(baseURL, token string, timeout time.Duration, apiVersion string) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if apiVersion != "v1" {
		apiVersion = "v2"
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/") + "/api/" + apiVersion,
		token:      token,
		maxRetries: defaultMaxRetries,
		backoff:    defaultBackoff,
		http:       &http.Client{Timeout: timeout},
		api:        apiVersion,
	}
}

// APIVersion returns the URL prefix version this client talks to.
func (c *Client) APIVersion() string { return c.api }

// createVerb picks the create-method verb per API version: Vikunja v1
// creates with PUT, v2 with POST (verified live on 2.7.1, card #56).
func (c *Client) createVerb(ctx context.Context, path string, body, out any) error {
	if c.api == "v1" {
		return c.Put(ctx, path, body, out)
	}
	return c.Post(ctx, path, body, out)
}

// updateVerb picks the update-method verb per API version: v1 updates
// with POST, v2 with PUT.
func (c *Client) updateVerb(ctx context.Context, path string, body, out any) error {
	if c.api == "v1" {
		return c.Post(ctx, path, body, out)
	}
	return c.Put(ctx, path, body, out)
}

func (c *Client) SetRateLimit(rps float64) *Client {
	if rps <= 0 {
		c.limiter = nil
		return c
	}
	c.limiter = rate.NewLimiter(rate.Limit(rps), 1)
	return c
}

func (c *Client) URL(path string, query map[string]string) string {
	if len(query) == 0 {
		return c.baseURL + path
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	q := c.baseURL + path
	for i, k := range keys {
		if i == 0 {
			q += "?"
		} else {
			q += "&"
		}
		q += httpurl.QueryEscape(k) + "=" + httpurl.QueryEscape(query[k])
	}
	return q
}

type request struct {
	method string
	path   string
	query  map[string]string
	body   any
	out    any
}

func (c *Client) Do(ctx context.Context, r request) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * c.backoff
			if rErr, ok := lastErr.(*retryAfter); ok {
				wait = rErr.wait
			}
			if err := sleep(ctx, wait); err != nil {
				return err
			}
		}
		err := c.doOnce(ctx, r)
		if err == nil {
			return nil
		}
		var rErr *retryAfter
		if errors.As(err, &rErr) {
			lastErr = err
			continue
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			return classifyUnreachable(ctx, err)
		}
		if apiErr.StatusCode != http.StatusTooManyRequests && apiErr.StatusCode < 500 {
			return err
		}
		lastErr = err
	}
	return lastErr
}

type retryAfter struct {
	wait time.Duration
}

func (e *retryAfter) Error() string {
	return "retry after " + e.wait.String()
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) doOnce(ctx context.Context, r request) error {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
	}
	var body io.Reader
	if r.body != nil {
		data, err := json.Marshal(r.body)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, r.method, c.URL(r.path, r.query), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode, Body: string(data)}
		parseAPIError(apiErr)
		if apiErr.StatusCode == http.StatusTooManyRequests {
			if wait := parseRetryAfter(resp.Header.Get("Retry-After")); wait > 0 {
				return &retryAfter{wait: wait}
			}
		}
		return apiErr
	}
	if r.out != nil && len(data) > 0 {
		return json.Unmarshal(data, r.out)
	}
	return nil
}

func parseAPIError(e *APIError) {
	var v struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(e.Body), &v); err == nil {
		e.Code = v.Code
		e.Message = strings.TrimSpace(v.Message)
	}
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		return time.Until(t)
	}
	return 0
}

func (c *Client) Get(ctx context.Context, path string, query map[string]string, out any) error {
	return c.Do(ctx, request{method: http.MethodGet, path: path, query: query, out: out})
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, request{method: http.MethodPost, path: path, body: body, out: out})
}

func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, request{method: http.MethodPut, path: path, body: body, out: out})
}

func (c *Client) Delete(ctx context.Context, path string) error {
	return c.Do(ctx, request{method: http.MethodDelete, path: path})
}

func (c *Client) BaseURL() string {
	return c.baseURL
}
