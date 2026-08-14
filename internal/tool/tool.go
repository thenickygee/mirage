package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/thenickygee/mirage/internal/config"
)

type Tool struct {
	ID      string
	Path    string
	Ext     string
	Content string
}

func ToolsDir() (string, error) {
	return config.ToolsDir()
}

func LoadAll() ([]*Tool, error) {
	dir, err := ToolsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading tools dir: %w", err)
	}

	var tools []*Tool
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		// Only load recognized tool file types
		switch ext {
		case ".ts", ".js", ".py", ".sh":
		default:
			continue
		}
		t, loadErr := Load(filepath.Join(dir, name))
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("loading %s: %w", name, loadErr))
			continue
		}
		tools = append(tools, t)
	}
	if len(errs) > 0 {
		return tools, fmt.Errorf("%d tool(s) failed to load: %w", len(errs), errs[0])
	}
	return tools, nil
}

func Load(path string) (*Tool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	return &Tool{
		ID:      strings.TrimSuffix(name, ext),
		Path:    path,
		Ext:     ext,
		Content: string(data),
	}, nil
}

// Watch watches the tools directory for changes, calling onChange whenever a
// file is created, written, removed, or renamed. Events are debounced so that
// a single save triggers only one call. Watch blocks until ctx is cancelled.
func Watch(ctx context.Context, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	dir, err := ToolsDir()
	if err != nil {
		return fmt.Errorf("resolving tools dir: %w", err)
	}

	if dirErr := watcher.Add(dir); dirErr != nil && !os.IsNotExist(dirErr) {
		return fmt.Errorf("watching tools dir: %w", dirErr)
	}

	var debounce *time.Timer
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
				!event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(300*time.Millisecond, onChange)
		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		}
	}
}

// Description extracts a description from the tool source if available.
// It looks for a `description:` field in the source text.
func (t *Tool) Description() string {
	for _, line := range strings.Split(t.Content, "\n") {
		trimmed := strings.TrimSpace(line)
		// Match `description: "..."` or `description: '...'`
		if strings.HasPrefix(trimmed, "description") {
			colon := strings.Index(trimmed, ":")
			if colon < 0 {
				continue
			}
			val := strings.TrimSpace(trimmed[colon+1:])
			val = strings.Trim(val, `"',`)
			if val != "" {
				return val
			}
		}
	}
	return ""
}
