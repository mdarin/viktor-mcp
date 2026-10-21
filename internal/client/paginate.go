package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

type ListOptions struct {
	Page    int
	PerPage int
	Limit   int
	Extra   map[string]string
}

func (o ListOptions) query(api string) map[string]string {
	size := "page_size"
	if api == "v2" {
		size = "per_page"
	}
	q := map[string]string{}
	if o.Page > 0 {
		q["page"] = strconv.Itoa(o.Page)
	}
	if o.PerPage > 0 {
		q[size] = strconv.Itoa(o.PerPage)
	}
	for k, v := range o.Extra {
		if v != "" {
			q[k] = v
		}
	}
	return q
}

// pageItems fetches one page and appends its items. Vikunja returns a
// bare JSON array on v1 and a Paginated envelope {items,total,...} on
// v2; the shape is auto-detected so the client tolerates both. The
// second return value is the envelope total (0 for bare arrays).
func (c *Client) pageItems(ctx context.Context, path string, query map[string]string, all *[]json.RawMessage) (int, int, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, path, query, &raw); err != nil {
		return 0, 0, err
	}
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var page []json.RawMessage
		if err := json.Unmarshal(trimmed, &page); err != nil {
			return 0, 0, err
		}
		*all = append(*all, page...)
		return len(page), 0, nil
	}
	var envelope struct {
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return 0, 0, err
	}
	*all = append(*all, envelope.Items...)
	return len(envelope.Items), envelope.Total, nil
}

func (c *Client) List(ctx context.Context, path string, opts ListOptions, out any) error {
	if opts.Limit <= 0 {
		opts.Limit = 1 << 20
	}
	var all []json.RawMessage
	for pageNum := 1; len(all) < opts.Limit; pageNum++ {
		if pageNum > 1000 {
			return fmt.Errorf("list %s: pagination did not terminate after 1000 pages", path)
		}
		o := opts
		o.Page = pageNum
		n, total, err := c.pageItems(ctx, path, o.query(c.api), &all)
		if err != nil {
			return err
		}
		if len(all) > opts.Limit {
			all = all[:opts.Limit]
		}
		if n == 0 || opts.Page > 0 || len(all) >= opts.Limit {
			break
		}
		if total > 0 && len(all) >= total {
			break
		}
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
