package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/thenickygee/mirage/internal/config"
)

type Command struct {
	// Derived from filename
	ID   string
	Path string

	// Frontmatter
	Description string `yaml:"description"`
	Agent       string `yaml:"agent,omitempty"`
	Subtask     bool   `yaml:"subtask,omitempty"`
	Model       string `yaml:"model,omitempty"`

	// Body (template)
	Template string
}

func CommandsDir() (string, error) {
	return config.CommandsDir()
}

func LoadAll() ([]*Command, error) {
	dir, err := CommandsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading commands dir: %w", err)
	}

	var commands []*Command
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		c, loadErr := Load(filepath.Join(dir, e.Name()))
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("loading %s: %w", e.Name(), loadErr))
			continue
		}
		commands = append(commands, c)
	}
	if len(errs) > 0 {
		return commands, fmt.Errorf("%d command(s) failed to load: %w", len(errs), errs[0])
	}
	return commands, nil
}

func Load(path string) (*Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	c := &Command{
		ID:   strings.TrimSuffix(filepath.Base(path), ".md"),
		Path: path,
	}

	content := string(data)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) >= 3 {
			if err := yaml.Unmarshal([]byte(parts[1]), c); err != nil {
				return nil, fmt.Errorf("parsing frontmatter: %w", err)
			}
			c.Template = strings.TrimSpace(parts[2])
		}
	} else {
		c.Template = strings.TrimSpace(content)
	}

	return c, nil
}

func (c *Command) Save() error {
	type frontmatter struct {
		Description string `yaml:"description,omitempty"`
		Agent       string `yaml:"agent,omitempty"`
		Subtask     bool   `yaml:"subtask,omitempty"`
		Model       string `yaml:"model,omitempty"`
	}

	fm := frontmatter{
		Description: c.Description,
		Agent:       c.Agent,
		Subtask:     c.Subtask,
		Model:       c.Model,
	}

	enc, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshaling frontmatter: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(enc)
	sb.WriteString("---\n")
	if c.Template != "" {
		sb.WriteString("\n")
		sb.WriteString(c.Template)
		sb.WriteString("\n")
	}

	return os.WriteFile(c.Path, []byte(sb.String()), 0644)
}

// Watch watches the commands directory for changes, calling onChange whenever
// a file is created, written, removed, or renamed. Events are debounced so
// that a single save triggers only one call. Watch blocks until ctx is
// cancelled.
func Watch(ctx context.Context, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	dir, err := CommandsDir()
	if err != nil {
		return fmt.Errorf("resolving commands dir: %w", err)
	}

	if dirErr := watcher.Add(dir); dirErr != nil && !os.IsNotExist(dirErr) {
		return fmt.Errorf("watching commands dir: %w", dirErr)
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

func New(id string) (*Command, error) {
	dir, err := CommandsDir()
	if err != nil {
		return nil, err
	}
	return &Command{
		ID:   id,
		Path: filepath.Join(dir, id+".md"),
	}, nil
}
