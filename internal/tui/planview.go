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
	sectionCount
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

// flatItem represents a single navigable row in the plan view.
type flatItem struct {
	section  planSection
	focusIdx int // index into WeekFocus (for sub-items, the parent)
	subIdx   int // -1 if this is a top-level item, >=0 if sub-item
}

type PlanViewModel struct {
	plan    *model.Plan
	inbox   *model.Inbox
	project *model.Project

	section      planSection
	flatItems    []flatItem
	cursorIdx    int
	scrollOffset int
	viewHeight   int
}

func NewPlanViewModel(plan *model.Plan, inbox *model.Inbox, project *model.Project) PlanViewModel {
	m := PlanViewModel{
		plan:    plan,
		inbox:   inbox,
		project: project,
	}
	m.rebuildFlat()
	return m
}

func (m *PlanViewModel) SetData(plan *model.Plan, inbox *model.Inbox, project *model.Project) {
	m.plan = plan
	m.inbox = inbox
	m.project = project
	m.rebuildFlat()
}

func (m *PlanViewModel) rebuildFlat() {
	m.flatItems = nil
	switch m.section {
	case sectionWeekFocus:
		for i, f := range m.plan.WeekFocus {
			m.flatItems = append(m.flatItems, flatItem{section: sectionWeekFocus, focusIdx: i, subIdx: -1})
			for si := range f.SubItems {
				m.flatItems = append(m.flatItems, flatItem{section: sectionWeekFocus, focusIdx: i, subIdx: si})
			}
		}
	case sectionToday:
		for i := range m.plan.Today {
			m.flatItems = append(m.flatItems, flatItem{section: sectionToday, focusIdx: i, subIdx: -1})
		}
	case sectionInbox:
		if m.inbox != nil {
			for i := range m.inbox.Items {
				m.flatItems = append(m.flatItems, flatItem{section: sectionInbox, focusIdx: i, subIdx: -1})
			}
		}
	case sectionScratch:
		for i := range m.plan.Scratch {
			m.flatItems = append(m.flatItems, flatItem{section: sectionScratch, focusIdx: i, subIdx: -1})
		}
	}
	if m.cursorIdx >= len(m.flatItems) {
		m.cursorIdx = len(m.flatItems) - 1
	}
	if m.cursorIdx < 0 {
		m.cursorIdx = 0
	}
}

func (m *PlanViewModel) NextSection() {
	m.section = (m.section + 1) % sectionCount
	m.cursorIdx = 0
	m.scrollOffset = 0
	m.rebuildFlat()
}

func (m *PlanViewModel) PrevSection() {
	if m.section == 0 {
		m.section = sectionCount - 1
	} else {
		m.section--
	}
	m.cursorIdx = 0
	m.scrollOffset = 0
	m.rebuildFlat()
}

func (m *PlanViewModel) CursorDown() {
	if m.cursorIdx < len(m.flatItems)-1 {
		m.cursorIdx++
	}
}

func (m *PlanViewModel) CursorUp() {
	if m.cursorIdx > 0 {
		m.cursorIdx--
	}
}

// MoveUp swaps the current top-level item with the one above.
func (m *PlanViewModel) MoveUp() bool {
	fi := m.currentFlat()
	if fi == nil {
		return false
	}
	switch m.section {
	case sectionWeekFocus:
		if fi.subIdx >= 0 {
			// Move sub-item up within parent
			parent := &m.plan.WeekFocus[fi.focusIdx]
			if fi.subIdx > 0 {
				parent.SubItems[fi.subIdx], parent.SubItems[fi.subIdx-1] = parent.SubItems[fi.subIdx-1], parent.SubItems[fi.subIdx]
				m.cursorIdx--
				m.rebuildFlat()
				return true
			}
		} else if fi.focusIdx > 0 {
			m.plan.WeekFocus[fi.focusIdx], m.plan.WeekFocus[fi.focusIdx-1] = m.plan.WeekFocus[fi.focusIdx-1], m.plan.WeekFocus[fi.focusIdx]
			m.rebuildFlat()
			// Move cursor to new position
			m.cursorIdx = 0
			for i, f := range m.flatItems {
				if f.focusIdx == fi.focusIdx-1 && f.subIdx == -1 {
					m.cursorIdx = i
					break
				}
			}
			return true
		}
	case sectionToday:
		if fi.focusIdx > 0 {
			m.plan.Today[fi.focusIdx], m.plan.Today[fi.focusIdx-1] = m.plan.Today[fi.focusIdx-1], m.plan.Today[fi.focusIdx]
			m.cursorIdx--
			m.rebuildFlat()
			return true
		}
	case sectionScratch:
		if fi.focusIdx > 0 {
			m.plan.Scratch[fi.focusIdx], m.plan.Scratch[fi.focusIdx-1] = m.plan.Scratch[fi.focusIdx-1], m.plan.Scratch[fi.focusIdx]
			m.cursorIdx--
			m.rebuildFlat()
			return true
		}
	}
	return false
}

// MoveDown swaps the current top-level item with the one below.
func (m *PlanViewModel) MoveDown() bool {
	fi := m.currentFlat()
	if fi == nil {
		return false
	}
	switch m.section {
	case sectionWeekFocus:
		if fi.subIdx >= 0 {
			parent := &m.plan.WeekFocus[fi.focusIdx]
			if fi.subIdx < len(parent.SubItems)-1 {
				parent.SubItems[fi.subIdx], parent.SubItems[fi.subIdx+1] = parent.SubItems[fi.subIdx+1], parent.SubItems[fi.subIdx]
				m.cursorIdx++
				m.rebuildFlat()
				return true
			}
		} else if fi.focusIdx < len(m.plan.WeekFocus)-1 {
			m.plan.WeekFocus[fi.focusIdx], m.plan.WeekFocus[fi.focusIdx+1] = m.plan.WeekFocus[fi.focusIdx+1], m.plan.WeekFocus[fi.focusIdx]
			m.rebuildFlat()
			m.cursorIdx = 0
			for i, f := range m.flatItems {
				if f.focusIdx == fi.focusIdx+1 && f.subIdx == -1 {
					m.cursorIdx = i
					break
				}
			}
			return true
		}
	case sectionToday:
		if fi.focusIdx < len(m.plan.Today)-1 {
			m.plan.Today[fi.focusIdx], m.plan.Today[fi.focusIdx+1] = m.plan.Today[fi.focusIdx+1], m.plan.Today[fi.focusIdx]
			m.cursorIdx++
			m.rebuildFlat()
			return true
		}
	case sectionScratch:
		if fi.focusIdx < len(m.plan.Scratch)-1 {
			m.plan.Scratch[fi.focusIdx], m.plan.Scratch[fi.focusIdx+1] = m.plan.Scratch[fi.focusIdx+1], m.plan.Scratch[fi.focusIdx]
			m.cursorIdx++
			m.rebuildFlat()
			return true
		}
	}
	return false
}

// ToggleDone handles Enter — toggles done on Today items or breakdown sub-items.
func (m *PlanViewModel) ToggleDone() (string, bool) {
	fi := m.currentFlat()
	if fi == nil {
		return "", false
	}
	switch m.section {
	case sectionWeekFocus:
		if fi.subIdx >= 0 {
			sub := &m.plan.WeekFocus[fi.focusIdx].SubItems[fi.subIdx]
			sub.Done = !sub.Done
			m.rebuildFlat()
			if sub.Done {
				return fmt.Sprintf("Completed: %s", sub.Text), true
			}
			return fmt.Sprintf("Uncompleted: %s", sub.Text), true
		}
	case sectionToday:
		item := &m.plan.Today[fi.focusIdx]
		item.Done = !item.Done
		if item.Done {
			item.DoneAt = time.Now()
		} else {
			item.DoneAt = time.Time{}
		}
		m.rebuildFlat()
		if item.Done {
			return fmt.Sprintf("Completed: %s", item.Text), true
		}
		return fmt.Sprintf("Uncompleted: %s", item.Text), true
	}
	return "", false
}

// PromoteToCurrent returns the sub-item text if cursor is on a breakdown item.
func (m *PlanViewModel) PromoteItem() (*model.SubItem, int, bool) {
	fi := m.currentFlat()
	if fi == nil || m.section != sectionWeekFocus || fi.subIdx < 0 {
		return nil, 0, false
	}
	sub := &m.plan.WeekFocus[fi.focusIdx].SubItems[fi.subIdx]
	return sub, fi.focusIdx, true
}

func (m *PlanViewModel) currentFlat() *flatItem {
	if m.cursorIdx < 0 || m.cursorIdx >= len(m.flatItems) {
		return nil
	}
	return &m.flatItems[m.cursorIdx]
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

	if len(m.flatItems) == 0 {
		hints := map[planSection]string{
			sectionWeekFocus: "  No weekly focus items. Use :goal or :pin to add.",
			sectionToday:     "  No tasks for today. Use :today to add.",
			sectionInbox:     "  Inbox empty. External processes can write to inbox.yaml.",
			sectionScratch:   "  No scratch notes. Use :scratch to jot something down.",
		}
		sb.WriteString(helpStyle.Render(hints[m.section]))
		return sb.String()
	}

	for i, fi := range m.flatItems {
		cursor := "  "
		if i == m.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}

		switch m.section {
		case sectionWeekFocus:
			if fi.subIdx == -1 {
				sb.WriteString(m.renderFocusItem(cursor, fi.focusIdx))
			} else {
				sb.WriteString(m.renderSubItem(cursor, fi.focusIdx, fi.subIdx))
			}
		case sectionToday:
			sb.WriteString(m.renderTodoItem(cursor, fi.focusIdx))
		case sectionInbox:
			sb.WriteString(m.renderInboxItem(cursor, fi.focusIdx))
		case sectionScratch:
			sb.WriteString(m.renderScratchItem(cursor, fi.focusIdx))
		}
	}

	return sb.String()
}

func (m PlanViewModel) renderFocusItem(cursor string, idx int) string {
	item := m.plan.WeekFocus[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	if item.IssueNum > 0 {
		num := issueNumStyle.Render(fmt.Sprintf("#%d", item.IssueNum))
		status := ""
		title := item.Text
		if pi := m.resolveIssue(item.IssueNum); pi != nil {
			title = pi.Title
			status = "  " + renderStatus(pi.Status)
		}
		subCount := ""
		if len(item.SubItems) > 0 {
			done := 0
			for _, s := range item.SubItems {
				if s.Done {
					done++
				}
			}
			subCount = helpStyle.Render(fmt.Sprintf("  [%d/%d]", done, len(item.SubItems)))
		}
		return fmt.Sprintf("%s%s %s %s%s%s\n", cursor, lineNo, num, issueTitleStyle.Render(title), status, subCount)
	}

	subCount := ""
	if len(item.SubItems) > 0 {
		done := 0
		for _, s := range item.SubItems {
			if s.Done {
				done++
			}
		}
		subCount = helpStyle.Render(fmt.Sprintf("  [%d/%d]", done, len(item.SubItems)))
	}
	return fmt.Sprintf("%s%s %s%s\n", cursor, lineNo, item.Text, subCount)
}

func (m PlanViewModel) renderSubItem(cursor string, focusIdx, subIdx int) string {
	sub := m.plan.WeekFocus[focusIdx].SubItems[subIdx]
	indent := "      " // indent under parent

	check := "[ ]"
	textStyle := issueTitleStyle
	if sub.Done {
		check = "[x]"
		textStyle = lipgloss.NewStyle().Foreground(colorSuccess)
	}

	text := sub.Text
	if sub.IssueNum > 0 {
		if pi := m.resolveIssue(sub.IssueNum); pi != nil {
			text = fmt.Sprintf("#%d %s", sub.IssueNum, pi.Title)
		}
	}

	return fmt.Sprintf("%s%s%s %s\n", cursor, indent, check, textStyle.Render(text))
}

func (m PlanViewModel) renderTodoItem(cursor string, idx int) string {
	item := m.plan.Today[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	check := "[ ]"
	textStyle := issueTitleStyle
	overdueTag := ""

	if item.Done {
		check = "[x]"
		textStyle = lipgloss.NewStyle().Foreground(colorSuccess).Strikethrough(true)
	} else if !item.CreatedAt.IsZero() {
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		itemDate := time.Date(item.CreatedAt.Year(), item.CreatedAt.Month(), item.CreatedAt.Day(), 0, 0, 0, 0, item.CreatedAt.Location())
		days := int(today.Sub(itemDate).Hours() / 24)
		if days == 1 {
			textStyle = lipgloss.NewStyle().Foreground(colorWarning)
			overdueTag = lipgloss.NewStyle().Foreground(colorWarning).Render(" (carry-over)")
		} else if days >= 2 {
			textStyle = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)
			overdueTag = lipgloss.NewStyle().Foreground(colorDanger).Bold(true).Render(fmt.Sprintf(" (%dd overdue)", days))
		}
	}

	text := item.Text
	if item.IssueNum > 0 {
		if pi := m.resolveIssue(item.IssueNum); pi != nil {
			text = fmt.Sprintf("#%d %s", item.IssueNum, pi.Title)
		}
	}

	return fmt.Sprintf("%s%s %s %s%s\n", cursor, lineNo, check, textStyle.Render(text), overdueTag)
}

func (m PlanViewModel) renderInboxItem(cursor string, idx int) string {
	item := m.inbox.Items[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	age := ""
	if !item.CreatedAt.IsZero() {
		age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(item.CreatedAt)))
	}
	from := ""
	if item.From != "" {
		from = commentAuthorStyle.Render(item.From+": ")
	}

	return fmt.Sprintf("%s%s %s%s%s\n", cursor, lineNo, from, item.Text, age)
}

func (m PlanViewModel) renderScratchItem(cursor string, idx int) string {
	note := m.plan.Scratch[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	age := ""
	if !note.CreatedAt.IsZero() {
		age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(note.CreatedAt)))
	}

	return fmt.Sprintf("%s%s %s%s\n", cursor, lineNo, note.Text, age)
}
