package tools

type CommentListIn struct {
	TaskID int `json:"task_id"`
}

type CommentAddIn struct {
	TaskID int    `json:"task_id"`
	Text   string `json:"text"`
}

type CommentUpdateIn struct {
	TaskID    int    `json:"task_id"`
	CommentID int    `json:"comment_id"`
	Text      string `json:"text"`
}

type CommentDeleteIn struct {
	TaskID    int `json:"task_id"`
	CommentID int `json:"comment_id"`
}

type CommentRecentIn struct {
	Limit int `json:"limit,omitempty"`
}

func registerComment(r *Registry) {
	add[CommentListIn](r, "comment_list", "List comments on a task", handleCommentList(r))
	add[CommentAddIn](r, "comment_add", "Add a comment to a task", handleCommentAdd(r))
	add[CommentUpdateIn](r, "comment_update", "Update a comment", handleCommentUpdate(r))
	add[CommentDeleteIn](r, "comment_delete", "Delete a comment", handleCommentDelete(r))
	add[CommentRecentIn](r, "comment_recent", "Recent comments across all tasks", handleCommentRecent(r))
}
