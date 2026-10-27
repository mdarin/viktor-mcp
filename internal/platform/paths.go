// Package platform centralizes per-user filesystem locations so every
// subsystem (config, FSM, memory, shadow) resolves the same directories
// on every OS (card #939): XDG dirs by default, %AppData%/%LocalAppData%
// fallback on Windows.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir returns the per-user configuration directory for viktor:
// $XDG_CONFIG_HOME/viktor when set (on every OS — this is how tests and
// power users relocate state), otherwise $HOME/.config/viktor on unix
// and %AppData%\viktor on Windows.
func ConfigDir() (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "viktor"), nil
	}
	if runtime.GOOS == "windows" {
		return configDirWindows()
	}
	return configDirUnix()
}

// StateDir returns the per-user mutable state directory for viktor
// (FSM databases, sync cursors, shadow queue): $XDG_STATE_HOME/viktor
// when set (on every OS), otherwise $HOME/.local/state/viktor on unix
// and %LocalAppData%\viktor\state on Windows.
func StateDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "viktor"), nil
	}
	if runtime.GOOS == "windows" {
		return stateDirWindows()
	}
	return stateDirUnix()
}

func configDirUnix() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	return filepath.Join(home, ".config", "viktor"), nil
}

func configDirWindows() (string, error) {
	appData := os.Getenv("AppData")
	if appData == "" {
		return "", ErrNoHome
	}
	return filepath.Join(appData, "viktor"), nil
}

func stateDirUnix() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "viktor"), nil
}

func stateDirWindows() (string, error) {
	local := os.Getenv("LocalAppData")
	if local == "" {
		return "", ErrNoHome
	}
	return filepath.Join(local, "viktor", "state"), nil
}
