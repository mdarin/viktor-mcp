package tools

type BatchTaskSpec struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	DueDate     string   `json:"due_date,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Bucket      string   `json:"bucket,omitempty"`
	Ref         string   `json:"ref,omitempty"`
	BlockedBy   []string `json:"blocked_by,omitempty"`
	Blocks      []string `json:"blocks,omitempty"`
}

type BatchCreateTasksIn struct {
	ProjectID           int             `json:"project_id"`
	Tasks               []BatchTaskSpec `json:"tasks"`
	CreateMissingLabels bool            `json:"create_missing_labels,omitempty"`
}

type BatchUpdate struct {
	TaskID      int    `json:"task_id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Priority    int    `json:"priority,omitempty"`
}

type BatchUpdateTasksIn struct {
	Updates []BatchUpdate `json:"updates"`
}

type BatchRelabelIn struct {
	ProjectID    int      `json:"project_id"`
	TaskIDs      []int    `json:"task_ids"`
	AddLabels    []string `json:"add_labels,omitempty"`
	RemoveLabels []string `json:"remove_labels,omitempty"`
	SetLabels    []string `json:"set_labels,omitempty"`
}

type BatchMoveByLabelIn struct {
	ProjectID   int    `json:"project_id"`
	LabelFilter string `json:"label_filter"`
	ViewID      int    `json:"view_id"`
	BucketID    int    `json:"bucket_id"`
}

type BatchCompleteByLabelIn struct {
	ProjectID   int    `json:"project_id"`
	LabelFilter string `json:"label_filter"`
}

type BatchCreateLabelsIn struct {
	Labels []BatchLabelSpec `json:"labels"`
}

type BatchLabelSpec struct {
	Title    string `json:"title"`
	HexColor string `json:"hex_color,omitempty"`
}

type BatchAssignBucketsIn struct {
	ProjectID   int                `json:"project_id"`
	ViewID      int                `json:"view_id"`
	Assignments []BucketAssignSpec `json:"assignments"`
}

type BucketAssignSpec struct {
	TaskID   int `json:"task_id"`
	BucketID int `json:"bucket_id"`
	Position int `json:"position,omitempty"`
}

type BatchLabelToBucketsIn struct {
	ProjectID        int            `json:"project_id"`
	ViewID           int            `json:"view_id"`
	LabelToBucketMap map[string]int `json:"label_to_bucket_map"`
}

type BatchReorderTasksIn struct {
	ProjectID int            `json:"project_id"`
	ViewID    int            `json:"view_id"`
	Positions []PositionSpec `json:"positions"`
}

type PositionSpec struct {
	TaskID   int `json:"task_id"`
	Position int `json:"position"`
}

func registerBatch(r *Registry) {
	add[BatchCreateTasksIn](r, "batch_create_tasks", "Create multiple tasks at once with labels and relations", handleBatchCreateTasks(r))
	add[BatchUpdateTasksIn](r, "batch_update_tasks", "Update multiple tasks at once", handleBatchUpdateTasks(r))
	add[BatchRelabelIn](r, "batch_relabel", "Bulk update labels on multiple tasks", handleBatchRelabel(r))
	add[BatchMoveByLabelIn](r, "batch_move_by_label", "Move all tasks with a matching label to a bucket", handleBatchMoveByLabel(r))
	add[BatchCompleteByLabelIn](r, "batch_complete_by_label", "Complete all tasks with a matching label", handleBatchCompleteByLabel(r))
	add[BatchCreateLabelsIn](r, "batch_create_labels", "Create multiple labels, skipping existing ones", handleBatchCreateLabels(r))
	add[BatchAssignBucketsIn](r, "batch_assign_buckets", "Bulk assign tasks to kanban buckets", handleBatchAssignBuckets(r))
	add[BatchLabelToBucketsIn](r, "batch_label_to_buckets", "Move tasks to buckets based on their labels", handleBatchLabelToBuckets(r))
	add[BatchReorderTasksIn](r, "batch_reorder_tasks", "Set positions for multiple tasks in a view", handleBatchReorderTasks(r))
	add[BatchGetIn](r, "task_batch_get", "Fetch multiple tasks concurrently, results in request order", handleBatchGet(r))
}
