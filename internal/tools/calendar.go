package tools

type CalAddEventIn struct {
	ProjectID   int    `json:"project_id"`
	Title       string `json:"title"`
	DueDate     string `json:"due_date"`
	Description string `json:"description,omitempty"`
	StartDate   string `json:"start_date,omitempty"`
	EndDate     string `json:"end_date,omitempty"`
	LabelName   string `json:"label_name,omitempty"`
}

func registerCalendar(r *Registry) {
	add[CalAddEventIn](r, "cal_add_event", "Add an event to the calendar by creating a labeled task", handleCalAddEvent(r))
}
