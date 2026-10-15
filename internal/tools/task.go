package tools

import "time"

type TaskQueryIn struct {
	Query     string `json:"query"`
	Instance  string `json:"instance,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	ProjectID int    `json:"project_id,omitempty"`
}

type TaskListIn struct {
	ProjectID        int    `json:"project_id"`
	LabelFilter      string `json:"label_filter,omitempty"`
	IncludeCompleted bool   `json:"include_completed,omitempty"`
}

type TaskGetIn struct {
	TaskID int `json:"task_id"`
}

type TaskCreateIn struct {
	ProjectID   int       `json:"project_id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	DueDate     time.Time `json:"due_date,omitempty"`
	StartDate   time.Time `json:"start_date,omitempty"`
	EndDate     time.Time `json:"end_date,omitempty"`
	Priority    int       `json:"priority,omitempty"`
	HexColor    string    `json:"hex_color,omitempty"`
	RepeatAfter int       `json:"repeat_after,omitempty"`
	RepeatMode  int       `json:"repeat_mode,omitempty"`
}

type TaskDeleteIn struct {
	TaskID int `json:"task_id"`
}

type TaskMoveIn struct {
	TaskID          int `json:"task_id"`
	TargetProjectID int `json:"target_project_id"`
}

type TaskSetPositionIn struct {
	TaskID    int  `json:"task_id"`
	ProjectID int  `json:"project_id"`
	ViewID    int  `json:"view_id"`
	BucketID  int  `json:"bucket_id"`
	ApplySort bool `json:"apply_sort,omitempty"`
}

type TaskSetRemindersIn struct {
	TaskID    int         `json:"task_id"`
	Reminders []time.Time `json:"reminders"`
}

type TaskAddLabelIn struct {
	TaskID  int `json:"task_id"`
	LabelID int `json:"label_id"`
}

type TaskAssignUserIn struct {
	TaskID int `json:"task_id"`
	UserID int `json:"user_id"`
}

type TaskUnassignUserIn struct {
	TaskID int `json:"task_id"`
	UserID int `json:"user_id"`
}

type TaskCreateRelationIn struct {
	TaskID       int    `json:"task_id"`
	RelationKind string `json:"relation_kind"`
	OtherTaskID  int    `json:"other_task_id"`
}

type TaskListRelationsIn struct {
	TaskID int `json:"task_id"`
}

func registerTask(r *Registry) {
	add[TaskQueryIn](r, "task_query", "Query tasks by mode: focus, due-today, due-week, overdue, high-priority, urgent, unscheduled, summary", handleTaskQuery(r))
	add[TaskListIn](r, "task_list", "List tasks in a project", handleTaskList(r))
	add[TaskGetIn](r, "task_get", "Get task details", handleTaskGet(r))
	add[TaskCreateIn](r, "task_create", "Create a task in a project", handleTaskCreate(r))
	add[TaskUpdateIn](r, "task_update", "Partially update a task; merges server-side so omitted fields keep their values", handleTaskUpdate(r))
	add[TaskCompleteIn](r, "task_complete", "Atomically close a task: done, done bucket, verification GET", handleTaskComplete(r))
	add[TaskDeleteIn](r, "task_delete", "Delete a task permanently", handleTaskDelete(r))
	add[TaskMoveIn](r, "task_move", "Move a task to a different project", handleTaskMove(r))
	add[TaskSetPositionIn](r, "task_set_position", "Move a task to a kanban bucket", handleTaskSetPosition(r))
	add[TaskSetRemindersIn](r, "task_set_reminders", "Replace all reminders on a task", handleTaskSetReminders(r))
	add[TaskAddLabelIn](r, "task_add_label", "Add a label to a task", handleTaskAddLabel(r))
	add[TaskAssignUserIn](r, "task_assign_user", "Assign a user to a task", handleTaskAssignUser(r))
	add[TaskUnassignUserIn](r, "task_unassign_user", "Remove a user from a task", handleTaskUnassignUser(r))
	add[TaskCreateRelationIn](r, "task_create_relation", "Create a relation between two tasks", handleTaskCreateRelation(r))
	add[TaskListRelationsIn](r, "task_list_relations", "List relations of a task", handleTaskListRelations(r))
}
