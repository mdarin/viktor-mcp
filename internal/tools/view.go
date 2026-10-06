package tools

type ViewListIn struct {
	ProjectID int `json:"project_id"`
}

type ViewCreateIn struct {
	ProjectID   int    `json:"project_id"`
	Title       string `json:"title"`
	ViewKind    string `json:"view_kind"`
	FilterQuery string `json:"filter_query,omitempty"`
}

type ViewUpdateIn struct {
	ProjectID   int    `json:"project_id"`
	ViewID      int    `json:"view_id"`
	Title       string `json:"title,omitempty"`
	FilterQuery string `json:"filter_query,omitempty"`
}

type ViewDeleteIn struct {
	ProjectID int `json:"project_id"`
	ViewID    int `json:"view_id"`
}

type ViewGetTasksIn struct {
	ProjectID int `json:"project_id"`
	ViewID    int `json:"view_id"`
}

type ViewSetPositionIn struct {
	TaskID   int     `json:"task_id"`
	ViewID   int     `json:"view_id"`
	Position float64 `json:"position"`
}

func registerView(r *Registry) {
	add[ViewListIn](r, "view_list", "List all views for a project", handleViewList(r))
	add[ViewCreateIn](r, "view_create", "Create a view for a project", handleViewCreate(r))
	add[ViewUpdateIn](r, "view_update", "Update a view title or filter", handleViewUpdate(r))
	add[ViewDeleteIn](r, "view_delete", "Delete a view from a project", handleViewDelete(r))
	add[ViewGetTasksIn](r, "view_get_tasks", "Get tasks via a specific view; kanban includes bucket info", handleViewGetTasks(r))
	add[ViewSetPositionIn](r, "view_set_position", "Set a task position within a list, gantt, or table view", handleViewSetPosition(r))
}
