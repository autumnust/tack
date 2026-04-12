package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/autumnust/tack/internal/model"
)

type planSection int

const (
	sectionWeekFocus      planSection = iota
	sectionToday
	sectionHibana
	sectionMonthlyTarget
	sectionCount
)

func sectionName(s planSection) string {
	switch s {
	case sectionWeekFocus:
		return "Week Focus"
	case sectionToday:
		return "Today"
	case sectionHibana:
		return "Hibana"
	case sectionMonthlyTarget:
		return "Monthly Target"
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
	project *model.Project

	section      planSection
	flatItems    []flatItem
	cursorIdx    int
	scrollOffset int
	viewHeight   int
	width        int

}

func NewPlanViewModel(plan *model.Plan, project *model.Project) PlanViewModel {
	m := PlanViewModel{
		plan:    plan,
		project: project,
	}
	m.rebuildFlat()
	return m
}

func (m *PlanViewModel) SetData(plan *model.Plan, project *model.Project) {
	m.plan = plan
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
	case sectionHibana:
		for i := range m.plan.Scratch {
			m.flatItems = append(m.flatItems, flatItem{section: sectionHibana, focusIdx: i, subIdx: -1})
		}
	case sectionMonthlyTarget:
		for i := range m.plan.MonthlyTargets {
			m.flatItems = append(m.flatItems, flatItem{section: sectionMonthlyTarget, focusIdx: i, subIdx: -1})
		}
	}
	if m.cursorIdx >= len(m.flatItems) {
		m.cursorIdx = len(m.flatItems) - 1
	}
	if m.cursorIdx < 0 {
		m.cursorIdx = 0
	}
	m.ensureVisible()
}

func (m *PlanViewModel) SetSection(s planSection) {
	m.section = s
	m.cursorIdx = 0
	m.scrollOffset = 0
	m.rebuildFlat()
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
		m.ensureVisible()
	}
}

func (m *PlanViewModel) CursorUp() {
	if m.cursorIdx > 0 {
		m.cursorIdx--
		m.ensureVisible()
	}
}

func (m *PlanViewModel) ensureVisible() {
	if m.cursorIdx < m.scrollOffset {
		m.scrollOffset = m.cursorIdx
	}
	if m.viewHeight > 0 && m.cursorIdx >= m.scrollOffset+m.viewHeight {
		m.scrollOffset = m.cursorIdx - m.viewHeight + 1
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
	case sectionHibana:
		if fi.focusIdx > 0 {
			m.plan.Scratch[fi.focusIdx], m.plan.Scratch[fi.focusIdx-1] = m.plan.Scratch[fi.focusIdx-1], m.plan.Scratch[fi.focusIdx]
			m.cursorIdx--
			m.rebuildFlat()
			return true
		}
	case sectionMonthlyTarget:
		if fi.focusIdx > 0 {
			m.plan.MonthlyTargets[fi.focusIdx], m.plan.MonthlyTargets[fi.focusIdx-1] = m.plan.MonthlyTargets[fi.focusIdx-1], m.plan.MonthlyTargets[fi.focusIdx]
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
	case sectionHibana:
		if fi.focusIdx < len(m.plan.Scratch)-1 {
			m.plan.Scratch[fi.focusIdx], m.plan.Scratch[fi.focusIdx+1] = m.plan.Scratch[fi.focusIdx+1], m.plan.Scratch[fi.focusIdx]
			m.cursorIdx++
			m.rebuildFlat()
			return true
		}
	case sectionMonthlyTarget:
		if fi.focusIdx < len(m.plan.MonthlyTargets)-1 {
			m.plan.MonthlyTargets[fi.focusIdx], m.plan.MonthlyTargets[fi.focusIdx+1] = m.plan.MonthlyTargets[fi.focusIdx+1], m.plan.MonthlyTargets[fi.focusIdx]
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
	case sectionMonthlyTarget:
		item := &m.plan.MonthlyTargets[fi.focusIdx]
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

func (m *PlanViewModel) View(width, height int) string {
	m.viewHeight = height - 6
	m.width = width

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
		case sectionHibana:
			count = len(m.plan.Scratch)
		case sectionMonthlyTarget:
			count = len(m.plan.MonthlyTargets)
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
			sectionWeekFocus:    "  No weekly focus items. Use :goal or :pin to add.",
			sectionToday:        "  No tasks for today. Use :today to add.",
			sectionHibana:       "  No notes yet. Use :hibana to jot something down.",
			sectionMonthlyTarget: "  No monthly targets. Use :target to add.",
		}
		sb.WriteString(helpStyle.Render(hints[m.section]))
		return sb.String()
	}

	endIdx := len(m.flatItems)
	if m.viewHeight > 0 && m.scrollOffset+m.viewHeight < endIdx {
		endIdx = m.scrollOffset + m.viewHeight
	}

	for i := m.scrollOffset; i < endIdx; i++ {
		fi := m.flatItems[i]
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
		case sectionHibana:
			sb.WriteString(m.renderHibanaItem(cursor, fi.focusIdx))
		case sectionMonthlyTarget:
			sb.WriteString(m.renderMonthlyTargetItem(cursor, fi.focusIdx))
		}
	}

	return sb.String()
}

// wrapText wraps long text to fit within maxWidth, indenting continuation lines.
func wrapText(text string, indent int, maxWidth int) string {
	if maxWidth <= indent+10 {
		return text // too narrow to wrap meaningfully
	}
	contentWidth := maxWidth - indent
	if len(text) <= contentWidth {
		return text
	}

	var lines []string
	remaining := text
	for len(remaining) > 0 {
		if len(remaining) <= contentWidth {
			lines = append(lines, remaining)
			break
		}
		// Find last space within contentWidth
		cut := contentWidth
		for cut > contentWidth/2 {
			if remaining[cut] == ' ' {
				break
			}
			cut--
		}
		if cut <= contentWidth/2 {
			cut = contentWidth // no good break point, hard cut
		}
		lines = append(lines, remaining[:cut])
		remaining = remaining[cut:]
		if len(remaining) > 0 && remaining[0] == ' ' {
			remaining = remaining[1:]
		}
	}

	if len(lines) <= 1 {
		return text
	}
	pad := strings.Repeat(" ", indent)
	return lines[0] + "\n" + pad + strings.Join(lines[1:], "\n"+pad)
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
		maxTitle := m.width - 6 - lipgloss.Width(num) - 1 - lipgloss.Width(status) - lipgloss.Width(subCount)
		title = truncate(title, maxTitle)
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
	// prefix: cursor(2) + lineNo(3) + space(1) = 6
	maxText := m.width - 6 - lipgloss.Width(subCount)
	text := truncate(item.Text, maxText)
	return fmt.Sprintf("%s%s %s%s\n", cursor, lineNo, text, subCount)
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

	// prefix: cursor(2) + indent(6) + check(3) + space(1) = 12
	text = truncate(text, m.width-12)
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

	// prefix: cursor(2) + lineNo(3) + space(1) + check(3) + space(1) = 10
	maxText := m.width - 10 - lipgloss.Width(overdueTag)
	text = truncate(text, maxText)
	return fmt.Sprintf("%s%s %s %s%s\n", cursor, lineNo, check, textStyle.Render(text), overdueTag)
}

func (m PlanViewModel) renderHibanaItem(cursor string, idx int) string {
	note := m.plan.Scratch[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	age := ""
	if !note.CreatedAt.IsZero() {
		age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(note.CreatedAt)))
	}

	pad := "      " // 6 chars: align under text (2 cursor + 3 lineNo + 1 space)
	contentWidth := m.width - 6
	contStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	// Split on newlines to preserve multiline formatting
	lines := strings.Split(note.Text, "\n")

	// First line: truncate/wrap to fit with age suffix
	firstMax := contentWidth - lipgloss.Width(age)
	first := truncate(lines[0], firstMax)
	result := fmt.Sprintf("%s%s %s%s\n", cursor, lineNo, first, age)

	// Continuation lines: wrap each to terminal width
	for _, line := range lines[1:] {
		if line == "" {
			result += "\n"
			continue
		}
		wrapped := wrapText(line, 6, m.width)
		for i, wl := range strings.Split(wrapped, "\n") {
			if i == 0 {
				result += pad + contStyle.Render(wl) + "\n"
			} else {
				result += contStyle.Render(wl) + "\n"
			}
		}
	}
	return result
}

func (m PlanViewModel) renderMonthlyTargetItem(cursor string, idx int) string {
	item := m.plan.MonthlyTargets[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	check := "[ ]"
	textStyle := issueTitleStyle
	if item.Done {
		check = "[x]"
		textStyle = lipgloss.NewStyle().Foreground(colorSuccess).Strikethrough(true)
	}

	age := ""
	if !item.CreatedAt.IsZero() {
		age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(item.CreatedAt)))
	}

	// prefix: cursor(2) + lineNo(3) + space(1) + check(3) + space(1) = 10
	maxText := m.width - 10 - lipgloss.Width(age)
	text := truncate(item.Text, maxText)
	return fmt.Sprintf("%s%s %s %s%s\n", cursor, lineNo, check, textStyle.Render(text), age)
}
