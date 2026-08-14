package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/thenickygee/mirage/internal/config"
)

// headerRe matches lines like "build (primary)" or "explore (subagent)"
var headerRe = regexp.MustCompile(`^(\S+)\s+\((\w+)\)\s*$`)

// Permission represents agent permission configuration.
type Permission map[string]interface{}

// Source indicates where an agent was loaded from.
type Source int

const (
	// SourceFile indicates the agent was loaded from ~/.config/opencode/agents/*.md.
	SourceFile Source = iota
	// SourceBuiltin indicates a built-in agent reported by `opencode agent list`.
	SourceBuiltin
)

// Agent represents an OpenCode agent configuration.
type Agent struct {
	ID          string
	Path        string
	Source      Source
	Description string     `yaml:"description"`
	Mode        string     `yaml:"mode"`
	Model       string     `yaml:"model"`
	Temperature *float64   `yaml:"temperature,omitempty"`
	TopP        *float64   `yaml:"top_p,omitempty"`
	Steps       *int       `yaml:"steps,omitempty"`
	Color       string     `yaml:"color,omitempty"`
	Hidden      bool       `yaml:"hidden,omitempty"`
	Disable     bool       `yaml:"disable,omitempty"`
	Skills      []string   `yaml:"skills,omitempty"`
	Permission  Permission `yaml:"permission,omitempty"`
	Prompt      string
}

// AgentsDir returns the path to the agents configuration directory.
func AgentsDir() (string, error) {
	return config.AgentsDir()
}

func loadFromFiles() ([]*Agent, error) {
	dir, err := AgentsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading agents dir: %w", err)
	}

	var agents []*Agent
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		a, loadErr := Load(filepath.Join(dir, e.Name()))
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("loading %s: %w", e.Name(), loadErr))
			continue
		}
		agents = append(agents, a)
	}
	if len(errs) > 0 {
		return agents, fmt.Errorf("%d agent(s) failed to load: %w", len(errs), errs[0])
	}
	return agents, nil
}

func Load(path string) (*Agent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	a := &Agent{
		ID:   strings.TrimSuffix(filepath.Base(path), ".md"),
		Path: path,
	}

	content := string(data)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) >= 3 {
			if err := yaml.Unmarshal([]byte(parts[1]), a); err != nil {
				return nil, fmt.Errorf("parsing frontmatter: %w", err)
			}
			a.Prompt = strings.TrimSpace(parts[2])
		}
	} else {
		a.Prompt = strings.TrimSpace(content)
	}

	return a, nil
}

// Save writes the agent configuration back to its markdown file.
func (a *Agent) Save() error {
	if a.Path == "" {
		return fmt.Errorf("cannot save built-in agent %q: no file path", a.ID)
	}
	var buf bytes.Buffer

	buf.WriteString("---\n")

	type frontmatter struct {
		Description string     `yaml:"description,omitempty"`
		Mode        string     `yaml:"mode,omitempty"`
		Model       string     `yaml:"model,omitempty"`
		Temperature *float64   `yaml:"temperature,omitempty"`
		TopP        *float64   `yaml:"top_p,omitempty"`
		Steps       *int       `yaml:"steps,omitempty"`
		Color       string     `yaml:"color,omitempty"`
		Hidden      bool       `yaml:"hidden,omitempty"`
		Disable     bool       `yaml:"disable,omitempty"`
		Skills      []string   `yaml:"skills,omitempty"`
		Permission  Permission `yaml:"permission,omitempty"`
	}

	fm := frontmatter{
		Description: a.Description,
		Mode:        a.Mode,
		Model:       a.Model,
		Temperature: a.Temperature,
		TopP:        a.TopP,
		Steps:       a.Steps,
		Color:       a.Color,
		Hidden:      a.Hidden,
		Disable:     a.Disable,
		Skills:      a.Skills,
		Permission:  a.Permission,
	}

	enc, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshaling frontmatter: %w", err)
	}
	buf.Write(enc)
	buf.WriteString("---\n")
	if a.Prompt != "" {
		buf.WriteString("\n")
		buf.WriteString(a.Prompt)
		buf.WriteString("\n")
	}

	return os.WriteFile(a.Path, buf.Bytes(), 0644)
}

func New(id string) (*Agent, error) {
	dir, err := AgentsDir()
	if err != nil {
		return nil, err
	}
	return &Agent{
		ID:   id,
		Path: filepath.Join(dir, id+".md"),
		Mode: "subagent",
	}, nil
}

// LoadFromCLI runs `opencode agent list` and parses its output, returning
// agents keyed by lowercase ID. These are built-in agents not present as
// files on disk.
func LoadFromCLI() (map[string]*Agent, error) {
	out, err := exec.Command("opencode", "agent", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("running opencode agent list: %w", err)
	}

	agents := make(map[string]*Agent)
	scanner := bufio.NewScanner(bytes.NewReader(out))

	var current *Agent
	var jsonLines []string

	flush := func() {
		if current == nil {
			return
		}
		if len(jsonLines) > 0 {
			raw := strings.Join(jsonLines, "\n")
			var perms []map[string]interface{}
			if json.Unmarshal([]byte(raw), &perms) == nil {
				p := make(Permission)
				for i, entry := range perms {
					key := fmt.Sprintf("%d", i)
					if perm, ok := entry["permission"]; ok {
						key = fmt.Sprintf("%v", perm)
					}
					p[key] = entry
				}
				current.Permission = p
			}
		}
		agents[strings.ToLower(current.ID)] = current
		current = nil
		jsonLines = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if m := headerRe.FindStringSubmatch(line); m != nil {
			flush()
			current = &Agent{
				ID:     m[1],
				Mode:   m[2],
				Source: SourceBuiltin,
			}
		} else if current != nil {
			jsonLines = append(jsonLines, line)
		}
	}
	flush()

	return agents, scanner.Err()
}

// LoadAll loads agents from disk files and supplements them with built-in
// agents reported by `opencode agent list`. File-defined agents take precedence.
func LoadAll() ([]*Agent, error) {
	fileAgents, err := loadFromFiles()

	// Build a set of IDs already covered by file agents.
	fileIDs := make(map[string]bool, len(fileAgents))
	for _, a := range fileAgents {
		fileIDs[strings.ToLower(a.ID)] = true
	}

	// Fetch built-in agents; if this fails we still return file agents.
	cliAgents, cliErr := LoadFromCLI()
	if cliErr == nil {
		for id, a := range cliAgents {
			if !fileIDs[id] {
				fileAgents = append(fileAgents, a)
			}
		}
	}

	// Attach the global AGENTS.md from the config root to the builtin "build"
	// agent, but only if a file-based agent hasn't already overridden it.
	if !fileIDs["build"] {
		if prompt, readErr := loadGlobalAgentsMD(); readErr == nil && prompt != "" {
			for _, a := range fileAgents {
				if strings.ToLower(a.ID) == "build" && a.Source == SourceBuiltin {
					a.Prompt = prompt
					break
				}
			}
		}
	}

	if err != nil {
		return fileAgents, err
	}
	return fileAgents, nil
}

// loadGlobalAgentsMD reads the AGENTS.md file from the OpenCode config root,
// which OpenCode uses as global instructions for the primary build agent.
func loadGlobalAgentsMD() (string, error) {
	d, err := config.ResolveDirs()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(d.Config, "AGENTS.md"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Watch watches the agents directory and global AGENTS.md for changes, calling
// onChange whenever a file is created, written, removed, or renamed. Events are
// debounced so that a single save triggers only one call. Watch blocks until ctx
// is cancelled.
func Watch(ctx context.Context, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	d, err := config.ResolveDirs()
	if err != nil {
		return fmt.Errorf("resolving dirs: %w", err)
	}

	// Watch the agents directory (catches new/edited/deleted agent .md files).
	agentsDir := filepath.Join(d.Config, "agents")
	if dirErr := watcher.Add(agentsDir); dirErr != nil && !os.IsNotExist(dirErr) {
		return fmt.Errorf("watching agents dir: %w", dirErr)
	}

	// Watch the config root directory to catch AGENTS.md changes (watching a
	// file directly breaks on atomic saves used by most editors).
	configDir := d.Config
	_ = watcher.Add(configDir)

	globalMD := filepath.Join(configDir, "AGENTS.md")

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
			// For the config root directory, only react to AGENTS.md changes.
			if filepath.Dir(event.Name) == configDir && event.Name != globalMD {
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
