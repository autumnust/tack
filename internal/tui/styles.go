package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#7C3AED") // purple
	colorSecondary = lipgloss.Color("#06B6D4") // cyan
	colorMuted     = lipgloss.Color("#6B7280") // gray
	colorSuccess   = lipgloss.Color("#10B981") // green
	colorWarning   = lipgloss.Color("#F59E0B") // amber
	colorDanger    = lipgloss.Color("#EF4444") // red
	colorBg        = lipgloss.Color("#1F2937") // dark bg
	colorHighlight = lipgloss.Color("#374151") // highlight bg

	// Tab bar
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 2).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(colorPrimary)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 2)

	// Board items
	cursorStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	epicStyle = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	issueNumStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	issueTitleStyle = lipgloss.NewStyle()

	// Status badges
	statusStyles = map[string]lipgloss.Style{
		"Todo":        lipgloss.NewStyle().Foreground(colorMuted),
		"In Progress": lipgloss.NewStyle().Foreground(colorWarning),
		"In Review":   lipgloss.NewStyle().Foreground(colorSecondary),
		"Done":        lipgloss.NewStyle().Foreground(colorSuccess),
	}

	// Command bar
	commandBarStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(colorMuted).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	// Detail view
	detailHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				Padding(0, 1)

	detailMetaStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	commentAuthorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorSecondary)

	commentTimeStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	// Borders and containers
	boardStyle = lipgloss.NewStyle().
			Padding(1, 2)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)
)

func statusStyle(status string) lipgloss.Style {
	if s, ok := statusStyles[status]; ok {
		return s
	}
	return lipgloss.NewStyle().Foreground(colorMuted)
}
