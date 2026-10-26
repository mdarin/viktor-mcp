// Package platform centralizes per-user filesystem locations so every
// subsystem (config, FSM, memory, shadow) resolves the same directories
// on every OS (card #939): XDG dirs on unix, %AppData%/%LocalAppData%
// on Windows.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir returns the per-user configuration directory for viktor:
// ($XDG_CONFIG_HOME|$HOME/.config)/viktor on unix, %AppData%\viktor on
// Windows.
func ConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		return configDirWindows()
	}
	return configDirUnix()
}

// StateDir returns the per-user mutable state directory for viktor
// (FSM databases, sync cursors, shadow queue): ($XDG_STATE_HOME|
// $HOME/.local/state)/viktor on unix, %LocalAppData%\viktor\state on
// Windows.
func StateDir() (string, error) {
	if runtime.GOOS == "windows" {
		return stateDirWindows()
	}
	return stateDirUnix()
}

func configDirUnix() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "viktor"), nil
}

func configDirWindows() (string, error) {
	appData := os.Getenv("AppData")
	if appData == "" {
		return "", ErrNoHome
	}
	return filepath.Join(appData, "viktor"), nil
}

func stateDirUnix() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "viktor"), nil
}

func stateDirWindows() (string, error) {
	local := os.Getenv("LocalAppData")
	if local == "" {
		return "", ErrNoHome
	}
	return filepath.Join(local, "viktor", "state"), nil
}
