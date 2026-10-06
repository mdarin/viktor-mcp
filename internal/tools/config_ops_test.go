package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func configFixture(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("VIKTOR_SETTINGS", path)
}

func TestConfigSetGetList(t *testing.T) {
	configFixture(t)
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "config_set", map[string]any{"key": "door_closes", "value": "22:00"})
	if err != nil || out["created"] != true {
		t.Fatalf("set = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "config_get", map[string]any{"key": "door_closes"})
	if err != nil || out["value"] != "22:00" {
		t.Fatalf("get = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "config_set", map[string]any{"key": "door_closes", "value": "23:00"})
	if err != nil || out["created"] != false {
		t.Fatalf("re-set = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "config_list", map[string]any{})
	if err != nil || out["count"] != float64(1) {
		t.Fatalf("list = %v err=%v", out, err)
	}
}

func TestConfigSecretMasking(t *testing.T) {
	configFixture(t)
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "config_set", map[string]any{"key": "api_token", "value": "super-secret"}); err != nil {
		t.Fatal(err)
	}
	out, err := callCRUD(t, session, "config_get", map[string]any{"key": "api_token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := out["value"]; leaked {
		t.Fatalf("secret leaked: %v", out)
	}
	if out["token_set"] != true {
		t.Fatalf("out = %v", out)
	}
	out, err = callCRUD(t, session, "config_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	row := out["keys"].([]any)[0].(map[string]any)
	if row["token_set"] != true {
		t.Fatalf("row = %v", row)
	}
}

func TestConfigUpdateDelete(t *testing.T) {
	configFixture(t)
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "config_update", map[string]any{"key": "missing", "value": "x"}); err == nil {
		t.Fatal("update of missing key must fail")
	}
	if _, err := callCRUD(t, session, "config_delete", map[string]any{"key": "missing"}); err == nil {
		t.Fatal("delete of missing key must fail")
	}
	callCRUD(t, session, "config_set", map[string]any{"key": "goal", "value": "v1"})
	out, err := callCRUD(t, session, "config_update", map[string]any{"key": "goal", "value": "v2"})
	if err != nil || out["updated"] != true {
		t.Fatalf("update = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "config_get", map[string]any{"key": "goal"})
	if err != nil || out["value"] != "v2" {
		t.Fatalf("get = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "config_delete", map[string]any{"key": "goal"})
	if err != nil || out["deleted"] != true {
		t.Fatalf("delete = %v err=%v", out, err)
	}
	if _, err := callCRUD(t, session, "config_get", map[string]any{"key": "goal"}); err == nil {
		t.Fatal("get after delete must fail")
	}
}

func TestConfigKeyValidation(t *testing.T) {
	configFixture(t)
	_, session := newCRUDFixture(t)
	for _, bad := range []string{"", "Has Space", "-leading", "UPPER", "a/b"} {
		if _, err := callCRUD(t, session, "config_set", map[string]any{"key": bad, "value": "x"}); err == nil {
			t.Fatalf("key %q must be rejected", bad)
		}
	}
}

func TestSettingsFilePerms(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VIKTOR_SETTINGS", filepath.Join(dir, "sub", "settings.json"))
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "config_set", map[string]any{"key": "a", "value": "b"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sub", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("empty settings file")
	}
}
