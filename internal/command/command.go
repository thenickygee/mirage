package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thenickygee/mirage/internal/config"
	"github.com/thenickygee/mirage/internal/harness"
)

type Command struct {
	// Derived from filename
	ID     string         `yaml:"-"`
	Path   string         `yaml:"-"`
	Origin harness.Origin `yaml:"-"`

	// Frontmatter
	Description  string             `yaml:"description"`
	Agent        string             `yaml:"agent,omitempty"`
	Subtask      bool               `yaml:"subtask,omitempty"`
	Model        string             `yaml:"model,omitempty"`
	ArgumentHint harness.FlexString `yaml:"argument-hint,omitempty"`
	AllowedTools harness.FlexString `yaml:"allowed-tools,omitempty"`

	// Extra preserves frontmatter keys mirage has no field for.
	Extra map[string]any `yaml:",inline"`

	// Body (template)
	Template string `yaml:"-"`
}

func CommandsDir() (string, error) {
	return config.CommandsDir()
}

func LoadAll() ([]*Command, error) {
	var commands []*Command
	var errs []error
	for _, root := range harness.Roots(harness.Commands) {
		found, rootErrs := loadRoot(root)
		commands = append(commands, found...)
		errs = append(errs, rootErrs...)
	}
	if len(errs) > 0 {
		return commands, fmt.Errorf("%d command(s) failed to load: %w", len(errs), errs[0])
	}
	return commands, nil
}

// loadRoot loads every .md file under root. Claude Code treats subdirectories
// as namespaces (/dir:name) and prefixes plugin commands with the plugin name;
// OpenCode roots are read flat.
func loadRoot(root harness.Root) ([]*Command, []error) {
	var commands []*Command
	var errs []error
	_ = filepath.WalkDir(root.Dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if path != root.Dir && root.Harness != harness.Claude {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		c, loadErr := Load(path)
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("loading %s: %w", d.Name(), loadErr))
			return nil
		}
		c.Origin = root.Origin
		c.ID = qualifiedID(root, path)
		commands = append(commands, c)
		return nil
	})
	return commands, errs
}

func qualifiedID(root harness.Root, path string) string {
	rel, err := filepath.Rel(root.Dir, path)
	if err != nil {
		return strings.TrimSuffix(filepath.Base(path), ".md")
	}
	id := strings.ReplaceAll(strings.TrimSuffix(rel, ".md"), string(filepath.Separator), ":")
	if root.Plugin != "" {
		id = root.Plugin + ":" + id
	}
	return id
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

		ArgumentHint harness.FlexString `yaml:"argument-hint,omitempty"`
		AllowedTools harness.FlexString `yaml:"allowed-tools,omitempty"`
		Extra        map[string]any     `yaml:",inline"`
	}

	fm := frontmatter{
		Description:  c.Description,
		Agent:        c.Agent,
		Subtask:      c.Subtask,
		Model:        c.Model,
		ArgumentHint: c.ArgumentHint,
		AllowedTools: c.AllowedTools,
		Extra:        c.Extra,
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

// Watch watches every commands directory for changes, calling onChange
// whenever a file is created, written, removed, or renamed. Events are
// debounced so that a single save triggers only one call. Watch blocks until
// ctx is cancelled.
func Watch(ctx context.Context, onChange func()) error {
	return harness.WatchRoots(ctx, harness.Commands, onChange)
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
