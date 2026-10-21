package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thenickygee/mirage/internal/harness"
)

type Skill struct {
	// Derived from directory name
	ID     string         `yaml:"-"`
	Path   string         `yaml:"-"` // path to SKILL.md
	Origin harness.Origin `yaml:"-"`

	// Frontmatter
	Name          string             `yaml:"name"`
	Description   string             `yaml:"description"`
	License       string             `yaml:"license,omitempty"`
	Compatibility string             `yaml:"compatibility,omitempty"`
	AllowedTools  harness.FlexString `yaml:"allowed-tools,omitempty"`
	Metadata      map[string]any     `yaml:"metadata,omitempty"`

	// Extra preserves frontmatter keys mirage has no field for (Claude Code
	// adds several) so saving doesn't drop them.
	Extra map[string]any `yaml:",inline"`

	// Body
	Content string `yaml:"-"`
}

func LoadAll() ([]*Skill, error) {
	var skills []*Skill
	var errs []error
	for _, root := range harness.Roots(harness.Skills) {
		found, rootErrs := loadRoot(root)
		skills = append(skills, found...)
		errs = append(errs, rootErrs...)
	}
	if len(errs) > 0 {
		return skills, fmt.Errorf("%d skill(s) failed to load: %w", len(errs), errs[0])
	}
	return skills, nil
}

func loadRoot(root harness.Root) ([]*Skill, []error) {
	entries, err := os.ReadDir(root.Dir)
	if err != nil {
		return nil, []error{fmt.Errorf("reading skills dir: %w", err)}
	}
	var skills []*Skill
	var errs []error
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		skillPath := filepath.Join(root.Dir, e.Name(), "SKILL.md")
		s, loadErr := Load(skillPath)
		if os.IsNotExist(loadErr) {
			continue
		}
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("loading %s: %w", e.Name(), loadErr))
			continue
		}
		s.Origin = root.Origin
		if root.Plugin != "" {
			s.ID = root.Plugin + ":" + s.ID
		}
		skills = append(skills, s)
	}
	return skills, errs
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

// Watch watches every skills directory for changes, calling onChange whenever
// a file is created, written, removed, or renamed. Events are debounced so that
// a single save triggers only one call. Watch blocks until ctx is cancelled.
func Watch(ctx context.Context, onChange func()) error {
	return harness.WatchRoots(ctx, harness.Skills, onChange)
}

func (s *Skill) Save() error {
	type frontmatter struct {
		Name          string             `yaml:"name"`
		Description   string             `yaml:"description"`
		License       string             `yaml:"license,omitempty"`
		Compatibility string             `yaml:"compatibility,omitempty"`
		AllowedTools  harness.FlexString `yaml:"allowed-tools,omitempty"`
		Metadata      map[string]any     `yaml:"metadata,omitempty"`
		Extra         map[string]any     `yaml:",inline"`
	}

	fm := frontmatter{
		Name:          s.Name,
		Description:   s.Description,
		License:       s.License,
		Compatibility: s.Compatibility,
		AllowedTools:  s.AllowedTools,
		Metadata:      s.Metadata,
		Extra:         s.Extra,
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
