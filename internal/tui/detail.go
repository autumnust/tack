package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/standup-kanban/standup-kanban/internal/model"
)

type DetailModel struct {
	issue    *model.ProjectItem
	viewport viewport.Model
	ready    bool
}

func NewDetailModel(issue *model.ProjectItem, width, height int) DetailModel {
	vp := viewport.New(width, height-4)
	vp.SetContent(renderDetail(issue, width))
	return DetailModel{
		issue:    issue,
		viewport: vp,
		ready:    true,
	}
}

func (m *DetailModel) SetSize(width, height int) {
	m.viewport.Width = width
	m.viewport.Height = height - 4
	if m.issue != nil {
		m.viewport.SetContent(renderDetail(m.issue, width))
	}
}

func (m DetailModel) View(width int) string {
	var sb strings.Builder

	// Header
	header := fmt.Sprintf("#%d %s", m.issue.Number, m.issue.Title)
	sb.WriteString(detailHeaderStyle.Render(header))
	sb.WriteString("\n")

	// Meta line
	meta := []string{
		fmt.Sprintf("Status: %s", renderStatus(m.issue.Status)),
		fmt.Sprintf("Repo: %s", m.issue.Repo),
	}
	if len(m.issue.Assignees) > 0 {
		meta = append(meta, fmt.Sprintf("Assignees: %s", strings.Join(m.issue.Assignees, ", ")))
	}
	if len(m.issue.Labels) > 0 {
		meta = append(meta, fmt.Sprintf("Labels: %s", strings.Join(m.issue.Labels, ", ")))
	}
	if m.issue.Parent != nil {
		meta = append(meta, fmt.Sprintf("Parent: #%d %s", m.issue.Parent.Number, m.issue.Parent.Title))
	}
	sb.WriteString(detailMetaStyle.Render(strings.Join(meta, "  │  ")))
	sb.WriteString("\n")

	separator := lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", min(width-4, 80)))
	sb.WriteString(separator)
	sb.WriteString("\n")

	// Viewport with rendered content
	sb.WriteString(m.viewport.View())

	return sb.String()
}

func renderDetail(issue *model.ProjectItem, width int) string {
	var sb strings.Builder

	// Body (markdown rendered)
	if issue.Body != "" {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(min(width-4, 100)),
		)
		if err == nil {
			rendered, err := renderer.Render(issue.Body)
			if err == nil {
				sb.WriteString(rendered)
			} else {
				sb.WriteString(issue.Body)
			}
		} else {
			sb.WriteString(issue.Body)
		}
	} else {
		sb.WriteString(helpStyle.Render("  (no description)"))
	}

	// Comments
	if len(issue.Comments) > 0 {
		sb.WriteString("\n")
		commentHeader := lipgloss.NewStyle().Bold(true).Foreground(colorSecondary).
			Render(fmt.Sprintf("── Comments (%d) ", len(issue.Comments)))
		sb.WriteString(commentHeader)
		sb.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", 40)))
		sb.WriteString("\n\n")

		for _, c := range issue.Comments {
			age := timeAgo(c.CreatedAt)
			author := commentAuthorStyle.Render(c.Author)
			ts := commentTimeStyle.Render(fmt.Sprintf("(%s)", age))
			sb.WriteString(fmt.Sprintf("  %s %s\n", author, ts))

			// Render comment body as markdown
			renderer, err := glamour.NewTermRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(min(width-8, 96)),
			)
			if err == nil {
				rendered, err := renderer.Render(c.Body)
				if err == nil {
					// Indent
					for _, line := range strings.Split(rendered, "\n") {
						sb.WriteString("    " + line + "\n")
					}
				} else {
					sb.WriteString("    " + c.Body + "\n")
				}
			} else {
				sb.WriteString("    " + c.Body + "\n")
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}
