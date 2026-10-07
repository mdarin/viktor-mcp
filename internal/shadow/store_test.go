package shadow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "shadow"), "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAppendSequencesEntries(t *testing.T) {
	s := newTestStore(t)
	e1 := &Entry{Tool: "task_create", Op: "create", Payload: json.RawMessage(`{"title":"a"}`)}
	e2 := &Entry{Tool: "task_update", Op: "update"}
	if err := s.Append(e1); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(e2); err != nil {
		t.Fatal(err)
	}
	if e1.Seq != 1 || e2.Seq != 2 {
		t.Fatalf("seqs = %d, %d", e1.Seq, e2.Seq)
	}
	if e1.ShadowID != "s-1" || e2.ShadowID != "s-2" {
		t.Fatalf("shadow ids = %q, %q", e1.ShadowID, e2.ShadowID)
	}
	all, err := s.ReadAll()
	if err != nil || len(all) != 2 {
		t.Fatalf("readAll = %d entries, err %v", len(all), err)
	}
	if len(s.Pending()) != 2 {
		t.Fatalf("pending = %d", s.PendingCount())
	}
}

func TestSeqRestoredAfterRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shadow")
	s1, _ := NewStore(root, "test")
	if err := s1.Append(&Entry{Tool: "task_create", Op: "create"}); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	e := &Entry{Tool: "task_create", Op: "create"}
	if err := s2.Append(e); err != nil {
		t.Fatal(err)
	}
	if e.Seq != 2 || e.ShadowID != "s-2" {
		t.Fatalf("after restart seq=%d shadow=%s", e.Seq, e.ShadowID)
	}
}

func TestMarkSyncedAndClear(t *testing.T) {
	s := newTestStore(t)
	if err := s.Append(&Entry{Tool: "task_create", Op: "create"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(&Entry{Tool: "task_update", Op: "update"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSynced("s-1", 777); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ReadAll()
	if all[0].Status != "synced" || all[0].RemoteID != 777 {
		t.Fatalf("entry0 = %+v", all[0])
	}
	if s.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", s.PendingCount())
	}
	if err := s.ClearSynced(); err != nil {
		t.Fatal(err)
	}
	all, _ = s.ReadAll()
	if len(all) != 1 || all[0].ShadowID != "s-2" {
		t.Fatalf("after clear: %+v", all)
	}
}

func TestSaveTaskAndRender(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProject(ProjectRef{ID: 64, Title: "My Queue"}); err != nil {
		t.Fatal(err)
	}
	snap := TaskSnapshot{ShadowID: "s-1", Title: "Fix thing", Description: "do it", DueDate: "2026-10-10T00:00:00Z", Priority: 3}
	if err := s.SaveTask(64, "My Queue", "s-1", snap); err != nil {
		t.Fatal(err)
	}
	var back TaskSnapshot
	if !s.LoadMirrorTask(64, "My Queue", "s-1", &back) {
		t.Fatal("mirror task not loaded")
	}
	if back.Title != "Fix thing" {
		t.Fatalf("title = %q", back.Title)
	}
	if err := s.RenderProject(64, "My Queue"); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(s.ProjectDir(64, "My Queue"), "project.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(md)
	for _, want := range []string{"# My Queue", "## Fix thing", "shadow: s-1", "due: 2026-10-10", "open 1, done 0"} {
		if !strings.Contains(got, want) {
			t.Errorf("project.md missing %q\n%s", want, got)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"My Queue!", "64-my-queue"},
		{"///", "64-untitled"},
		{"Österreich Übung", "64-sterreich-bung"},
	}
	for _, c := range cases {
		if got := Slug(64, c.in); got != c.want {
			t.Errorf("Slug(64, %q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeleteProjectRemovesMirror(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProject(ProjectRef{ID: 5, Title: "temp"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTask(5, "temp", "s-1", TaskSnapshot{ShadowID: "s-1", Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProject(5, "temp"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.ProjectDir(5, "temp")); !os.IsNotExist(err) {
		t.Fatalf("project dir still exists, err %v", err)
	}
}

func TestUnreachableDirIsPrivate(t *testing.T) {
	s := newTestStore(t)
	info, err := os.Stat(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("root mode = %v", info.Mode())
	}
}
