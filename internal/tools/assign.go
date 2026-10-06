package tools

type AssignQueueIn struct {
	Instance string `json:"instance,omitempty"`
}

type AssignApplyIn struct {
	Instance    string `json:"instance,omitempty"`
	TaskID      int    `json:"task_id"`
	Destination string `json:"destination"`
	Notes       string `json:"notes,omitempty"`
}

func registerAssign(r *Registry) {
	add[AssignQueueIn](r, "assign_queue", "Check the assignment queue for pending handoff items", handleAssignQueue(r))
	add[AssignApplyIn](r, "assign_apply", "Claim and complete a handoff item from the assignment queue", handleAssignApply(r))
}
