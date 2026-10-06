package client

import (
	"context"
	"encoding/json"
	"strconv"
)

type ListOptions struct {
	Page    int
	PerPage int
	Limit   int
	Extra   map[string]string
}

func (o ListOptions) query() map[string]string {
	q := map[string]string{}
	if o.Page > 0 {
		q["page"] = strconv.Itoa(o.Page)
	}
	if o.PerPage > 0 {
		q["page_size"] = strconv.Itoa(o.PerPage)
	}
	for k, v := range o.Extra {
		if v != "" {
			q[k] = v
		}
	}
	return q
}

func (c *Client) List(ctx context.Context, path string, opts ListOptions, out any) error {
	if opts.Limit <= 0 {
		opts.Limit = 1 << 20
	}
	var all []json.RawMessage
	for pageNum := 1; len(all) < opts.Limit; pageNum++ {
		o := opts
		o.Page = pageNum
		var page []json.RawMessage
		if err := c.Get(ctx, path, o.query(), &page); err != nil {
			return err
		}
		all = append(all, page...)
		if len(all) > opts.Limit {
			all = all[:opts.Limit]
		}
		if len(page) == 0 || opts.Page > 0 || len(all) >= opts.Limit {
			break
		}
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
