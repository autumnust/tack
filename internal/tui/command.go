package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type CommandModel struct {
	input  textinput.Model
	active bool
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
	return CommandModel{input: ti}
}

func (m *CommandModel) Activate() {
	m.active = true
	m.input.SetValue("")
	m.input.Focus()
}

func (m *CommandModel) Deactivate() {
	m.active = false
	m.input.Blur()
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
			result := parseCommand(m.input.Value())
			m.Deactivate()
			return result, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return nil, cmd
}

func (m CommandModel) View() string {
	if m.active {
		return commandBarStyle.Render(m.input.View())
	}
	return ""
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
