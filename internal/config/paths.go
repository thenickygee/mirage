package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Dirs holds the resolved base directories for opencode config and data.
type Dirs struct {
	// Config is the directory containing agents, skills, commands, and tools.
	Config string
	// Data is the directory containing sessions and message storage.
	Data string
}

// ResolveDirs returns the platform-appropriate opencode config and data directories.
//
//   - macOS / Linux: ~/.config/opencode  and  ~/.local/share/opencode
//   - Windows:       %APPDATA%\opencode   and  %LOCALAPPDATA%\opencode
func ResolveDirs() (Dirs, error) {
	switch runtime.GOOS {
	case "windows":
		return resolveWindows()
	default:
		return resolveUnix()
	}
}

func resolveUnix() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("resolving home directory: %w", err)
	}

	configBase := os.Getenv("XDG_CONFIG_HOME")
	if configBase == "" {
		configBase = filepath.Join(home, ".config")
	}

	dataBase := os.Getenv("XDG_DATA_HOME")
	if dataBase == "" {
		dataBase = filepath.Join(home, ".local", "share")
	}

	return Dirs{
		Config: filepath.Join(configBase, "opencode"),
		Data:   filepath.Join(dataBase, "opencode"),
	}, nil
}

func resolveWindows() (Dirs, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Dirs{}, fmt.Errorf("resolving home directory: %w", err)
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Dirs{}, fmt.Errorf("resolving home directory: %w", err)
		}
		localAppData = filepath.Join(home, "AppData", "Local")
	}

	return Dirs{
		Config: filepath.Join(appData, "opencode"),
		Data:   filepath.Join(localAppData, "opencode"),
	}, nil
}

// AgentsDir returns the path to the agents config directory.
func AgentsDir() (string, error) {
	d, err := ResolveDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Config, "agents"), nil
}

// SkillsDir returns the path to the skills config directory.
func SkillsDir() (string, error) {
	d, err := ResolveDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Config, "skills"), nil
}

// CommandsDir returns the path to the commands config directory.
func CommandsDir() (string, error) {
	d, err := ResolveDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Config, "commands"), nil
}

// ToolsDir returns the path to the tools config directory.
func ToolsDir() (string, error) {
	d, err := ResolveDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Config, "tools"), nil
}

// SessionsDir returns the path to the sessions data directory.
func SessionsDir() (string, error) {
	d, err := ResolveDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Data, "storage", "session"), nil
}

// MessagesDir returns the path to the messages data directory.
func MessagesDir() (string, error) {
	d, err := ResolveDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Data, "storage", "message"), nil
}
