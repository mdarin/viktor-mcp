package client

import (
	"context"
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
		Page:    p.Page,
		PerPage: p.PerPage,
		Limit:   p.Limit,
		Extra: map[string]string{
			"s":        p.Search,
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
	return c.Put(ctx, "/projects", body, out)
}

func (c *Client) UpdateProject(ctx context.Context, projectID int, body, out any) error {
	return c.Post(ctx, url("/projects/%d", projectID), body, out)
}

func (c *Client) DeleteProject(ctx context.Context, projectID int) error {
	return c.Delete(ctx, url("/projects/%d", projectID))
}

func (c *Client) ShareProject(ctx context.Context, projectID int, body, out any) error {
	return c.Put(ctx, url("/projects/%d/users", projectID), body, out)
}

func (c *Client) ProjectUsers(ctx context.Context, projectID int, out any) error {
	return c.Get(ctx, url("/projects/%d/users", projectID), nil, out)
}

func (c *Client) ShareProjectTeam(ctx context.Context, projectID int, body, out any) error {
	return c.Put(ctx, url("/projects/%d/teams", projectID), body, out)
}

func (c *Client) ProjectTeams(ctx context.Context, projectID int, out any) error {
	return c.Get(ctx, url("/projects/%d/teams", projectID), nil, out)
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
	return c.Put(ctx, url("/projects/%d/tasks", projectID), body, out)
}

func (c *Client) UpdateTask(ctx context.Context, taskID int, body, out any) error {
	return c.Post(ctx, url("/tasks/%d", taskID), body, out)
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
	return c.Post(ctx, url("/tasks/%d/position", taskID), body, out)
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
	return c.Put(ctx, url("/tasks/%d/labels", taskID), map[string]int{"label_id": labelID}, out)
}

func (c *Client) RemoveTaskLabel(ctx context.Context, taskID, labelID int) error {
	return c.Delete(ctx, url("/tasks/%d/labels/%d", taskID, labelID))
}

func (c *Client) CreateTaskRelation(ctx context.Context, taskID int, kind string, otherTaskID int, out any) error {
	return c.Put(ctx, url("/tasks/%d/relations", taskID), map[string]any{
		"other_task_id": otherTaskID,
		"relation_kind": kind,
	}, out)
}

func (c *Client) AssignUser(ctx context.Context, taskID, userID int, out any) error {
	return c.Put(ctx, url("/tasks/%d/assignees", taskID), map[string]int{"user_id": userID}, out)
}

func (c *Client) UnassignUser(ctx context.Context, taskID, userID int) error {
	return c.Delete(ctx, url("/tasks/%d/assignees/%d", taskID, userID))
}

func (c *Client) CreateRelation(ctx context.Context, taskID int, kind string, body, out any) error {
	return c.Put(ctx, url("/tasks/%d/relations/%s", taskID, kind), body, out)
}

func (c *Client) ListRelations(ctx context.Context, taskID int, out any) error {
	return c.Get(ctx, url("/tasks/%d/relations", taskID), nil, out)
}

type View struct {
	ID   int    `json:"id"`
	Kind string `json:"view_kind"`
}

func (c *Client) ListViews(ctx context.Context, projectID int, out any) error {
	return c.Get(ctx, url("/projects/%d/views", projectID), nil, out)
}

func (c *Client) CreateView(ctx context.Context, projectID int, body, out any) error {
	return c.Put(ctx, url("/projects/%d/views", projectID), body, out)
}

func (c *Client) UpdateView(ctx context.Context, projectID, viewID int, body, out any) error {
	return c.Post(ctx, url("/projects/%d/views/%d", projectID, viewID), body, out)
}

func (c *Client) DeleteView(ctx context.Context, projectID, viewID int) error {
	return c.Delete(ctx, url("/projects/%d/views/%d", projectID, viewID))
}

func (c *Client) ViewTasks(ctx context.Context, projectID, viewID int, out any) error {
	return c.Get(ctx, url("/projects/%d/views/%d/tasks", projectID, viewID), nil, out)
}

func (c *Client) ListBuckets(ctx context.Context, projectID, viewID int, out any) error {
	return c.Get(ctx, url("/projects/%d/views/%d/buckets", projectID, viewID), nil, out)
}

func (c *Client) CreateBucket(ctx context.Context, projectID, viewID int, body, out any) error {
	return c.Put(ctx, url("/projects/%d/views/%d/buckets", projectID, viewID), body, out)
}

func (c *Client) DeleteBucket(ctx context.Context, projectID, viewID, bucketID int) error {
	return c.Delete(ctx, url("/projects/%d/views/%d/buckets/%d", projectID, viewID, bucketID))
}

func (c *Client) BucketTasks(ctx context.Context, projectID, viewID int, out any) error {
	return c.Get(ctx, url("/projects/%d/views/%d/tasks", projectID, viewID), nil, out)
}

type Label struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

func (c *Client) ListLabels(ctx context.Context, out any) error {
	return c.Get(ctx, "/labels", nil, out)
}

func (c *Client) CreateLabel(ctx context.Context, body, out any) error {
	return c.Put(ctx, "/labels", body, out)
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
	return c.Get(ctx, url("/tasks/%d/comments", taskID), nil, out)
}

func (c *Client) AddComment(ctx context.Context, taskID int, body, out any) error {
	return c.Put(ctx, url("/tasks/%d/comments", taskID), body, out)
}

func (c *Client) UpdateComment(ctx context.Context, taskID, commentID int, body, out any) error {
	return c.Post(ctx, url("/tasks/%d/comments/%d", taskID, commentID), body, out)
}

func (c *Client) DeleteComment(ctx context.Context, taskID, commentID int) error {
	return c.Delete(ctx, url("/tasks/%d/comments/%d", taskID, commentID))
}

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

func (c *Client) SearchUsers(ctx context.Context, s string, out any) error {
	return c.Get(ctx, "/users", map[string]string{"s": s}, out)
}

func (c *Client) SearchAll(ctx context.Context, s string, out any) error {
	return c.Get(ctx, "/search", map[string]string{"s": s}, out)
}

func (c *Client) MoveTaskToBucket(ctx context.Context, projectID, viewID, bucketID, taskID int, out any) error {
	body := map[string]any{
		"task_id":         taskID,
		"project_view_id": viewID,
		"bucket_id":       bucketID,
	}
	return c.Post(ctx, url("/projects/%d/views/%d/buckets/%d/tasks", projectID, viewID, bucketID), body, out)
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
	var queryMap map[string]string
	if query != "" {
		queryMap = map[string]string{"s": query}
	}
	return c.Get(ctx, "/teams", queryMap, out)
}

func (c *Client) GetTeam(ctx context.Context, teamID int, out any) error {
	return c.Get(ctx, url("/teams/%d", teamID), nil, out)
}

func (c *Client) CreateTeam(ctx context.Context, body, out any) error {
	return c.Put(ctx, "/teams", body, out)
}

func (c *Client) UpdateTeam(ctx context.Context, teamID int, body, out any) error {
	return c.Post(ctx, url("/teams/%d", teamID), body, out)
}

func (c *Client) DeleteTeam(ctx context.Context, teamID int) error {
	return c.Delete(ctx, url("/teams/%d", teamID))
}

func (c *Client) AddTeamMember(ctx context.Context, teamID int, username string, out any) error {
	return c.Put(ctx, url("/teams/%d/members", teamID), map[string]any{"username": username}, out)
}

func (c *Client) RemoveTeamMember(ctx context.Context, teamID int, username string) error {
	return c.Delete(ctx, url("/teams/%d/members/%s", teamID, username))
}
