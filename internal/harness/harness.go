// Package harness locates agent, skill and command definitions for each
// supported coding harness (OpenCode and Claude Code) across user, project and
// plugin scopes.
package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/thenickygee/mirage/internal/config"
)

const (
	OpenCode = "opencode"
	Claude   = "claude"

	ScopeUser    = "user"
	ScopeProject = "project"
	ScopePlugin  = "plugin"
)

// Kind names a definition type that lives in a per-harness subdirectory.
type Kind string

const (
	Agents   Kind = "agents"
	Skills   Kind = "skills"
	Commands Kind = "commands"
)

// Origin records which harness and scope a definition was loaded from.
type Origin struct {
	Harness string
	Scope   string
	Plugin  string // plugin name when Scope == ScopePlugin
}

// ReadOnly reports whether the definition lives somewhere mirage should not
// write back to (installed plugin caches).
func (o Origin) ReadOnly() bool { return o.Scope == ScopePlugin }

// Root is one directory that may contain definitions of a Kind.
type Root struct {
	Dir string
	Origin
}

// Roots returns every existing directory holding definitions of kind, ordered
// user, project, plugin. Within a scope OpenCode comes before Claude Code.
func Roots(kind Kind) []Root {
	var roots []Root
	seen := make(map[string]bool)
	add := func(dir string, o Origin) {
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return
		}
		seen[dir] = true
		roots = append(roots, Root{Dir: dir, Origin: o})
	}

	home, _ := os.UserHomeDir()
	if d, err := config.ResolveDirs(); err == nil {
		add(filepath.Join(d.Config, string(kind)), Origin{OpenCode, ScopeUser, ""})
	}
	if home != "" {
		add(filepath.Join(home, ".claude", string(kind)), Origin{Claude, ScopeUser, ""})
	}

	for _, p := range projectDirs(home) {
		for _, sub := range opencodeSubdirs(kind) {
			add(filepath.Join(p, ".opencode", sub), Origin{OpenCode, ScopeProject, ""})
		}
		add(filepath.Join(p, ".claude", string(kind)), Origin{Claude, ScopeProject, ""})
	}

	for _, pl := range installedPlugins(home) {
		add(filepath.Join(pl.path, string(kind)), Origin{Claude, ScopePlugin, pl.name})
	}
	return roots
}

// opencodeSubdirs lists the directory spellings OpenCode accepts inside a
// project's .opencode folder (singular and plural).
func opencodeSubdirs(kind Kind) []string {
	return []string{string(kind), strings.TrimSuffix(string(kind), "s")}
}

// projectDirs returns the working directory and its ancestors up to and
// including the enclosing git root (or just the working directory when not in
// a repo). The home directory is excluded since its .claude is the user scope.
func projectDirs(home string) []string {
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}
	root := gitRoot(wd)
	var dirs []string
	for dir := wd; ; dir = filepath.Dir(dir) {
		if dir != home {
			dirs = append(dirs, dir)
		}
		if dir == root || dir == filepath.Dir(dir) {
			break
		}
	}
	return dirs
}

func gitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			return dir
		}
	}
}

type plugin struct {
	name string
	path string
}

type installedPluginsFile struct {
	Plugins map[string][]struct {
		Scope       string `json:"scope"`
		InstallPath string `json:"installPath"`
		ProjectPath string `json:"projectPath"`
	} `json:"plugins"`
}

// installedPlugins reads Claude Code's plugin registry. Entries scoped to a
// project are only included when that project is the current one.
func installedPlugins(home string) []plugin {
	if home == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var f installedPluginsFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	wd, _ := os.Getwd()
	var out []plugin
	for key, installs := range f.Plugins {
		name, _, _ := strings.Cut(key, "@")
		for _, in := range installs {
			if in.ProjectPath != "" && !within(wd, in.ProjectPath) {
				continue
			}
			out = append(out, plugin{name: name, path: in.InstallPath})
			break
		}
	}
	return out
}

func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// WatchDirs watches each of dirs plus its subdirectories (to a shallow depth),
// and each of flat without descending, and calls onChange, debounced, when anything beneath them is written, created,
// removed or renamed. accept, when non-nil, filters events by path. It blocks
// until ctx is cancelled.
func WatchDirs(ctx context.Context, dirs, flat []string, accept func(path string) bool, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()

	for _, d := range dirs {
		addTree(watcher, d, 3)
	}
	for _, d := range flat {
		addTree(watcher, d, 0)
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
			if accept != nil && !accept(event.Name) {
				continue
			}
			if event.Has(fsnotify.Create) {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					addTree(watcher, event.Name, 2)
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

func addTree(w *fsnotify.Watcher, dir string, depth int) {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if w.Add(dir) != nil || depth == 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		sub := filepath.Join(dir, e.Name())
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			addTree(w, sub, depth-1)
		}
	}
}

// WatchRoots watches every root of the given kinds.
func WatchRoots(ctx context.Context, kind Kind, onChange func()) error {
	var dirs []string
	for _, r := range Roots(kind) {
		if !r.ReadOnly() {
			dirs = append(dirs, r.Dir)
		}
	}
	return WatchDirs(ctx, dirs, nil, nil, onChange)
}

// FlexString is a frontmatter value that Claude Code lets be either a scalar
// or a YAML list (allowed-tools, argument-hint); a list is joined with ", ".
type FlexString string

func (t *FlexString) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		var items []string
		if err := n.Decode(&items); err != nil {
			return err
		}
		*t = FlexString(strings.Join(items, ", "))
		return nil
	}
	var str string
	if err := n.Decode(&str); err != nil {
		return err
	}
	*t = FlexString(str)
	return nil
}
