package platform

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

// XDG overrides win on every OS — tests (t.Setenv) and power users
// rely on this to relocate state (the card #939 CI lesson: without it
// all tests on Windows shared one real %LocalAppData% database).
func TestXDGOverridesCrossPlatform(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfg, "viktor"); dir != want {
		t.Fatalf("ConfigDir = %q, want %q", dir, want)
	}
	st := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", st)
	dir, err = StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(st, "viktor"); dir != want {
		t.Fatalf("StateDir = %q, want %q", dir, want)
	}
}

func TestConfigDirUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix branch")
	}
	xdg := filepath.Join(t.TempDir(), "xdg-cfg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdg, "viktor"); dir != want {
		t.Fatalf("ConfigDir = %q, want %q", dir, want)
	}
	// Fallback: $HOME/.config/viktor.
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	dir, err = ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "viktor"); dir != want {
		t.Fatalf("ConfigDir = %q, want %q", dir, want)
	}
}

func TestConfigDirWindows(t *testing.T) {
	appData := filepath.Join(t.TempDir(), "AppData", "Roaming")
	t.Setenv("AppData", appData)
	dir, err := configDirWindows()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(appData, "viktor"); dir != want {
		t.Fatalf("configDirWindows = %q, want %q", dir, want)
	}
	t.Setenv("AppData", "")
	if _, err := configDirWindows(); !errors.Is(err, ErrNoHome) {
		t.Fatalf("missing AppData: err = %v, want ErrNoHome", err)
	}
}

func TestStateDirUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix branch")
	}
	xdg := filepath.Join(t.TempDir(), "xdg-state")
	t.Setenv("XDG_STATE_HOME", xdg)
	dir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdg, "viktor"); dir != want {
		t.Fatalf("StateDir = %q, want %q", dir, want)
	}
	// Fallback: $HOME/.local/state/viktor.
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	dir, err = StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "viktor"); dir != want {
		t.Fatalf("StateDir = %q, want %q", dir, want)
	}
}

func TestStateDirWindows(t *testing.T) {
	local := filepath.Join(t.TempDir(), "AppData", "Local")
	t.Setenv("LocalAppData", local)
	dir, err := stateDirWindows()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(local, "viktor", "state"); dir != want {
		t.Fatalf("stateDirWindows = %q, want %q", dir, want)
	}
	t.Setenv("LocalAppData", "")
	if _, err := stateDirWindows(); !errors.Is(err, ErrNoHome) {
		t.Fatalf("missing LocalAppData: err = %v, want ErrNoHome", err)
	}
}
