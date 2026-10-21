package harness_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/command"
	"github.com/thenickygee/mirage/internal/harness"
	"github.com/thenickygee/mirage/internal/skill"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (home, project string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	project = filepath.Join(home, "work", "proj")
	write(t, filepath.Join(project, ".git", "HEAD"), "")
	t.Chdir(filepath.Join(project))

	plug := filepath.Join(home, ".claude", "plugins", "cache", "mk", "acme", "1.0.0")
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"acme@mk":[{"scope":"user","installPath":"`+plug+`"}]}}`)

	write(t, filepath.Join(home, ".claude", "agents", "reviewer.md"),
		"---\nname: reviewer\ndescription: reviews\ntools: Read, Grep\nmodel: sonnet\n---\nBe careful.\n")
	write(t, filepath.Join(project, ".claude", "agents", "local.md"), "---\nname: local\ndescription: proj\n---\nhi\n")
	write(t, filepath.Join(plug, "agents", "helper.md"), "---\nname: helper\ndescription: plug\n---\nhi\n")

	write(t, filepath.Join(home, ".claude", "skills", "tidy", "SKILL.md"),
		"---\nname: tidy\ndescription: tidies\nallowed-tools:\n  - Read\n  - Edit\ndisable-model-invocation: true\n---\nbody\n")
	write(t, filepath.Join(home, ".claude", "skills", "empty", "notes.txt"), "no skill file")
	write(t, filepath.Join(project, ".opencode", "skill", "oc", "SKILL.md"), "---\nname: oc\ndescription: d\n---\nx\n")

	write(t, filepath.Join(home, ".claude", "commands", "git", "sync.md"), "---\ndescription: sync\nargument-hint: [branch]\n---\ndo it\n")
	write(t, filepath.Join(plug, "commands", "go.md"), "---\ndescription: go\n---\ngo\n")
	return home, project
}

func TestAgentsAcrossHarnessesAndScopes(t *testing.T) {
	setup(t)
	agents, err := agent.LoadAll()
	_ = err // LoadFromCLI may fail without opencode installed; file agents still load
	got := map[string]harness.Origin{}
	for _, a := range agents {
		got[a.ID] = a.Origin
	}
	want := map[string]harness.Origin{
		"reviewer":    {Harness: harness.Claude, Scope: harness.ScopeUser},
		"local":       {Harness: harness.Claude, Scope: harness.ScopeProject},
		"acme:helper": {Harness: harness.Claude, Scope: harness.ScopePlugin, Plugin: "acme"},
	}
	for id, o := range want {
		if got[id] != o {
			t.Errorf("agent %q origin = %+v, want %+v", id, got[id], o)
		}
	}
}

func TestClaudeAgentSavePreservesExtraFrontmatter(t *testing.T) {
	home, _ := setup(t)
	path := filepath.Join(home, ".claude", "agents", "reviewer.md")
	a, err := agent.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a.Description = "reviews more"
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := agent.Load(path)
	if b.Name != "reviewer" || b.Extra["tools"] != "Read, Grep" || b.Description != "reviews more" || b.Prompt != "Be careful." {
		t.Errorf("round trip lost data: %+v", b)
	}
}

func TestSkills(t *testing.T) {
	setup(t)
	skills, err := skill.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*skill.Skill{}
	for _, s := range skills {
		byID[s.ID] = s
	}
	if s := byID["tidy"]; s == nil || s.AllowedTools != "Read, Edit" || s.Extra["disable-model-invocation"] != true {
		t.Errorf("claude skill not parsed: %+v", s)
	}
	if s := byID["oc"]; s == nil || s.Origin.Harness != harness.OpenCode || s.Origin.Scope != harness.ScopeProject {
		t.Errorf("project opencode skill missing: %+v", s)
	}
	if _, ok := byID["empty"]; ok {
		t.Error("directory without SKILL.md should be skipped")
	}
}

func TestCommandsNamespaced(t *testing.T) {
	setup(t)
	commands, err := command.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range commands {
		ids[c.ID] = true
	}
	for _, id := range []string{"git:sync", "acme:go"} {
		if !ids[id] {
			t.Errorf("missing command %q in %v", id, ids)
		}
	}
}
