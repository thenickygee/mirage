package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/thenickygee/mirage/internal/agent"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type formField int

const (
	fieldName formField = iota
	fieldDescription
	fieldModel
	fieldMode
	fieldColor
	fieldTemperature
	fieldTopP
	fieldSteps
	fieldHidden
	fieldDisable
	fieldPermissions
	fieldPrompt
	fieldCount
)

var fieldLabels = map[formField]string{
	fieldName:        "Name (ID)",
	fieldDescription: "Description",
	fieldModel:       "Model",
	fieldMode:        "Mode",
	fieldColor:       "Color",
	fieldTemperature: "Temperature",
	fieldTopP:        "Top P",
	fieldSteps:       "Steps",
	fieldHidden:      "Hidden (y/n)",
	fieldDisable:     "Disable (y/n)",
	fieldPermissions: "Permissions (key: value, one per line)",
	fieldPrompt:      "System Prompt",
}

type formModel struct {
	isNew    bool
	original *agent.Agent
	inputs   []textinput.Model
	prompt   textarea.Model
	perms    textarea.Model
	focused  formField
	width    int
	height   int
	err      string
	scrollY  int
}

func newForm(a *agent.Agent, isNew bool) formModel {
	inputs := make([]textinput.Model, int(fieldPermissions)) // indices 0–9, excludes perms and prompt textareas

	for i := range inputs {
		t := textinput.New()
		t.CharLimit = 256
		inputs[i] = t
	}

	prompt := textarea.New()
	prompt.CharLimit = 0
	prompt.SetWidth(60)
	prompt.SetHeight(8)
	prompt.ShowLineNumbers = false

	perms := textarea.New()
	perms.CharLimit = 0
	perms.SetWidth(60)
	perms.SetHeight(4)
	perms.ShowLineNumbers = false

	f := formModel{
		isNew:    isNew,
		original: a,
		inputs:   inputs,
		prompt:   prompt,
		perms:    perms,
	}

	if a != nil {
		inputs[fieldName].SetValue(a.ID)
		inputs[fieldDescription].SetValue(a.Description)
		inputs[fieldModel].SetValue(a.Model)
		inputs[fieldMode].SetValue(a.Mode)
		inputs[fieldColor].SetValue(a.Color)
		if a.Temperature != nil {
			inputs[fieldTemperature].SetValue(fmt.Sprintf("%.2f", *a.Temperature))
		}
		if a.TopP != nil {
			inputs[fieldTopP].SetValue(fmt.Sprintf("%.2f", *a.TopP))
		}
		if a.Steps != nil {
			inputs[fieldSteps].SetValue(fmt.Sprintf("%d", *a.Steps))
		}
		if a.Hidden {
			inputs[fieldHidden].SetValue("y")
		} else {
			inputs[fieldHidden].SetValue("n")
		}
		if a.Disable {
			inputs[fieldDisable].SetValue("y")
		} else {
			inputs[fieldDisable].SetValue("n")
		}

		// Permissions
		var permLines []string
		for k, v := range a.Permission {
			permLines = append(permLines, k+": "+fmt.Sprintf("%v", v))
		}
		perms.SetValue(strings.Join(permLines, "\n"))

		prompt.SetValue(a.Prompt)
	} else {
		inputs[fieldMode].SetValue("subagent")
		inputs[fieldHidden].SetValue("n")
		inputs[fieldDisable].SetValue("n")
	}

	f.inputs = inputs
	f.prompt = prompt
	f.perms = perms

	// Focus first field
	f.focusField(0)
	return f
}

func (f *formModel) focusField(idx formField) {
	f.focused = idx
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	f.prompt.Blur()
	f.perms.Blur()

	switch idx {
	case fieldPrompt:
		f.prompt.Focus()
	case fieldPermissions:
		f.perms.Focus()
	default:
		if int(idx) < len(f.inputs) {
			f.inputs[idx].Focus()
		}
	}
}

func (f *formModel) nextField() {
	next := f.focused + 1
	if !f.isNew && next == fieldName {
		next++
	}
	if next >= fieldCount {
		next = 0
		if !f.isNew {
			next = fieldDescription
		}
	}
	f.focusField(next)
}

func (f *formModel) prevField() {
	prev := f.focused - 1
	if !f.isNew && prev == fieldName {
		prev--
	}
	minField := formField(0)
	if !f.isNew {
		minField = fieldDescription
	}
	if prev < minField {
		prev = fieldCount - 1
	}
	f.focusField(prev)
}

func (f formModel) Update(msg tea.Msg) (formModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, Keys.Tab):
			f.nextField()
			return f, nil
		case key.Matches(msg, Keys.ShiftTab):
			f.prevField()
			return f, nil
		}
	}

	// Update active field
	switch f.focused {
	case fieldPrompt:
		var cmd tea.Cmd
		f.prompt, cmd = f.prompt.Update(msg)
		cmds = append(cmds, cmd)
	case fieldPermissions:
		var cmd tea.Cmd
		f.perms, cmd = f.perms.Update(msg)
		cmds = append(cmds, cmd)
	default:
		if int(f.focused) < len(f.inputs) {
			var cmd tea.Cmd
			f.inputs[f.focused], cmd = f.inputs[f.focused].Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return f, tea.Batch(cmds...)
}

func (f *formModel) build() (*agent.Agent, error) {
	name := strings.TrimSpace(f.inputs[fieldName].Value())
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, " /\\") {
		return nil, fmt.Errorf("name must not contain spaces or slashes")
	}

	var temp *float64
	if v := strings.TrimSpace(f.inputs[fieldTemperature].Value()); v != "" {
		t, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("temperature must be a number")
		}
		temp = &t
	}
	var topP *float64
	if v := strings.TrimSpace(f.inputs[fieldTopP].Value()); v != "" {
		t, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("top_p must be a number")
		}
		topP = &t
	}
	var steps *int
	if v := strings.TrimSpace(f.inputs[fieldSteps].Value()); v != "" {
		s, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("steps must be an integer")
		}
		steps = &s
	}

	hidden := strings.ToLower(strings.TrimSpace(f.inputs[fieldHidden].Value())) == "y"
	disable := strings.ToLower(strings.TrimSpace(f.inputs[fieldDisable].Value())) == "y"

	perms := parsePermissions(f.perms.Value())

	var path string
	if f.original != nil {
		path = f.original.Path
	}

	a := &agent.Agent{
		ID:          name,
		Path:        path,
		Description: strings.TrimSpace(f.inputs[fieldDescription].Value()),
		Model:       strings.TrimSpace(f.inputs[fieldModel].Value()),
		Mode:        strings.TrimSpace(f.inputs[fieldMode].Value()),
		Color:       strings.TrimSpace(f.inputs[fieldColor].Value()),
		Temperature: temp,
		TopP:        topP,
		Steps:       steps,
		Hidden:      hidden,
		Disable:     disable,
		Permission:  perms,
		Prompt:      strings.TrimSpace(f.prompt.Value()),
	}

	if f.isNew {
		newAgent, err := agent.New(name)
		if err != nil {
			return nil, fmt.Errorf("resolving agent path: %w", err)
		}
		a.Path = newAgent.Path
	}

	return a, nil
}

func parsePermissions(s string) agent.Permission {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	result := agent.Permission{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (f formModel) View() string {
	title := "Edit Agent"
	if f.isNew {
		title = "New Agent"
	}

	var sb strings.Builder
	sb.WriteString(StyleFormTitle.Render(title) + "\n\n")

	if f.err != "" {
		sb.WriteString(StyleBadgeDisabled.Render(" ✗ "+f.err+" ") + "\n\n")
	}

	renderInput := func(field formField, label string) string {
		active := f.focused == field
		lbl := StyleLabel.Render(label + ":")
		var inp string
		if active {
			inp = StyleActiveInput.Render(f.inputs[field].View())
		} else {
			inp = StyleInactiveInput.Render(f.inputs[field].View())
		}
		return lbl + "\n" + inp + "\n\n"
	}

	renderTextarea := func(field formField, label string, ta textarea.Model) string {
		active := f.focused == field
		lbl := StyleLabel.Render(label + ":")
		var box string
		if active {
			box = StyleActiveInput.Render(ta.View())
		} else {
			box = StyleInactiveInput.Render(ta.View())
		}
		return lbl + "\n" + box + "\n\n"
	}

	if f.isNew {
		sb.WriteString(renderInput(fieldName, fieldLabels[fieldName]))
	}
	sb.WriteString(renderInput(fieldDescription, fieldLabels[fieldDescription]))
	sb.WriteString(renderInput(fieldModel, fieldLabels[fieldModel]))
	sb.WriteString(renderInput(fieldMode, fieldLabels[fieldMode]))
	sb.WriteString(renderInput(fieldColor, fieldLabels[fieldColor]))
	sb.WriteString(renderInput(fieldTemperature, fieldLabels[fieldTemperature]))
	sb.WriteString(renderInput(fieldTopP, fieldLabels[fieldTopP]))
	sb.WriteString(renderInput(fieldSteps, fieldLabels[fieldSteps]))
	sb.WriteString(renderInput(fieldHidden, fieldLabels[fieldHidden]))
	sb.WriteString(renderInput(fieldDisable, fieldLabels[fieldDisable]))
	sb.WriteString(renderTextarea(fieldPermissions, fieldLabels[fieldPermissions], f.perms))
	sb.WriteString(renderTextarea(fieldPrompt, fieldLabels[fieldPrompt], f.prompt))

	helpKeys := []struct{ k, d string }{
		{"tab", "next"},
		{"shift+tab", "prev"},
		{"ctrl+s", "save"},
		{"esc", "cancel"},
	}
	var helpParts []string
	for _, h := range helpKeys {
		helpParts = append(helpParts, StyleStatusKey.Render(h.k)+" "+StyleMuted.Render(h.d))
	}
	sb.WriteString(strings.Join(helpParts, "  "))

	return sb.String()
}
