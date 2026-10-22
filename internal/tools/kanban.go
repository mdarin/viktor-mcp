package tools

type KanbanGetIn struct {
	ProjectID int `json:"project_id"`
}

type KanbanSetupIn struct {
	ProjectID            int      `json:"project_id,omitempty"`
	ProjectTitle         string   `json:"project_title,omitempty"`
	Columns              []string `json:"columns,omitempty"`
	ViewTitle            string   `json:"view_title,omitempty"`
	DeleteDefaultBuckets *bool    `json:"delete_default_buckets,omitempty"`
}

type KanbanListBucketsIn struct {
	ProjectID int `json:"project_id"`
	ViewID    int `json:"view_id"`
}

type KanbanCreateBucketIn struct {
	ProjectID int    `json:"project_id"`
	ViewID    int    `json:"view_id"`
	Title     string `json:"title"`
	Position  int    `json:"position,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type KanbanDeleteBucketIn struct {
	ProjectID int `json:"project_id"`
	ViewID    int `json:"view_id"`
	BucketID  int `json:"bucket_id"`
}

type KanbanSortBucketIn struct {
	ProjectID int    `json:"project_id"`
	ViewID    int    `json:"view_id"`
	BucketID  int    `json:"bucket_id"`
	SortBy    string `json:"sort_by,omitempty"`
	ThenBy    string `json:"then_by,omitempty"`
}

type KanbanTasksByBucketIn struct {
	ProjectID int `json:"project_id"`
	ViewID    int `json:"view_id"`
}

func registerKanban(r *Registry) {
	add[KanbanGetIn](r, "kanban_get", "Get the kanban view for a project", handleKanbanGet(r))
	add[KanbanSetupIn](r, "kanban_setup", "Set up a kanban board with exactly the requested columns, one call, idempotent", handleKanbanSetup(r))
	add[KanbanListBucketsIn](r, "kanban_list_buckets", "List kanban buckets in a view", handleKanbanListBuckets(r))
	add[KanbanCreateBucketIn](r, "kanban_create_bucket", "Create a kanban bucket", handleKanbanCreateBucket(r))
	add[KanbanDeleteBucketIn](r, "kanban_delete_bucket", "Delete a kanban bucket; tasks may move to another bucket", handleKanbanDeleteBucket(r))
	add[KanbanSortBucketIn](r, "kanban_sort_bucket", "Re-sort all tasks in a bucket", handleKanbanSortBucket(r))
	add[KanbanTasksByBucketIn](r, "kanban_tasks_by_bucket", "Get tasks grouped by kanban bucket", handleKanbanTasksByBucket(r))
	add[BoardAuditIn](r, "board_audit", "Check a project's kanban board against its profile (missing/extra columns, done bucket, counts)", handleBoardAudit(r))
}
