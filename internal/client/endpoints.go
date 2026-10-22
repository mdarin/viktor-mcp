package client

import (
	"context"
	"encoding/json"
	"fmt"
)

type Health struct {
	Version string `json:"version"`
}

func (c *Client) Info(ctx context.Context) (*Health, error) {
	var h Health
	if err := c.Get(ctx, "/info", nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

type Project struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

type ListParams struct {
	Search  string
	Filter  string
	SortBy  string
	OrderBy string
	Page    int
	PerPage int
	Limit   int
}

func (p ListParams) options() ListOptions {
	return ListOptions{
		Search:  p.Search,
		Page:    p.Page,
		PerPage: p.PerPage,
		Limit:   p.Limit,
		Extra: map[string]string{
			"filter":   p.Filter,
			"sort_by":  p.SortBy,
			"order_by": p.OrderBy,
		},
	}
}

func (c *Client) ListProjects(ctx context.Context, p ListParams, out any) error {
	return c.List(ctx, "/projects", p.options(), out)
}

func (c *Client) GetProject(ctx context.Context, projectID int, out any) error {
	return c.Get(ctx, url("/projects/%d", projectID), nil, out)
}

func (c *Client) CreateProject(ctx context.Context, body, out any) error {
	return c.createVerb(ctx, "/projects", body, out)
}

func (c *Client) UpdateProject(ctx context.Context, projectID int, body, out any) error {
	return c.updateVerb(ctx, url("/projects/%d", projectID), body, out)
}

func (c *Client) DeleteProject(ctx context.Context, projectID int) error {
	return c.Delete(ctx, url("/projects/%d", projectID))
}

func (c *Client) ShareProject(ctx context.Context, projectID int, body, out any) error {
	if c.api == "v2" {
		// v2 ProjectUser schema: username + permission only.
		if m, ok := body.(map[string]any); ok {
			body = map[string]any{"username": m["username"], "permission": m["permission"]}
		}
		return c.Post(ctx, url("/projects/%d/users", projectID), body, out)
	}
	return c.createVerb(ctx, url("/projects/%d/users", projectID), body, out)
}

func (c *Client) ProjectUsers(ctx context.Context, projectID int, out any) error {
	return c.List(ctx, url("/projects/%d/users", projectID), ListOptions{PerPage: 50}, out)
}

func (c *Client) ShareProjectTeam(ctx context.Context, projectID int, body, out any) error {
	return c.createVerb(ctx, url("/projects/%d/teams", projectID), body, out)
}

func (c *Client) ProjectTeams(ctx context.Context, projectID int, out any) error {
	return c.List(ctx, url("/projects/%d/teams", projectID), ListOptions{PerPage: 50}, out)
}

func (c *Client) UnshareProjectTeam(ctx context.Context, projectID, teamID int) error {
	return c.Delete(ctx, url("/projects/%d/teams/%d", projectID, teamID))
}

func (c *Client) ListTasks(ctx context.Context, p ListParams, out any) error {
	return c.List(ctx, "/tasks", p.options(), out)
}

func (c *Client) ProjectTasks(ctx context.Context, projectID int, p ListParams, out any) error {
	return c.List(ctx, url("/projects/%d/tasks", projectID), p.options(), out)
}

func (c *Client) GetTask(ctx context.Context, taskID int, out any) error {
	return c.Get(ctx, url("/tasks/%d", taskID), nil, out)
}

func (c *Client) CreateTask(ctx context.Context, projectID int, body, out any) error {
	return c.createVerb(ctx, url("/projects/%d/tasks", projectID), body, out)
}

func (c *Client) UpdateTask(ctx context.Context, taskID int, body, out any) error {
	return c.updateVerb(ctx, url("/tasks/%d", taskID), body, out)
}

func (c *Client) DeleteTask(ctx context.Context, taskID int) error {
	return c.Delete(ctx, url("/tasks/%d", taskID))
}

func (c *Client) MoveTaskToProject(ctx context.Context, taskID, targetProjectID int, out any) error {
	var current map[string]any
	if err := c.GetTask(ctx, taskID, &current); err != nil {
		return fmt.Errorf("get task for move: %w", err)
	}
	current["project_id"] = targetProjectID
	return c.UpdateTask(ctx, taskID, current, out)
}

func (c *Client) SetTaskPosition(ctx context.Context, taskID int, body, out any) error {
	if c.api != "v1" {
		if m, ok := body.(map[string]any); ok {
			// v2: bucket assignment is a dedicated route; position is a
			// per-view sort value without bucket_id in the body.
			if bid, hasBucket := num(m["bucket_id"]); hasBucket && bid > 0 {
				pid, _ := num(m["project_id"])
				vid, _ := num(m["project_view_id"])
				// v2 TaskBucket accepts task_id only; in-bucket ordering
				// goes through the position route separately.
				return c.Put(ctx, url("/projects/%d/views/%d/buckets/%d/tasks", int(pid), int(vid), int(bid)), map[string]any{"task_id": taskID}, out)
			}
			payload := map[string]any{}
			if vid, ok := num(m["project_view_id"]); ok {
				payload["project_view_id"] = vid
			}
			if pos, ok := num(m["position"]); ok {
				payload["position"] = pos
			}
			return c.Put(ctx, url("/tasks/%d/position", taskID), payload, out)
		}
	}
	return c.updateVerb(ctx, url("/tasks/%d/position", taskID), body, out)
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func (c *Client) SetReminders(ctx context.Context, taskID int, reminders []string, out any) error {
	var current map[string]any
	if err := c.GetTask(ctx, taskID, &current); err != nil {
		return fmt.Errorf("get task for reminders: %w", err)
	}
	objs := make([]map[string]any, 0, len(reminders))
	for _, r := range reminders {
		objs = append(objs, map[string]any{"reminder": r})
	}
	current["reminders"] = objs
	return c.UpdateTask(ctx, taskID, current, out)
}

func (c *Client) AddTaskLabel(ctx context.Context, taskID, labelID int, out any) error {
	return c.createVerb(ctx, url("/tasks/%d/labels", taskID), map[string]int{"label_id": labelID}, out)
}

func (c *Client) RemoveTaskLabel(ctx context.Context, taskID, labelID int) error {
	return c.Delete(ctx, url("/tasks/%d/labels/%d", taskID, labelID))
}

func (c *Client) CreateTaskRelation(ctx context.Context, taskID int, kind string, otherTaskID int, out any) error {
	return c.createVerb(ctx, url("/tasks/%d/relations", taskID), map[string]any{
		"other_task_id": otherTaskID,
		"relation_kind": kind,
	}, out)
}

func (c *Client) AssignUser(ctx context.Context, taskID, userID int, out any) error {
	return c.createVerb(ctx, url("/tasks/%d/assignees", taskID), map[string]int{"user_id": userID}, out)
}

func (c *Client) UnassignUser(ctx context.Context, taskID, userID int) error {
	return c.Delete(ctx, url("/tasks/%d/assignees/%d", taskID, userID))
}

func (c *Client) CreateRelation(ctx context.Context, taskID int, kind string, body, out any) error {
	if c.api == "v1" {
		return c.Put(ctx, url("/tasks/%d/relations/%s", taskID, kind), body, out)
	}
	// v2 has no per-kind path: relation kind travels in the body.
	payload := map[string]any{"relation_kind": kind}
	if m, ok := body.(map[string]any); ok {
		for k, v := range m {
			payload[k] = v
		}
	}
	return c.Post(ctx, url("/tasks/%d/relations", taskID), payload, out)
}

func (c *Client) ListRelations(ctx context.Context, taskID int, out any) error {
	return c.Get(ctx, url("/tasks/%d/relations", taskID), nil, out)
}

type View struct {
	ID   int    `json:"id"`
	Kind string `json:"view_kind"`
}

func (c *Client) ListViews(ctx context.Context, projectID int, out any) error {
	return c.List(ctx, url("/projects/%d/views", projectID), ListOptions{}, out)
}

func (c *Client) CreateView(ctx context.Context, projectID int, body, out any) error {
	return c.createVerb(ctx, url("/projects/%d/views", projectID), body, out)
}

func (c *Client) UpdateView(ctx context.Context, projectID, viewID int, body, out any) error {
	return c.updateVerb(ctx, url("/projects/%d/views/%d", projectID, viewID), body, out)
}

func (c *Client) DeleteView(ctx context.Context, projectID, viewID int) error {
	return c.Delete(ctx, url("/projects/%d/views/%d", projectID, viewID))
}

func (c *Client) ViewTasks(ctx context.Context, projectID, viewID int, out any) error {
	if c.api == "v1" {
		// v1 returns buckets with nested tasks.
		return c.Get(ctx, url("/projects/%d/views/%d/tasks", projectID, viewID), nil, out)
	}
	// v2 returns a flat paginated task list: reassemble the v1-style
	// bucket shape (id/title/position/tasks) for callers.
	var buckets []map[string]any
	if err := c.List(ctx, url("/projects/%d/views/%d/buckets", projectID, viewID), ListOptions{}, &buckets); err != nil {
		return err
	}
	var tasks []map[string]any
	if err := c.List(ctx, url("/projects/%d/views/%d/tasks", projectID, viewID), ListOptions{}, &tasks); err != nil {
		return err
	}
	byID := map[float64]map[string]any{}
	for _, b := range buckets {
		id, _ := b["id"].(float64)
		b["tasks"] = []any{}
		byID[id] = b
	}
	for _, t := range tasks {
		bid, _ := t["bucket_id"].(float64)
		var b map[string]any
		if got, ok := byID[bid]; ok {
			b = got
		} else if len(buckets) > 0 {
			// v2.6 does not auto-assign a default bucket on task create;
			// bucketless tasks surface in the first bucket (UI behavior).
			b = buckets[0]
		} else {
			continue
		}
		b["tasks"] = append(b["tasks"].([]any), t)
	}
	raw, err := json.Marshal(buckets)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) ListBuckets(ctx context.Context, projectID, viewID int, out any) error {
	return c.List(ctx, url("/projects/%d/views/%d/buckets", projectID, viewID), ListOptions{}, out)
}

func (c *Client) CreateBucket(ctx context.Context, projectID, viewID int, body, out any) error {
	return c.createVerb(ctx, url("/projects/%d/views/%d/buckets", projectID, viewID), body, out)
}

func (c *Client) DeleteBucket(ctx context.Context, projectID, viewID, bucketID int) error {
	return c.Delete(ctx, url("/projects/%d/views/%d/buckets/%d", projectID, viewID, bucketID))
}

func (c *Client) BucketTasks(ctx context.Context, projectID, viewID int, out any) error {
	return c.List(ctx, url("/projects/%d/views/%d/tasks", projectID, viewID), ListOptions{}, out)
}

type Label struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

func (c *Client) ListLabels(ctx context.Context, p ListParams, out any) error {
	return c.List(ctx, "/labels", p.options(), out)
}

func (c *Client) CreateLabel(ctx context.Context, body, out any) error {
	return c.createVerb(ctx, "/labels", body, out)
}

func (c *Client) DeleteLabel(ctx context.Context, labelID int) error {
	return c.Delete(ctx, url("/labels/%d", labelID))
}

type Comment struct {
	ID      int    `json:"id"`
	Text    string `json:"comment"`
	TaskID  int    `json:"task_id"`
	Created string `json:"created"`
	Updated string `json:"updated"`
	Author  *struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	} `json:"author"`
}

func (c *Client) TaskComments(ctx context.Context, taskID int, out any) error {
	return c.List(ctx, url("/tasks/%d/comments", taskID), ListOptions{}, out)
}

func (c *Client) AddComment(ctx context.Context, taskID int, body, out any) error {
	return c.createVerb(ctx, url("/tasks/%d/comments", taskID), body, out)
}

func (c *Client) UpdateComment(ctx context.Context, taskID, commentID int, body, out any) error {
	return c.updateVerb(ctx, url("/tasks/%d/comments/%d", taskID, commentID), body, out)
}

func (c *Client) DeleteComment(ctx context.Context, taskID, commentID int) error {
	return c.Delete(ctx, url("/tasks/%d/comments/%d", taskID, commentID))
}

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

func (c *Client) SearchUsers(ctx context.Context, s string, out any) error {
	return c.List(ctx, "/users", ListParams{Search: s, PerPage: 50}.options(), out)
}

// SearchAll searches globally. Vikunja removed /search in v2; callers on
// v2 should compose ListProjects/ListTasks instead.
func (c *Client) SearchAll(ctx context.Context, s string, out any) error {
	return c.Get(ctx, "/search", map[string]string{"s": s}, out)
}

func (c *Client) MoveTaskToBucket(ctx context.Context, projectID, viewID, bucketID, taskID int, out any) error {
	body := map[string]any{
		"task_id":         taskID,
		"project_view_id": viewID,
		"bucket_id":       bucketID,
	}
	return c.updateVerb(ctx, url("/projects/%d/views/%d/buckets/%d/tasks", projectID, viewID, bucketID), body, out)
}

type Team struct {
	ID          int          `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	IsPublic    bool         `json:"is_public"`
	Members     []TeamMember `json:"members"`
}

type TeamMember struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
}

func (c *Client) ListTeams(ctx context.Context, query string, out any) error {
	return c.List(ctx, "/teams", ListParams{Search: query, PerPage: 50}.options(), out)
}

func (c *Client) GetTeam(ctx context.Context, teamID int, out any) error {
	return c.Get(ctx, url("/teams/%d", teamID), nil, out)
}

func (c *Client) CreateTeam(ctx context.Context, body, out any) error {
	return c.createVerb(ctx, "/teams", body, out)
}

func (c *Client) UpdateTeam(ctx context.Context, teamID int, body, out any) error {
	return c.updateVerb(ctx, url("/teams/%d", teamID), body, out)
}

func (c *Client) DeleteTeam(ctx context.Context, teamID int) error {
	return c.Delete(ctx, url("/teams/%d", teamID))
}

func (c *Client) AddTeamMember(ctx context.Context, teamID int, username string, out any) error {
	return c.createVerb(ctx, url("/teams/%d/members", teamID), map[string]any{"username": username}, out)
}

func (c *Client) RemoveTeamMember(ctx context.Context, teamID int, username string) error {
	return c.Delete(ctx, url("/teams/%d/members/%s", teamID, username))
}
