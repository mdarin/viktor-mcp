package tools

import (
	"testing"
)

func TestCommentListEmpty(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "comment_list", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(0) {
		t.Fatalf("out = %v", out)
	}
}

func TestCommentLifecycle(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "comment_add", map[string]any{"task_id": 301, "text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if out["id"] != float64(1) || out["text"] != "hello" || out["author"] != "fake" {
		t.Fatalf("add = %v", out)
	}
	out, err = callCRUD(t, session, "comment_update", map[string]any{"task_id": 301, "comment_id": 1, "text": "edited"})
	if err != nil {
		t.Fatal(err)
	}
	if out["text"] != "edited" {
		t.Fatalf("update = %v", out)
	}
	out, err = callCRUD(t, session, "comment_list", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	rows := out["comments"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["text"] != "edited" || rows[0].(map[string]any)["task_id"] != float64(301) {
		t.Fatalf("list = %v", out)
	}
	if rows[0].(map[string]any)["author"] != "fake" {
		t.Fatalf("author = %v", rows[0])
	}
	out, err = callCRUD(t, session, "comment_delete", map[string]any{"task_id": 301, "comment_id": 1})
	if err != nil {
		t.Fatal(err)
	}
	if out["deleted"] != true {
		t.Fatalf("delete = %v", out)
	}
	if _, err := callCRUD(t, session, "comment_delete", map[string]any{"task_id": 301, "comment_id": 1}); err == nil {
		t.Fatal("delete of missing comment must fail")
	}
}

func TestCommentAddValidation(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "comment_add", map[string]any{"task_id": 301, "text": "  "}); err == nil {
		t.Fatal("blank text must fail")
	}
}

func TestCommentRecentUnsupported(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "comment_recent", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false || out["count"] != float64(0) {
		t.Fatalf("out = %v", out)
	}
}
