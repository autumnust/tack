package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type CommandModel struct {
	input       textinput.Model
	active      bool
	completions []string // current matching completions
	compIdx     int      // selected completion index (-1 = none)
	allNames    []string // all completable names (set externally)
}

type CommandResult struct {
	Action string
	Args   []string
	Raw    string
}

func NewCommandModel() CommandModel {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.CharLimit = 256
	return CommandModel{input: ti, compIdx: -1}
}

func (m *CommandModel) SetCompletionNames(names []string) {
	m.allNames = names
}

func (m *CommandModel) Activate() {
	m.active = true
	m.input.SetValue("")
	m.input.Focus()
	m.completions = nil
	m.compIdx = -1
}

func (m *CommandModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.completions = nil
	m.compIdx = -1
}

func (m *CommandModel) IsActive() bool {
	return m.active
}

func (m *CommandModel) Update(msg tea.Msg) (*CommandResult, tea.Cmd) {
	if !m.active {
		return nil, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape:
			m.Deactivate()
			return nil, nil
		case tea.KeyEnter:
			// If completion is active, accept it first
			if m.compIdx >= 0 && m.compIdx < len(m.completions) {
				m.acceptCompletion()
				return nil, nil
			}
			result := parseCommand(m.input.Value())
			m.Deactivate()
			return result, nil
		case tea.KeyTab:
			if len(m.completions) > 0 {
				// Cycle forward
				m.compIdx = (m.compIdx + 1) % len(m.completions)
				return nil, nil
			}
		case tea.KeyShiftTab:
			if len(m.completions) > 0 {
				// Cycle backward
				m.compIdx--
				if m.compIdx < 0 {
					m.compIdx = len(m.completions) - 1
				}
				return nil, nil
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	// Update completions based on current input
	m.updateCompletions()

	return nil, cmd
}

func (m *CommandModel) updateCompletions() {
	val := m.input.Value()

	// Find the last @ token being typed
	atIdx := strings.LastIndex(val, "@")
	if atIdx < 0 {
		m.completions = nil
		m.compIdx = -1
		return
	}

	// Extract the partial name after @
	partial := strings.ToLower(val[atIdx+1:])
	// Don't complete if there's a space after the partial (user moved on)
	if strings.Contains(partial, " ") {
		m.completions = nil
		m.compIdx = -1
		return
	}

	var matches []string
	for _, name := range m.allNames {
		if partial == "" || strings.HasPrefix(strings.ToLower(name), partial) {
			matches = append(matches, name)
		}
	}

	m.completions = matches
	if len(matches) == 0 {
		m.compIdx = -1
	} else if m.compIdx >= len(matches) {
		m.compIdx = 0
	}
}

func (m *CommandModel) acceptCompletion() {
	if m.compIdx < 0 || m.compIdx >= len(m.completions) {
		return
	}
	selected := m.completions[m.compIdx]

	val := m.input.Value()
	atIdx := strings.LastIndex(val, "@")
	if atIdx < 0 {
		return
	}

	// Replace from @ to end with the selected name
	newVal := val[:atIdx+1] + selected + " "
	m.input.SetValue(newVal)
	m.input.SetCursor(len(newVal))
	m.completions = nil
	m.compIdx = -1
}

func (m CommandModel) View() string {
	if !m.active {
		return ""
	}

	inputLine := m.input.View()

	if len(m.completions) > 0 {
		// Render completions inline
		var compParts []string
		for i, c := range m.completions {
			if i == m.compIdx {
				compParts = append(compParts,
					lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render(c))
			} else {
				compParts = append(compParts,
					lipgloss.NewStyle().Foreground(colorMuted).Render(c))
			}
		}
		hint := "  " + strings.Join(compParts, "  ")
		return commandBarStyle.Render(inputLine + "\n" + hint)
	}

	return commandBarStyle.Render(inputLine)
}

func parseCommand(raw string) *CommandResult {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := tokenize(raw)
	if len(parts) == 0 {
		return nil
	}

	return &CommandResult{
		Action: strings.ToLower(parts[0]),
		Args:   parts[1:],
		Raw:    raw,
	}
}

// tokenize splits command input, respecting quoted strings.
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
		} else if ch == '"' || ch == '\'' {
			inQuote = true
			quoteChar = ch
		} else if ch == ' ' || ch == '\t' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func commandHelp() string {
	return fmt.Sprintf("%s  %s",
		helpStyle.Render(":mv #N <status>"),
		helpStyle.Render(":c \"comment\" | :open | Tab/j/k/Enter/Esc | r=refresh q=quit"),
	)
}
