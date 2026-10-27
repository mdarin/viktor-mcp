package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type fakeCRUD struct {
	srvURL     string
	tasks      map[int]map[string]any
	projects   map[int]map[string]any
	relations  map[int]map[string][]int
	nextID     int
	nextProjID int
	filters    []string
	moveBodies []map[string]any
	lastMove   map[string]any
	viewTasks  []any
	moves      []map[string]any
	teamsFake  *fakeTeams
	labels     map[int]map[string]any
	nextLabel  int
	comments   map[int][]string
	teamShares map[int]map[int]float64
	// verifier fixture hooks (tools/verifier_test.go)
	registry       *Registry
	verifierTaskID int
	verifierRepo   string
}

func (f *fakeCRUD) filteredTasks(filter string) []map[string]any {
	out := []map[string]any{}
	for _, t := range f.tasks {
		if strings.Contains(filter, "done = false") {
			if d, _ := t["done"].(bool); d {
				continue
			}
		}
		if i := strings.Index(filter, "project_id = "); i >= 0 {
			want := filter[i+len("project_id = "):]
			if j := strings.IndexAny(want, " )"); j >= 0 {
				want = want[:j]
			}
			if pid, _ := t["project_id"].(float64); fmt.Sprintf("%d", int(pid)) != want {
				continue
			}
		}
		if strings.Contains(filter, zeroDate) {
			if dd, ok := t["due_date"].(string); ok && dd != zeroDate {
				continue
			}
		}
		if strings.Contains(filter, "due_date < now") {
			if dd, ok := t["due_date"].(string); ok && dd >= "2026-10-05" {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

func (f *fakeCRUD) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/api/v2/tasks":
		filter := r.URL.Query().Get("filter")
		f.filters = append(f.filters, filter)
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		if sq := r.URL.Query().Get("q"); sq != "" {
			out := []map[string]any{}
			for _, t := range f.filteredTasks(filter) {
				if strings.Contains(t["title"].(string), sq) {
					out = append(out, t)
				}
			}
			json.NewEncoder(w).Encode(out)
			return
		}
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		json.NewEncoder(w).Encode(f.filteredTasks(filter))
	case strings.HasPrefix(path, "/api/v2/tasks/") && strings.HasSuffix(path, "/comments") && r.Method == http.MethodGet:
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		var id int
		fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
		rows := []map[string]any{}
		for i, text := range f.comments[id] {
			rows = append(rows, map[string]any{"id": float64(i + 1), "comment": text, "task_id": float64(id), "author": map[string]any{"username": "fake"}})
		}
		json.NewEncoder(w).Encode(rows)
	case strings.HasPrefix(path, "/api/v2/tasks/") && strings.Contains(path, "/comments/") && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		var tid, cid int
		fmt.Sscanf(path, "/api/v2/tasks/%d/comments/%d", &tid, &cid)
		cs := f.comments[tid]
		if cid < 1 || cid > len(cs) {
			http.Error(w, `{"code":4002}`, http.StatusNotFound)
			return
		}
		if r.Method == http.MethodDelete {
			text := cs[cid-1]
			f.comments[tid] = append(cs[:cid-1:cid-1], cs[cid:]...)
			json.NewEncoder(w).Encode(map[string]any{"id": float64(cid), "comment": text, "task_id": float64(tid)})
			return
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		text, _ := b["comment"].(string)
		cs[cid-1] = text
		json.NewEncoder(w).Encode(map[string]any{"id": float64(cid), "comment": text, "task_id": float64(tid), "updated": "2026-01-01T00:00:00Z"})
	case strings.HasPrefix(path, "/api/v2/tasks/") && r.Method == http.MethodGet:
		var id int
		fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
		t, ok := f.tasks[id]
		if !ok {
			http.Error(w, `{"code":4002}`, http.StatusNotFound)
			return
		}
		out := map[string]any{}
		for k, v := range t {
			out[k] = v
		}
		if r.URL.Query().Get("related") == "true" {
			rt := map[string]any{}
			for kind, ids := range f.relations[id] {
				list := []any{}
				for _, oid := range ids {
					list = append(list, f.tasks[oid])
				}
				rt[kind] = list
			}
			out["related_tasks"] = rt
		}
		json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(path, "/api/v2/tasks/") && strings.HasSuffix(path, "/relations") && r.Method == http.MethodPost:
		var id int
		fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		kind, _ := b["relation_kind"].(string)
		other := int(b["other_task_id"].(float64))
		if kind != "subtask" && kind != "parenttask" && kind != "blocking" && kind != "blocked" &&
			kind != "related" && kind != "duplicate" && kind != "proxypartof" && kind != "proxycontributessto" {
			http.Error(w, `{"code":4007,"message":"The task relation is invalid."}`, http.StatusBadRequest)
			return
		}
		if other == id {
			http.Error(w, `{"code":4010,"message":"You cannot relate a task with itself"}`, http.StatusBadRequest)
			return
		}
		if f.relations == nil {
			f.relations = map[int]map[string][]int{}
		}
		if f.relations[id] == nil {
			f.relations[id] = map[string][]int{}
		}
		f.relations[id][kind] = append(f.relations[id][kind], other)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"task_id": id, "other_task_id": other, "relation_kind": kind})
	case strings.HasPrefix(path, "/api/v2/tasks/") && strings.HasSuffix(path, "/position") && r.Method == http.MethodPut:
		var id int
		fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		b["task_id"] = float64(id)
		f.moves = append(f.moves, b)
		json.NewEncoder(w).Encode(map[string]any{"position": b["position"], "bucket_id": b["bucket_id"]})
		return
	case strings.HasPrefix(path, "/api/v2/tasks/") && r.Method == http.MethodPut:
		var id int
		fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.moveBodies = append(f.moveBodies, b)
		if t, ok := f.tasks[id]; ok {
			for _, key := range []string{"reminders", "title", "description", "priority", "due_date", "done", "hex_color"} {
				if v, has := b[key]; has {
					t[key] = v
				}
			}
			if pid, hasPID := b["project_id"]; hasPID {
				t["project_id"] = pid
				f.lastMove = map[string]any{"id": id, "project_id": pid}
			}
			json.NewEncoder(w).Encode(t)
		} else {
			http.Error(w, `{"code":4002}`, http.StatusNotFound)
		}
	case strings.HasPrefix(path, "/api/v2/tasks/") && r.Method == http.MethodDelete:
		var id int
		fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
		if strings.Contains(path, "/labels/") {
			var lid int
			fmt.Sscanf(path, "/api/v2/tasks/%d/labels/%d", &id, &lid)
			task := f.tasks[id]
			if task == nil {
				http.Error(w, `{"code":4004}`, http.StatusNotFound)
				return
			}
			existing, _ := task["labels"].([]any)
			kept := existing[:0]
			for _, l := range existing {
				if asInt(l.(map[string]any)["id"]) != lid {
					kept = append(kept, l)
				}
			}
			task["labels"] = kept
			json.NewEncoder(w).Encode(task)
			return
		}
		delete(f.tasks, id)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case strings.HasPrefix(path, "/api/v2/tasks/") && r.Method == http.MethodPost:
		if strings.HasSuffix(path, "/assignees") {
			var id int
			fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			json.NewEncoder(w).Encode(map[string]any{"user_id": b["user_id"]})
			return
		}
		if strings.HasSuffix(path, "/comments") {
			var id int
			fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			comment, _ := b["comment"].(string)
			f.comments[id] = append(f.comments[id], comment)
			json.NewEncoder(w).Encode(map[string]any{"id": float64(len(f.comments[id])), "comment": comment, "task_id": float64(id), "author": map[string]any{"username": "fake"}})
			return
		}
		if strings.HasSuffix(path, "/labels") {
			var id int
			fmt.Sscanf(path, "/api/v2/tasks/%d", &id)
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			lid := int(b["label_id"].(float64))
			task := f.tasks[id]
			if task == nil || f.labels[lid] == nil {
				http.Error(w, `{"code":4004}`, http.StatusNotFound)
				return
			}
			existing, _ := task["labels"].([]any)
			task["labels"] = append(existing, map[string]any{"id": float64(lid), "title": f.labels[lid]["title"]})
			json.NewEncoder(w).Encode(task)
			return
		}
		http.Error(w, "unexpected put "+path, http.StatusNotFound)
	case strings.HasPrefix(path, "/api/v2/tasks/") && r.Method == http.MethodDelete && strings.Contains(path, "/assignees/"):
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case strings.HasPrefix(path, "/api/v2/projects/") && strings.HasSuffix(path, "/tasks") && r.Method == http.MethodPost:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.nextID++
		created := map[string]any{"id": float64(f.nextID), "title": b["title"], "done": false, "priority": b["priority"], "project_id": 8}
		if d, ok := b["due_date"]; ok {
			created["due_date"] = d
		}
		if d, ok := b["hex_color"]; ok {
			created["hex_color"] = d
		}
		f.tasks[f.nextID] = created
		json.NewEncoder(w).Encode(created)
	case strings.HasPrefix(path, "/api/v2/projects/") && strings.HasSuffix(path, "/tasks") && r.Method == http.MethodGet && !strings.Contains(path, "/views"):
		filter := r.URL.Query().Get("filter")
		f.filters = append(f.filters, filter)
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		var pid int
		fmt.Sscanf(path, "/api/v2/projects/%d/tasks", &pid)
		out := []map[string]any{}
		for _, t := range f.filteredTasks(filter) {
			if tp, _ := t["project_id"].(float64); int(tp) == pid {
				out = append(out, t)
			}
		}
		json.NewEncoder(w).Encode(out)
	case strings.HasSuffix(path, "/position") && r.Method == http.MethodPut:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		json.NewEncoder(w).Encode(map[string]any{"position": 123.5, "bucket_id": b["bucket_id"]})
	case path == "/api/v2/projects" && r.Method == http.MethodGet:
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		out := []map[string]any{}
		for _, pr := range f.projects {
			out = append(out, pr)
		}
		json.NewEncoder(w).Encode(out)
	case path == "/api/v2/projects" && r.Method == http.MethodPost:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.nextProjID++
		created := map[string]any{"id": float64(f.nextProjID), "title": b["title"], "description": b["description"]}
		if f.projects == nil {
			f.projects = map[int]map[string]any{}
		}
		f.projects[f.nextProjID] = created
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(created)
	case strings.Contains(path, "/teams") && strings.HasPrefix(path, "/api/v2/projects/") && r.Method == http.MethodGet:
		var pid int
		fmt.Sscanf(path, "/api/v2/projects/%d/teams", &pid)
		out := []map[string]any{}
		for tid, perm := range f.teamShares[pid] {
			tm := f.teamsFake.teams[tid]
			out = append(out, map[string]any{"id": float64(tid), "name": tm["name"], "permission": perm})
		}
		json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(path, "/api/v2/projects/") && r.Method == http.MethodGet && !strings.Contains(path, "/views"):
		var pid int
		fmt.Sscanf(path, "/api/v2/projects/%d", &pid)
		pr, ok := f.projects[pid]
		if !ok {
			http.Error(w, `{"code":4044}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(pr)
	case strings.HasPrefix(path, "/api/v2/projects/") && r.Method == http.MethodPut && !strings.Contains(path, "/tasks") && !strings.Contains(path, "/views") && !strings.Contains(path, "/teams") && !strings.Contains(path, "/users") && !strings.Contains(path, "/buckets"):
		var pid int
		fmt.Sscanf(path, "/api/v2/projects/%d", &pid)
		pr, ok := f.projects[pid]
		if !ok {
			http.Error(w, `{"code":4044}`, http.StatusNotFound)
			return
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if t, hasT := b["title"]; hasT {
			pr["title"] = t
		}
		if d, hasD := b["description"]; hasD {
			pr["description"] = d
		}
		if a, hasA := b["is_archived"]; hasA {
			pr["is_archived"] = a
		}
		json.NewEncoder(w).Encode(pr)
	case strings.Contains(path, "/teams") && strings.HasPrefix(path, "/api/v2/projects/") && r.Method == http.MethodPost:
		var pid int
		fmt.Sscanf(path, "/api/v2/projects/%d/teams", &pid)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		tid := int(b["team_id"].(float64))
		if f.teamsFake == nil || f.teamsFake.teams[tid] == nil {
			http.Error(w, `{"code":6002}`, http.StatusNotFound)
			return
		}
		if f.teamShares[pid] == nil {
			f.teamShares[pid] = map[int]float64{}
		}
		if _, dup := f.teamShares[pid][tid]; dup {
			http.Error(w, `{"code":6004}`, http.StatusConflict)
			return
		}
		perm, _ := b["permission"].(float64)
		f.teamShares[pid][tid] = perm
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"team_id": float64(tid), "permission": perm})
	case strings.Contains(path, "/teams/") && strings.HasPrefix(path, "/api/v2/projects/") && r.Method == http.MethodDelete:
		var pid, tid int
		fmt.Sscanf(path, "/api/v2/projects/%d/teams/%d", &pid, &tid)
		if _, ok := f.teamShares[pid][tid]; !ok {
			http.Error(w, `{"code":6007}`, http.StatusForbidden)
			return
		}
		delete(f.teamShares[pid], tid)
		json.NewEncoder(w).Encode(map[string]any{"message": "Successfully deleted."})
	case strings.HasPrefix(path, "/api/v2/projects/") && r.Method == http.MethodDelete && !strings.Contains(path, "/views/") && !strings.Contains(path, "/buckets/"):
		var pid int
		fmt.Sscanf(path, "/api/v2/projects/%d", &pid)
		delete(f.projects, pid)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case strings.HasPrefix(path, "/api/v2/projects/") && strings.HasSuffix(path, "/views") && r.Method == http.MethodGet:
		if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": float64(39), "title": "List", "view_kind": "list"},
			{"id": float64(40), "title": "Kanban", "view_kind": "kanban", "done_bucket_id": float64(0)},
		})
	case strings.HasPrefix(path, "/api/v2/projects/") && strings.HasSuffix(path, "/views") && r.Method == http.MethodPost:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		json.NewEncoder(w).Encode(map[string]any{"id": float64(40), "title": b["title"], "view_kind": "kanban"})
	case strings.HasPrefix(path, "/api/v2/projects/") && strings.HasSuffix(path, "/buckets") && r.Method == http.MethodGet:
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": float64(41), "title": "To-Do", "position": float64(1000)},
			{"id": float64(42), "title": "Done", "position": float64(2000)},
		})
	case strings.HasPrefix(path, "/api/v2/projects/") && strings.HasSuffix(path, "/buckets") && r.Method == http.MethodPost:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		json.NewEncoder(w).Encode(map[string]any{"id": float64(41), "title": b["title"], "position": b["position"], "limit": b["limit"]})
	case strings.HasSuffix(path, "/views/40") && r.Method == http.MethodPut:
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case strings.Contains(path, "/buckets/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case strings.Contains(path, "/views/") && strings.HasSuffix(path, "/tasks") && r.Method == http.MethodGet && strings.Count(path, "/") == 7:
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode(f.viewTasks)
	case strings.Contains(path, "/buckets/") && strings.HasSuffix(path, "/tasks") && r.Method == http.MethodPut:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.moves = append(f.moves, b)
		json.NewEncoder(w).Encode(map[string]any{"bucket_id": b["bucket_id"], "task_id": b["task_id"]})
	case path == "/api/v2/teams" && r.Method == http.MethodGet:
		q := r.URL.Query().Get("q")
		out := []map[string]any{}
		if f.teamsFake == nil {
			json.NewEncoder(w).Encode(out)
			return
		}
		for _, t := range f.teamsFake.teams {
			if q == "" || strings.Contains(t["name"].(string), q) {
				out = append(out, t)
			}
		}
		json.NewEncoder(w).Encode(out)
	case path == "/api/v2/teams" && r.Method == http.MethodPost:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.teamsFake.next++
		created := map[string]any{"id": float64(f.teamsFake.next), "name": b["name"], "description": b["description"], "members": []any{}}
		f.teamsFake.teams[f.teamsFake.next] = created
		json.NewEncoder(w).Encode(created)
	case strings.HasPrefix(path, "/api/v2/teams/") && strings.HasSuffix(path, "/members") && r.Method == http.MethodPost:
		var tid int
		fmt.Sscanf(path, "/api/v2/teams/%d", &tid)
		tm, ok := f.teamsFake.teams[tid]
		if !ok {
			http.Error(w, `{"code":4002}`, http.StatusNotFound)
			return
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		ms, _ := tm["members"].([]any)
		for _, m := range ms {
			if m.(map[string]any)["username"] == b["username"] {
				http.Error(w, `{"code":6005}`, http.StatusConflict)
				return
			}
		}
		member := map[string]any{"id": float64(len(ms) + 1), "username": b["username"], "admin": false}
		ms = append(ms, member)
		tm["members"] = ms
		json.NewEncoder(w).Encode(member)
	case strings.Contains(path, "/members/") && strings.HasSuffix(path, "/members") == false && strings.Contains(path, "/teams/") && r.Method == http.MethodDelete:
		seg := strings.Split(strings.TrimPrefix(path, "/api/v2/teams/"), "/")
		var tid int
		fmt.Sscanf(seg[0], "%d", &tid)
		tm, ok := f.teamsFake.teams[tid]
		if !ok || len(seg) < 3 {
			http.Error(w, `{"code":4002}`, http.StatusNotFound)
			return
		}
		ms, _ := tm["members"].([]any)
		kept := ms[:0]
		for _, m := range ms {
			if m.(map[string]any)["username"] != seg[2] {
				kept = append(kept, m)
			}
		}
		tm["members"] = kept
		json.NewEncoder(w).Encode(map[string]any{"message": "deleted"})
	case strings.HasPrefix(path, "/api/v2/teams/") && r.Method == http.MethodGet:
		var tid int
		fmt.Sscanf(path, "/api/v2/teams/%d", &tid)
		tm, ok := f.teamsFake.teams[tid]
		if !ok {
			http.Error(w, `{"code":4002}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(tm)
	case strings.HasPrefix(path, "/api/v2/teams/") && r.Method == http.MethodPut && !strings.Contains(path, "/members"):
		var tid int
		fmt.Sscanf(path, "/api/v2/teams/%d", &tid)
		tm, ok := f.teamsFake.teams[tid]
		if !ok {
			http.Error(w, `{"code":4002}`, http.StatusNotFound)
			return
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if n, has := b["name"]; has {
			tm["name"] = n
		}
		if d, has := b["description"]; has {
			tm["description"] = d
		}
		json.NewEncoder(w).Encode(tm)
	case strings.HasPrefix(path, "/api/v2/teams/") && r.Method == http.MethodDelete && !strings.Contains(path, "/members"):
		var tid int
		fmt.Sscanf(path, "/api/v2/teams/%d", &tid)
		delete(f.teamsFake.teams, tid)
		json.NewEncoder(w).Encode(map[string]any{"message": "deleted"})
	case path == "/api/v2/labels" && r.Method == http.MethodGet:
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		out := []map[string]any{}
		for _, l := range f.labels {
			out = append(out, l)
		}
		json.NewEncoder(w).Encode(out)
	case path == "/api/v2/labels" && r.Method == http.MethodPost:
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.nextLabel++
		l := map[string]any{"id": float64(f.nextLabel), "title": b["title"], "hex_color": b["hex_color"]}
		f.labels[f.nextLabel] = l
		json.NewEncoder(w).Encode(l)
	default:
		http.Error(w, "unexpected: "+r.Method+" "+path, http.StatusNotFound)
	}
}

func newCRUDFixture(t *testing.T) (*fakeCRUD, *mcp.ClientSession) {
	t.Helper()
	// Tests relocate state via XDG_STATE_HOME; close any FSM stores
	// opened before this test and after it, so TempDir RemoveAll never
	// hits an open SQLite handle (Windows unlinkat semantics).
	CloseFSMStores()
	t.Cleanup(CloseFSMStores)
	f := &fakeCRUD{nextID: 300, nextProjID: 900, projects: map[int]map[string]any{}, labels: map[int]map[string]any{1: {"id": float64(1), "title": "qa"}}, nextLabel: 1, comments: map[int][]string{}, teamShares: map[int]map[int]float64{}, tasks: map[int]map[string]any{
		301: {"id": float64(301), "title": "a", "done": false, "priority": float64(5), "project_id": float64(8), "due_date": "2026-01-01T00:00:00Z", "labels": []any{}, "assignees": []any{}, "reminders": []any{}},
		302: {"id": float64(302), "title": "b", "done": true, "priority": float64(0), "project_id": float64(8), "labels": []any{}, "assignees": []any{}, "reminders": []any{}},
		303: {"id": float64(303), "title": "c", "done": false, "priority": float64(2), "project_id": float64(9), "labels": []any{}, "assignees": []any{}, "reminders": []any{}},
	}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	f.srvURL = srv.URL
	t.Cleanup(srv.Close)

	api := client.New(srv.URL, "test-token", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	registry := NewRegistry(srvMCP, api)
	f.registry = registry
	registry.RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srvMCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return f, session
}

func callCRUD(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if res.IsError {
		msg := "tool error"
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msg = tc.Text
			}
		}
		return nil, fmt.Errorf("%s", msg)
	}
	out, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m, nil
}

func TestTaskCreateShape(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_create", map[string]any{
		"project_id": 8, "title": "new", "priority": 3, "due_date": "2026-12-01T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["id"] != float64(301) || out["priority"] != float64(3) {
		t.Fatalf("create out = %v", out)
	}
	created := f.tasks[f.nextID]
	if created["due_date"] != "2026-12-01T10:00:00Z" {
		t.Fatalf("due_date = %v", created["due_date"])
	}
}

func TestTaskCreateEmptyTitle(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "task_create", map[string]any{"project_id": 8, "title": ""}); err == nil {
		t.Fatal("empty title must be rejected")
	}
}

func TestTaskGetWithRelated(t *testing.T) {
	f, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "task_create_relation", map[string]any{
		"task_id": 301, "relation_kind": "related", "other_task_id": 303,
	}); err != nil {
		t.Fatal(err)
	}
	_ = f
	out, err := callCRUD(t, session, "task_get", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["related_tasks"]; !ok {
		t.Fatalf("related_tasks expected: %v", out)
	}
}

func TestTaskGetMissing(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "task_get", map[string]any{"task_id": 999}); err == nil {
		t.Fatal("missing task must error")
	}
}

func TestTaskListFiltersDone(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_list", map[string]any{"project_id": 8})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("open-only expected, got %v", out)
	}
	if !strings.Contains(f.filters[len(f.filters)-1], "done = false") {
		t.Fatalf("filter = %q", f.filters[len(f.filters)-1])
	}
}

func TestTaskListLabelFilter(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["labels"] = []any{map[string]any{"id": float64(1), "title": "qa"}}
	out, err := callCRUD(t, session, "task_list", map[string]any{"project_id": 8, "label_filter": "qa"})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("label filter: %v", out)
	}
	out, _ = callCRUD(t, session, "task_list", map[string]any{"project_id": 8, "label_filter": "nope"})
	if out["count"] != float64(0) {
		t.Fatalf("no-match filter: %v", out)
	}
}

func TestTaskDeleteAndMove(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_delete", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if out["deleted"] != true || len(f.tasks) != 2 {
		t.Fatalf("delete: %v, tasks=%d", out, len(f.tasks))
	}
	out, err = callCRUD(t, session, "task_move", map[string]any{"task_id": 303, "target_project_id": 8})
	if err != nil {
		t.Fatal(err)
	}
	if out["moved"] != true {
		t.Fatalf("move out = %v", out)
	}
	if f.lastMove["project_id"] != float64(8) {
		t.Fatalf("move body = %v", f.moveBodies)
	}
	if len(f.moveBodies) != 1 || len(f.moveBodies[0]) < 5 {
		t.Fatalf("move must POST full body, got %d keys", len(f.moveBodies[0]))
	}
}

func TestTaskSetPosition(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_set_position", map[string]any{
		"task_id": 301, "project_id": 8, "view_id": 32, "bucket_id": 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["bucket_id"] != float64(42) {
		t.Fatalf("set_position out = %v", out)
	}
}

func TestTaskSetRemindersVerify(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_set_reminders", map[string]any{
		"task_id": 301, "reminders": []string{"2026-10-10T09:00:00Z", "2026-10-11T09:00:00Z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(2) {
		t.Fatalf("reminders out = %v", out)
	}
	if got := f.tasks[301]["reminders"].([]any); len(got) != 2 {
		t.Fatalf("reminders on task = %v", got)
	}
}

func TestTaskAssignRoundtrip(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_assign_user", map[string]any{"task_id": 301, "user_id": 2})
	if err != nil {
		t.Fatal(err)
	}
	if out["assigned"] != true {
		t.Fatalf("assign = %v", out)
	}
	out, err = callCRUD(t, session, "task_unassign_user", map[string]any{"task_id": 301, "user_id": 2})
	if err != nil || out["unassigned"] != true {
		t.Fatalf("unassign = %v err=%v", out, err)
	}
}

func TestTaskQueryModes(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_query", map[string]any{"query": "overdue"})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(2) || out["mode"] != "overdue" {
		t.Fatalf("overdue (301 + 303 zero-date): %v", out)
	}
	if f.filters[len(f.filters)-1] != "due_date < now && done = false" {
		t.Fatalf("filter = %q", f.filters[len(f.filters)-1])
	}
	out, err = callCRUD(t, session, "task_query", map[string]any{"query": "urgent", "project_id": 8})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("urgent (client-side done filter): %v", out)
	}
	out, err = callCRUD(t, session, "task_query", map[string]any{"query": "unscheduled"})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("unscheduled: %v", out)
	}
}

func TestTaskQuerySummaryAndUnknown(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_query", map[string]any{"query": "summary", "project_id": 8})
	if err != nil {
		t.Fatal(err)
	}
	if out["overdue"] == nil || out["due_week"] == nil {
		t.Fatalf("summary keys: %v", out)
	}
	if _, err := callCRUD(t, session, "task_query", map[string]any{"query": "bogus"}); err == nil {
		t.Fatal("unknown mode must be rejected")
	}
}

func TestTaskCreateHexColor(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_create", map[string]any{
		"project_id": 8, "title": "colored", "hex_color": "#8b5cf6",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["hex_color"] != "#8b5cf6" {
		t.Fatalf("hex_color = %v", out["hex_color"])
	}
	if f.tasks[f.nextID]["hex_color"] != "#8b5cf6" {
		t.Fatalf("body hex_color = %v", f.tasks[f.nextID]["hex_color"])
	}
	for _, bad := range []string{"8b5cf6", "#8b5c", "#zzzzzz", "#"} {
		if _, err := callCRUD(t, session, "task_create", map[string]any{
			"project_id": 8, "title": "bad", "hex_color": bad,
		}); err == nil {
			t.Fatalf("hex_color %q must be rejected", bad)
		}
	}
}

func TestTaskUpdateHexColor(t *testing.T) {
	f, session := newCRUDFixture(t)
	// Set.
	if _, err := callCRUD(t, session, "task_update", map[string]any{
		"task_id": 301, "hex_color": "#ff0000",
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.tasks[301]["hex_color"]; got != "#ff0000" {
		t.Fatalf("hex_color = %v", got)
	}
	// Merge semantics: other fields survive the GET→patch→POST roundtrip.
	if f.tasks[301]["title"] == nil || f.tasks[301]["done"] == nil {
		t.Fatalf("merge lost unrelated fields: %v", f.tasks[301])
	}
	// Clear with empty string.
	if _, err := callCRUD(t, session, "task_update", map[string]any{
		"task_id": 301, "hex_color": "",
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := f.tasks[301]["hex_color"]; !ok || got != "" {
		t.Fatalf("clear: hex_color = %v (present=%v)", got, ok)
	}
	// Invalid rejected client-side, no POST fired.
	if _, err := callCRUD(t, session, "task_update", map[string]any{
		"task_id": 301, "hex_color": "red",
	}); err == nil {
		t.Fatal("invalid hex_color must be rejected")
	}
}

func TestTaskCreateLabels(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_create", map[string]any{"project_id": 8, "title": "with label", "labels": []string{"QA"}})
	if err != nil {
		t.Fatal(err)
	}
	labels, _ := out["labels"].([]any)
	if len(labels) != 1 || labels[0].(map[string]any)["title"] != "qa" {
		t.Fatalf("labels = %v, want attached qa", labels)
	}
	if _, err := callCRUD(t, session, "task_create", map[string]any{"project_id": 8, "title": "bad", "labels": []string{"nope"}}); err == nil {
		t.Fatal("unknown label must be an error, not a silent drop")
	}
}

func TestTaskGetEnrichesBucket(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.viewTasks = []any{
		map[string]any{"id": float64(301), "title": "t1", "bucket_id": float64(42)},
	}
	out, err := callCRUD(t, session, "task_get", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if out["bucket_id"] != float64(42) || out["bucket_title"] != "Done" {
		t.Fatalf("bucket enrichment failed: bucket_id=%v bucket_title=%v", out["bucket_id"], out["bucket_title"])
	}
}

func TestTaskCreatePlacesInFirstBucket(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_create", map[string]any{"project_id": 8, "title": "parked"})
	if err != nil {
		t.Fatal(err)
	}
	// Fresh work must not land in the view's done bucket (card #893):
	// the first non-done bucket (41 "To-Do") is the default placement.
	if out["bucket_id"] != float64(41) || out["bucket_title"] != "To-Do" {
		t.Fatalf("bucket placement = %v/%v, want 41/To-Do", out["bucket_id"], out["bucket_title"])
	}
	if len(f.moves) != 1 || f.moves[0]["task_id"] != out["id"] {
		t.Fatalf("moves = %v", f.moves)
	}
}
