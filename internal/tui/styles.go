package tui

import (
	"time"

	"github.com/charmbracelet/lipgloss"
)

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

	// upstashAgeSchemes shade upstash (hibana-graduated) board rows by how
	// long they've been sitting without a GH backing. Four buckets:
	//   0: <1d (fresh)   1: 1-3d   2: 3-7d   3: 7d+ (stale)
	// Schemes are user-selectable on the board via "C".
	upstashAgeSchemes = []upstashAgeScheme{
		{
			name:    "heat",
			summary: "calm → alarming (green → red)",
			levels:  [4]lipgloss.Color{"#10B981", "#F59E0B", "#F97316", "#EF4444"},
		},
		{
			name:    "ocean",
			summary: "cool gradient (cyan → violet)",
			levels:  [4]lipgloss.Color{"#67E8F9", "#38BDF8", "#818CF8", "#C084FC"},
		},
		{
			name:    "rose",
			summary: "pink intensifies (soft → crimson)",
			levels:  [4]lipgloss.Color{"#FBCFE8", "#F472B6", "#EC4899", "#BE185D"},
		},
		{
			name:    "mono",
			summary: "grayscale (dim → bright)",
			levels:  [4]lipgloss.Color{"#4B5563", "#9CA3AF", "#D1D5DB", "#F9FAFB"},
		},
	}
	upstashAgeSchemeIdx = 0

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

type upstashAgeScheme struct {
	name    string
	summary string
	levels  [4]lipgloss.Color
}

func upstashAgeBucket(created time.Time) int {
	if created.IsZero() {
		return 0
	}
	age := time.Since(created)
	switch {
	case age < 24*time.Hour:
		return 0
	case age < 3*24*time.Hour:
		return 1
	case age < 7*24*time.Hour:
		return 2
	default:
		return 3
	}
}

func upstashAgeColor(created time.Time) lipgloss.Color {
	return upstashAgeSchemes[upstashAgeSchemeIdx].levels[upstashAgeBucket(created)]
}

func upstashAgeStyle(created time.Time) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(upstashAgeColor(created))
}

// cycleUpstashAgeScheme advances the active color scheme and returns the
// new scheme so the caller can surface its name to the user.
func cycleUpstashAgeScheme() upstashAgeScheme {
	upstashAgeSchemeIdx = (upstashAgeSchemeIdx + 1) % len(upstashAgeSchemes)
	return upstashAgeSchemes[upstashAgeSchemeIdx]
}

func currentUpstashAgeScheme() upstashAgeScheme {
	return upstashAgeSchemes[upstashAgeSchemeIdx]
}
