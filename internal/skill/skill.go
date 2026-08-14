package skill

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

type Skill struct {
	// Derived from directory name
	ID   string
	Path string // path to SKILL.md

	// Frontmatter
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`

	// Body
	Content string
}

func SkillsDir() (string, error) {
	return config.SkillsDir()
}

func LoadAll() ([]*Skill, error) {
	dir, err := SkillsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading skills dir: %w", err)
	}

	var skills []*Skill
	var errs []error
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		skillPath := filepath.Join(dir, e.Name(), "SKILL.md")
		s, loadErr := Load(skillPath)
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("loading %s: %w", e.Name(), loadErr))
			continue
		}
		skills = append(skills, s)
	}
	if len(errs) > 0 {
		return skills, fmt.Errorf("%d skill(s) failed to load: %w", len(errs), errs[0])
	}
	return skills, nil
}

func Load(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	s := &Skill{
		ID:   filepath.Base(filepath.Dir(path)),
		Path: path,
	}

	content := string(data)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) >= 3 {
			if err := yaml.Unmarshal([]byte(parts[1]), s); err != nil {
				return nil, fmt.Errorf("parsing frontmatter: %w", err)
			}
			s.Content = strings.TrimSpace(parts[2])
		}
	} else {
		s.Content = strings.TrimSpace(content)
	}

	if s.Name == "" {
		s.Name = s.ID
	}

	return s, nil
}

// Watch watches the skills directory for changes, calling onChange whenever a
// file is created, written, removed, or renamed. Events are debounced so that
// a single save triggers only one call. Watch blocks until ctx is cancelled.
func Watch(ctx context.Context, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	dir, err := SkillsDir()
	if err != nil {
		return fmt.Errorf("resolving skills dir: %w", err)
	}

	if dirErr := watcher.Add(dir); dirErr != nil && !os.IsNotExist(dirErr) {
		return fmt.Errorf("watching skills dir: %w", dirErr)
	}

	// Also watch each existing skill subdirectory so edits to SKILL.md trigger reloads.
	if entries, readErr := os.ReadDir(dir); readErr == nil {
		for _, e := range entries {
			if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
				continue
			}
			subdir := filepath.Join(dir, e.Name())
			// Resolve symlinks so the watcher can watch the real path.
			if resolved, resolveErr := filepath.EvalSymlinks(subdir); resolveErr == nil {
				subdir = resolved
			}
			_ = watcher.Add(subdir)
		}
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
			// When a new skill subdirectory is created, start watching it.
			if event.Has(fsnotify.Create) {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					_ = watcher.Add(event.Name)
				}
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

func (s *Skill) Save() error {
	type frontmatter struct {
		Name          string            `yaml:"name"`
		Description   string            `yaml:"description"`
		License       string            `yaml:"license,omitempty"`
		Compatibility string            `yaml:"compatibility,omitempty"`
		AllowedTools  string            `yaml:"allowed-tools,omitempty"`
		Metadata      map[string]string `yaml:"metadata,omitempty"`
	}

	fm := frontmatter{
		Name:          s.Name,
		Description:   s.Description,
		License:       s.License,
		Compatibility: s.Compatibility,
		AllowedTools:  s.AllowedTools,
		Metadata:      s.Metadata,
	}

	enc, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshaling frontmatter: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(enc)
	sb.WriteString("---\n")
	if s.Content != "" {
		sb.WriteString("\n")
		sb.WriteString(s.Content)
		sb.WriteString("\n")
	}

	return os.WriteFile(s.Path, []byte(sb.String()), 0644)
}
