package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/standup-kanban/standup-kanban/internal/model"
)

type planSection int

const (
	sectionWeekFocus planSection = iota
	sectionToday
	sectionInbox
	sectionScratch
	sectionCount // sentinel for wrapping
)

func sectionName(s planSection) string {
	switch s {
	case sectionWeekFocus:
		return "Week Focus"
	case sectionToday:
		return "Today"
	case sectionInbox:
		return "Inbox"
	case sectionScratch:
		return "Scratch"
	}
	return ""
}

type PlanViewModel struct {
	plan    *model.Plan
	inbox   *model.Inbox
	project *model.Project // for resolving issue status

	section    planSection
	cursorIdx  int
	scrollOffset int
	viewHeight int
}

func NewPlanViewModel(plan *model.Plan, inbox *model.Inbox, project *model.Project) PlanViewModel {
	return PlanViewModel{
		plan:    plan,
		inbox:   inbox,
		project: project,
	}
}

func (m *PlanViewModel) SetData(plan *model.Plan, inbox *model.Inbox, project *model.Project) {
	m.plan = plan
	m.inbox = inbox
	m.project = project
}

func (m *PlanViewModel) NextSection() {
	m.section = (m.section + 1) % sectionCount
	m.cursorIdx = 0
	m.scrollOffset = 0
}

func (m *PlanViewModel) PrevSection() {
	if m.section == 0 {
		m.section = sectionCount - 1
	} else {
		m.section--
	}
	m.cursorIdx = 0
	m.scrollOffset = 0
}

func (m *PlanViewModel) CursorDown() {
	max := m.sectionLen() - 1
	if m.cursorIdx < max {
		m.cursorIdx++
	}
}

func (m *PlanViewModel) CursorUp() {
	if m.cursorIdx > 0 {
		m.cursorIdx--
	}
}

func (m *PlanViewModel) sectionLen() int {
	switch m.section {
	case sectionWeekFocus:
		return len(m.plan.WeekFocus)
	case sectionToday:
		return len(m.plan.Today)
	case sectionInbox:
		if m.inbox != nil {
			return len(m.inbox.Items)
		}
		return 0
	case sectionScratch:
		return len(m.plan.Scratch)
	}
	return 0
}

func (m *PlanViewModel) resolveIssue(num int) *model.ProjectItem {
	if m.project == nil || num == 0 {
		return nil
	}
	for i := range m.project.Items {
		if m.project.Items[i].Number == num {
			return &m.project.Items[i]
		}
	}
	return nil
}

func (m PlanViewModel) View(width, height int) string {
	m.viewHeight = height - 6

	var sb strings.Builder

	// Section tabs
	var tabs []string
	for i := planSection(0); i < sectionCount; i++ {
		count := 0
		switch i {
		case sectionWeekFocus:
			count = len(m.plan.WeekFocus)
		case sectionToday:
			count = len(m.plan.Today)
		case sectionInbox:
			if m.inbox != nil {
				count = len(m.inbox.Items)
			}
		case sectionScratch:
			count = len(m.plan.Scratch)
		}
		label := fmt.Sprintf("%s (%d)", sectionName(i), count)
		if i == m.section {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(label))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	sb.WriteString("\n\n")

	switch m.section {
	case sectionWeekFocus:
		sb.WriteString(m.renderWeekFocus(width))
	case sectionToday:
		sb.WriteString(m.renderToday(width))
	case sectionInbox:
		sb.WriteString(m.renderInbox(width))
	case sectionScratch:
		sb.WriteString(m.renderScratch(width))
	}

	return sb.String()
}

func (m PlanViewModel) renderWeekFocus(width int) string {
	var sb strings.Builder
	if len(m.plan.WeekFocus) == 0 {
		sb.WriteString(helpStyle.Render("  No weekly focus items. Use :goal or :pin to add."))
		return sb.String()
	}
	for i, item := range m.plan.WeekFocus {
		cursor := "  "
		if i == m.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}
		lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
			Render(fmt.Sprintf("%d", i+1))

		if item.IssueNum > 0 {
			// Pinned issue — show live status
			num := issueNumStyle.Render(fmt.Sprintf("#%d", item.IssueNum))
			status := ""
			title := item.Text
			if pi := m.resolveIssue(item.IssueNum); pi != nil {
				title = pi.Title
				status = "  " + renderStatus(pi.Status)
			}
			sb.WriteString(fmt.Sprintf("%s%s %s %s%s\n", cursor, lineNo, num, issueTitleStyle.Render(title), status))
		} else {
			sb.WriteString(fmt.Sprintf("%s%s %s\n", cursor, lineNo, item.Text))
		}
	}
	return sb.String()
}

func (m PlanViewModel) renderToday(width int) string {
	var sb strings.Builder
	if len(m.plan.Today) == 0 {
		sb.WriteString(helpStyle.Render("  No tasks for today. Use :today to add."))
		return sb.String()
	}
	for i, item := range m.plan.Today {
		cursor := "  "
		if i == m.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}
		lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
			Render(fmt.Sprintf("%d", i+1))

		check := "[ ]"
		textStyle := issueTitleStyle
		if item.Done {
			check = "[x]"
			textStyle = lipgloss.NewStyle().Foreground(colorSuccess).Strikethrough(true)
		}

		text := item.Text
		if item.IssueNum > 0 {
			if pi := m.resolveIssue(item.IssueNum); pi != nil {
				text = fmt.Sprintf("#%d %s", item.IssueNum, pi.Title)
			}
		}

		sb.WriteString(fmt.Sprintf("%s%s %s %s\n", cursor, lineNo, check, textStyle.Render(text)))
	}
	return sb.String()
}

func (m PlanViewModel) renderInbox(width int) string {
	var sb strings.Builder
	if m.inbox == nil || len(m.inbox.Items) == 0 {
		sb.WriteString(helpStyle.Render("  Inbox empty. External processes can write to inbox.yaml."))
		return sb.String()
	}
	for i, item := range m.inbox.Items {
		cursor := "  "
		if i == m.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}
		lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
			Render(fmt.Sprintf("%d", i+1))

		age := ""
		if !item.CreatedAt.IsZero() {
			age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(item.CreatedAt)))
		}
		from := ""
		if item.From != "" {
			from = commentAuthorStyle.Render(item.From+": ")
		}

		sb.WriteString(fmt.Sprintf("%s%s %s%s%s\n", cursor, lineNo, from, item.Text, age))
	}
	return sb.String()
}

func (m PlanViewModel) renderScratch(width int) string {
	var sb strings.Builder
	if len(m.plan.Scratch) == 0 {
		sb.WriteString(helpStyle.Render("  No scratch notes. Use :scratch to jot something down."))
		return sb.String()
	}
	for i, note := range m.plan.Scratch {
		cursor := "  "
		if i == m.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}
		lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
			Render(fmt.Sprintf("%d", i+1))

		age := ""
		if !note.CreatedAt.IsZero() {
			age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(note.CreatedAt)))
		}

		_ = time.Now() // ensure time import
		sb.WriteString(fmt.Sprintf("%s%s %s%s\n", cursor, lineNo, note.Text, age))
	}
	return sb.String()
}
