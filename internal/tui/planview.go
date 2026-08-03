package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/autumnust/tack/internal/model"
	"github.com/charmbracelet/lipgloss"
)

type planSection int

const (
	sectionWeekFocus planSection = iota
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
	section     planSection
	focusIdx    int    // index into WeekFocus (for sub-items, the parent)
	subIdx      int    // -1 if this is a top-level item, >=0 if sub-item
	header      bool   // true for section divider lines (not selectable)
	headerLabel string // custom header text; overrides the default "Research"
}

type PlanViewModel struct {
	plan    *model.Plan
	project *model.Project

	section        planSection
	flatItems      []flatItem
	cursorIdx      int
	scrollOffset   int
	viewHeight     int
	width          int
	showDone       bool
	hibanaExpanded bool

	// Search (Hibana tab only)
	searching   bool   // true while user is typing in search bar
	searchQuery string // current search filter text
}

func NewPlanViewModel(plan *model.Plan, project *model.Project) PlanViewModel {
	m := PlanViewModel{
		plan:     plan,
		project:  project,
		showDone: true,
	}
	m.rebuildFlat()
	return m
}

func (m *PlanViewModel) SetData(plan *model.Plan, project *model.Project) {
	m.plan = plan
	m.project = project
	m.rebuildFlat()
}

func (m *PlanViewModel) SetSize(width, height int) {
	m.width = width
	m.viewHeight = height - 6
}

func (m *PlanViewModel) ToggleShowDone() {
	m.showDone = !m.showDone
	m.rebuildFlat()
}

func (m *PlanViewModel) ShowingDone() bool {
	return m.showDone
}

func (m *PlanViewModel) ToggleHibanaExpanded() {
	m.hibanaExpanded = !m.hibanaExpanded
}

func (m *PlanViewModel) HibanaExpanded() bool {
	return m.hibanaExpanded
}

func (m *PlanViewModel) StartSearch() {
	m.searching = true
	m.searchQuery = ""
	m.rebuildFlat()
}

func (m *PlanViewModel) IsSearching() bool {
	return m.searching
}

func (m *PlanViewModel) SearchQuery() string {
	return m.searchQuery
}

func (m *PlanViewModel) HasSearchFilter() bool {
	return m.searchQuery != ""
}

func (m *PlanViewModel) ConfirmSearch() {
	m.searching = false
}

func (m *PlanViewModel) ClearSearch() {
	m.searching = false
	m.searchQuery = ""
	m.cursorIdx = 0
	m.scrollOffset = 0
	m.rebuildFlat()
}

func (m *PlanViewModel) UpdateSearchQuery(q string) {
	m.searchQuery = q
	m.cursorIdx = 0
	m.scrollOffset = 0
	m.rebuildFlat()
}

// HibanaDisplayIndices returns the underlying Scratch indexes in the normal
// unfiltered order shown by the Hibana tab. The indexes also determine the
// visible note numbers rendered by the TUI.
func (m PlanViewModel) HibanaDisplayIndices() []int {
	regular, research := m.hibanaGroups("")
	return append(regular, research...)
}

func (m PlanViewModel) hibanaGroups(query string) (regular, research []int) {
	query = strings.ToLower(query)
	for i := range m.plan.Scratch {
		if query != "" && !strings.Contains(strings.ToLower(m.plan.Scratch[i].Text), query) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(m.plan.Scratch[i].Text), "[research]") {
			research = append(research, i)
		} else {
			regular = append(regular, i)
		}
	}
	sort.SliceStable(regular, func(i, j int) bool {
		a := m.plan.Scratch[regular[i]]
		b := m.plan.Scratch[regular[j]]
		return hibanaSortTime(a).After(hibanaSortTime(b))
	})
	sort.SliceStable(research, func(i, j int) bool {
		a := m.plan.Scratch[research[i]]
		b := m.plan.Scratch[research[j]]
		return hibanaSortTime(a).After(hibanaSortTime(b))
	})
	return regular, research
}

func (m *PlanViewModel) rebuildFlat() {
	m.flatItems = nil
	switch m.section {
	case sectionWeekFocus:
		for i, f := range m.plan.WeekFocus {
			if !m.showDone && f.Done {
				continue
			}
			m.flatItems = append(m.flatItems, flatItem{section: sectionWeekFocus, focusIdx: i, subIdx: -1})
			for si := range f.SubItems {
				m.flatItems = append(m.flatItems, flatItem{section: sectionWeekFocus, focusIdx: i, subIdx: si})
			}
		}
	case sectionToday:
		for i := range m.plan.Today {
			if !m.showDone && m.plan.Today[i].Done {
				continue
			}
			m.flatItems = append(m.flatItems, flatItem{section: sectionToday, focusIdx: i, subIdx: -1})
		}
	case sectionHibana:
		regular, research := m.hibanaGroups(m.searchQuery)
		for _, i := range regular {
			m.flatItems = append(m.flatItems, flatItem{section: sectionHibana, focusIdx: i, subIdx: -1})
		}
		if len(research) > 0 {
			m.flatItems = append(m.flatItems, flatItem{section: sectionHibana, focusIdx: -1, subIdx: -1, header: true})
			for _, i := range research {
				m.flatItems = append(m.flatItems, flatItem{section: sectionHibana, focusIdx: i, subIdx: -1})
			}
		}
	case sectionMonthlyTarget:
		current := time.Now().Format("2006-01")
		// Group target indices by month, then walk pending (older) months
		// first as pinned sections so the user sees what still owes a
		// reflection before the current month's working set.
		groups := map[string][]int{}
		var months []string
		for i := range m.plan.MonthlyTargets {
			if !m.showDone && m.plan.MonthlyTargets[i].Done {
				continue
			}
			month := m.plan.MonthlyTargets[i].Month
			if month == "" {
				month = current // defensive — backfill should have set this
			}
			if _, seen := groups[month]; !seen {
				months = append(months, month)
			}
			groups[month] = append(groups[month], i)
		}
		sort.Strings(months)
		hasPending := false
		for _, mo := range months {
			if mo < current {
				hasPending = true
				break
			}
		}
		for _, mo := range months {
			if hasPending {
				label := mo
				if mo < current {
					label = mo + " — pending reflection (use :reflect to seal)"
				} else if mo == current {
					label = mo + " — current"
				}
				m.flatItems = append(m.flatItems, flatItem{
					section:     sectionMonthlyTarget,
					focusIdx:    -1,
					subIdx:      -1,
					header:      true,
					headerLabel: label,
				})
			}
			for _, i := range groups[mo] {
				m.flatItems = append(m.flatItems, flatItem{section: sectionMonthlyTarget, focusIdx: i, subIdx: -1})
			}
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
	m.searching = false
	m.searchQuery = ""
	m.section = (m.section + 1) % sectionCount
	m.cursorIdx = 0
	m.scrollOffset = 0
	m.rebuildFlat()
}

func (m *PlanViewModel) PrevSection() {
	m.searching = false
	m.searchQuery = ""
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
	if len(m.flatItems) == 0 {
		return
	}
	start := m.cursorIdx
	for {
		if m.cursorIdx < len(m.flatItems)-1 {
			m.cursorIdx++
		} else {
			m.cursorIdx = 0
			m.scrollOffset = 0
		}
		if !m.flatItems[m.cursorIdx].header || m.cursorIdx == start {
			break
		}
	}
	m.ensureVisible()
}

func (m *PlanViewModel) CursorUp() {
	if len(m.flatItems) == 0 {
		return
	}
	start := m.cursorIdx
	for {
		if m.cursorIdx > 0 {
			m.cursorIdx--
		} else {
			m.cursorIdx = len(m.flatItems) - 1
		}
		if !m.flatItems[m.cursorIdx].header || m.cursorIdx == start {
			break
		}
	}
	m.ensureVisible()
}

// JumpToLine moves the cursor to the flat item whose data index matches lineNum (1-indexed).
func (m *PlanViewModel) JumpToLine(lineNum int) bool {
	for i, fi := range m.flatItems {
		if fi.header {
			continue
		}
		if fi.focusIdx == lineNum-1 && fi.subIdx == -1 {
			m.cursorIdx = i
			m.ensureVisible()
			return true
		}
	}
	return false
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
		// Hibana reorder is not supported: the section is sorted by
		// recency on render, so manual order would not persist across
		// reloads. Returning false leaves the cursor untouched.
		return false
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
		// Hibana reorder is not supported (see MoveUp).
		return false
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
		item := &m.plan.WeekFocus[fi.focusIdx]
		item.Done = !item.Done
		if item.Done {
			item.DoneAt = time.Now()
		} else {
			item.DoneAt = time.Time{}
		}
		m.rebuildFlat()
		label := item.Text
		if item.IssueNum > 0 {
			label = fmt.Sprintf("#%d %s", item.IssueNum, item.Text)
		}
		if item.Done {
			return fmt.Sprintf("Finished: %s", label), true
		}
		return fmt.Sprintf("Reopened: %s", label), true
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
		if i == sectionMonthlyTarget && m.hasPendingMonths() {
			label = fmt.Sprintf("%s ⚐ (%d)", sectionName(i), count)
		}
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
			sectionWeekFocus:     "  No weekly focus items. Use :goal or :pin to add.",
			sectionToday:         "  No tasks for today. Use :today to add.",
			sectionHibana:        "  No notes yet. Use :hibana to jot something down.",
			sectionMonthlyTarget: "  No monthly targets. Use :target to add.",
		}
		sb.WriteString(helpStyle.Render(hints[m.section]))
		return sb.String()
	}

	// Pre-render all items and count their lines.
	rendered := make([]string, len(m.flatItems))
	lineHeights := make([]int, len(m.flatItems))
	for i, fi := range m.flatItems {
		cursor := "  "
		if i == m.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}
		rendered[i] = m.renderFlatItem(cursor, fi)
		lineHeights[i] = strings.Count(rendered[i], "\n")
		if lineHeights[i] == 0 {
			lineHeights[i] = 1
		}
	}

	// Available lines for items (total height minus tabs/status/bottom).
	availLines := height - 6
	if availLines < 1 {
		availLines = 1
	}

	// Adjust scrollOffset so the cursor item is fully visible.
	if m.cursorIdx < m.scrollOffset {
		m.scrollOffset = m.cursorIdx
	}
	// Scroll forward until cursor item fits within available lines.
	for m.scrollOffset < m.cursorIdx {
		lines := 0
		for i := m.scrollOffset; i <= m.cursorIdx; i++ {
			lines += lineHeights[i]
		}
		if lines <= availLines {
			break
		}
		m.scrollOffset++
	}

	// Render items from scrollOffset until we run out of lines.
	usedLines := 0
	for i := m.scrollOffset; i < len(m.flatItems); i++ {
		if usedLines+lineHeights[i] > availLines {
			// If we haven't rendered anything yet, the cursor item itself
			// is taller than the viewport (common in expanded hibana view).
			// Show its top portion so the view isn't blank.
			if usedLines == 0 {
				sb.WriteString(truncateLines(rendered[i], availLines))
			}
			break
		}
		sb.WriteString(rendered[i])
		usedLines += lineHeights[i]
	}

	return sb.String()
}

func (m PlanViewModel) renderFlatItem(cursor string, fi flatItem) string {
	if fi.header {
		text := fi.headerLabel
		if text == "" {
			text = "Research"
		}
		label := lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render("── " + text + " ──")
		return fmt.Sprintf("  %s\n", label)
	}
	switch m.section {
	case sectionWeekFocus:
		if fi.subIdx == -1 {
			return m.renderFocusItem(cursor, fi.focusIdx)
		}
		return m.renderSubItem(cursor, fi.focusIdx, fi.subIdx)
	case sectionToday:
		return m.renderTodoItem(cursor, fi.focusIdx)
	case sectionHibana:
		return m.renderHibanaItem(cursor, fi.focusIdx)
	case sectionMonthlyTarget:
		return m.renderMonthlyTargetItem(cursor, fi.focusIdx)
	}
	return ""
}

// splitWrap splits text into word-wrapped lines. firstWidth is the max for the
// first line (to leave room for a suffix), restWidth for subsequent lines.
// weekdaysBetween returns the number of weekdays (Mon-Fri) between two dates,
// ignoring the time component. Returns 0 if from and to are the same date.
func weekdaysBetween(from, to time.Time) int {
	from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	to = time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	if !to.After(from) {
		return 0
	}
	count := 0
	for d := from.AddDate(0, 0, 1); !d.After(to); d = d.AddDate(0, 0, 1) {
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			count++
		}
	}
	return count
}

func splitWrap(text string, firstWidth, restWidth int) []string {
	if firstWidth <= 0 {
		firstWidth = 1
	}
	if restWidth <= 0 {
		restWidth = 1
	}
	if len(text) <= firstWidth {
		return []string{text}
	}

	var lines []string
	remaining := text
	width := firstWidth
	for len(remaining) > 0 {
		if len(remaining) <= width {
			lines = append(lines, remaining)
			break
		}
		cut := width
		if cut >= len(remaining) {
			lines = append(lines, remaining)
			break
		}
		for cut > width/2 {
			if remaining[cut] == ' ' {
				break
			}
			cut--
		}
		if cut <= width/2 {
			cut = width // no good break point, hard cut
		}
		lines = append(lines, remaining[:cut])
		remaining = remaining[cut:]
		if len(remaining) > 0 && remaining[0] == ' ' {
			remaining = remaining[1:]
		}
		width = restWidth
	}
	return lines
}

// truncateLines returns the first n lines of s, preserving any leading
// newlines. The result always ends with "\n" if the source did.
func truncateLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i, r := range s {
		if r == '\n' {
			count++
			if count == n {
				return s[:i+1]
			}
		}
	}
	return s
}

// wrapText wraps long text to fit within maxWidth, indenting continuation lines.
func wrapText(text string, indent int, maxWidth int) string {
	if maxWidth <= indent+10 {
		return text // too narrow to wrap meaningfully
	}
	contentWidth := maxWidth - indent
	lines := splitWrap(text, contentWidth, contentWidth)
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

	check := "[ ]"
	textStyle := issueTitleStyle
	if item.Done {
		check = "[x]"
		textStyle = lipgloss.NewStyle().Foreground(colorSuccess).Strikethrough(true)
	}

	if item.IssueNum > 0 {
		num := issueNumStyle.Render(fmt.Sprintf("#%d", item.IssueNum))
		status := ""
		title := item.Text
		if pi := m.resolveIssue(item.IssueNum); pi != nil {
			title = pi.Title
			status = "  " + renderStatus(pi.Status, pi.State)
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
		// prefix: cursor(2) + lineNo(3) + space(1) + check(3) + space(1) + #N + space(1)
		prefixWidth := 10 + lipgloss.Width(num) + 1
		suffix := status + subCount
		suffixWidth := lipgloss.Width(suffix)
		contentWidth := m.width - prefixWidth
		pad := strings.Repeat(" ", prefixWidth)
		titleLines := strings.Split(title, "\n")
		firstWrapped := splitWrap(titleLines[0], contentWidth-suffixWidth, contentWidth)
		result := fmt.Sprintf("%s%s %s %s %s%s\n", cursor, lineNo, check, num, textStyle.Render(firstWrapped[0]), suffix)
		for _, l := range firstWrapped[1:] {
			result += pad + textStyle.Render(l) + "\n"
		}
		for _, line := range titleLines[1:] {
			if line == "" {
				result += "\n"
				continue
			}
			for _, wl := range splitWrap(line, contentWidth, contentWidth) {
				result += pad + textStyle.Render(wl) + "\n"
			}
		}
		return result
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
	// prefix: cursor(2) + lineNo(3) + space(1) + check(3) + space(1) = 10
	prefixWidth := 10
	suffixWidth := lipgloss.Width(subCount)
	contentWidth := m.width - prefixWidth
	pad := strings.Repeat(" ", prefixWidth)
	textLines := strings.Split(item.Text, "\n")
	firstWrapped := splitWrap(textLines[0], contentWidth-suffixWidth, contentWidth)
	result := fmt.Sprintf("%s%s %s %s%s\n", cursor, lineNo, check, textStyle.Render(firstWrapped[0]), subCount)
	for _, l := range firstWrapped[1:] {
		result += pad + textStyle.Render(l) + "\n"
	}
	for _, line := range textLines[1:] {
		if line == "" {
			result += "\n"
			continue
		}
		for _, wl := range splitWrap(line, contentWidth, contentWidth) {
			result += pad + textStyle.Render(wl) + "\n"
		}
	}
	return result
}

func (m PlanViewModel) renderSubItem(cursor string, focusIdx, subIdx int) string {
	sub := m.plan.WeekFocus[focusIdx].SubItems[subIdx]
	indentStr := "      " // indent under parent

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
	prefixWidth := 12
	contentWidth := m.width - prefixWidth
	lines := splitWrap(text, contentWidth, contentWidth)
	pad := strings.Repeat(" ", prefixWidth)
	result := fmt.Sprintf("%s%s%s %s\n", cursor, indentStr, check, textStyle.Render(lines[0]))
	for _, l := range lines[1:] {
		result += pad + textStyle.Render(l) + "\n"
	}
	return result
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
		days := weekdaysBetween(item.CreatedAt, time.Now())
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
		} else {
			// Project not loaded yet (or item not in this project) — still
			// surface the marker so the user knows the row is shipped.
			text = fmt.Sprintf("#%d %s", item.IssueNum, item.Text)
		}
	}

	// prefix: cursor(2) + lineNo(3) + space(1) + check(3) + space(1) = 10
	prefixWidth := 10
	suffixWidth := lipgloss.Width(overdueTag)
	contentWidth := m.width - prefixWidth
	pad := strings.Repeat(" ", prefixWidth)

	// Split on newlines to preserve multiline formatting (like Hibana)
	textLines := strings.Split(text, "\n")

	// First line: wrap with overdue suffix
	firstWrapped := splitWrap(textLines[0], contentWidth-suffixWidth, contentWidth)
	result := fmt.Sprintf("%s%s %s %s%s\n", cursor, lineNo, check, textStyle.Render(firstWrapped[0]), overdueTag)
	for _, l := range firstWrapped[1:] {
		result += pad + textStyle.Render(l) + "\n"
	}

	// Continuation lines: wrap each independently
	for _, line := range textLines[1:] {
		if line == "" {
			result += "\n"
			continue
		}
		wrapped := splitWrap(line, contentWidth, contentWidth)
		for _, wl := range wrapped {
			result += pad + textStyle.Render(wl) + "\n"
		}
	}
	return result
}

func (m PlanViewModel) renderHibanaItem(cursor string, idx int) string {
	note := m.plan.Scratch[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	ts := hibanaSortTime(note)
	age := ""
	if !ts.IsZero() {
		age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(ts)))
	}

	// prefix: cursor(2) + lineNo(3) + space(1) = 6
	prefixWidth := 6
	contentWidth := m.width - prefixWidth
	headStyle := upstashAgeStyle(ts)

	// Collapsed: first line only, age-shaded so stale notes pop
	if !m.hibanaExpanded {
		firstLine := strings.SplitN(note.Text, "\n", 2)[0]
		suffixWidth := lipgloss.Width(age)
		firstWrapped := splitWrap(firstLine, contentWidth-suffixWidth, contentWidth)
		return fmt.Sprintf("%s%s %s%s\n", cursor, lineNo, headStyle.Render(firstWrapped[0]), age)
	}

	// Expanded: full content; first line age-shaded, continuation muted
	pad := strings.Repeat(" ", prefixWidth)
	contStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	// Split on newlines to preserve multiline formatting
	noteLines := strings.Split(note.Text, "\n")

	// First line: wrap (not truncate) to fit with age suffix
	suffixWidth := lipgloss.Width(age)
	firstWrapped := splitWrap(noteLines[0], contentWidth-suffixWidth, contentWidth)
	result := fmt.Sprintf("%s%s %s%s\n", cursor, lineNo, headStyle.Render(firstWrapped[0]), age)
	for _, l := range firstWrapped[1:] {
		result += pad + contStyle.Render(l) + "\n"
	}

	// Continuation lines: wrap each to terminal width
	for _, line := range noteLines[1:] {
		if line == "" {
			result += "\n"
			continue
		}
		wrapped := splitWrap(line, contentWidth, contentWidth)
		for _, wl := range wrapped {
			result += pad + contStyle.Render(wl) + "\n"
		}
	}
	return result
}

func hibanaSortTime(note model.ScratchNote) time.Time {
	if !note.UpdatedAt.IsZero() {
		return note.UpdatedAt
	}
	return note.CreatedAt
}

// renderMonthlyTargetItem renders a target the same way hibana renders
// a note in expanded mode: first line with line number / checkbox / age,
// continuation lines indented under the prefix in muted color so longer
// aspirational text can breathe.
func (m PlanViewModel) renderMonthlyTargetItem(cursor string, idx int) string {
	item := m.plan.MonthlyTargets[idx]
	lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
		Render(fmt.Sprintf("%d", idx+1))

	check := "[ ]"
	firstStyle := issueTitleStyle
	contStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	if item.Done {
		check = "[x]"
		doneStyle := lipgloss.NewStyle().Foreground(colorSuccess).Strikethrough(true)
		firstStyle = doneStyle
		contStyle = doneStyle
	}

	age := ""
	if !item.CreatedAt.IsZero() {
		age = commentTimeStyle.Render(fmt.Sprintf(" (%s)", timeAgo(item.CreatedAt)))
	}

	// prefix: cursor(2) + lineNo(3) + space(1) + check(3) + space(1) = 10
	prefixWidth := 10
	contentWidth := m.width - prefixWidth
	if contentWidth < 10 {
		contentWidth = 10
	}
	pad := strings.Repeat(" ", prefixWidth)

	noteLines := strings.Split(item.Text, "\n")
	suffixWidth := lipgloss.Width(age)
	firstWrapped := splitWrap(noteLines[0], contentWidth-suffixWidth, contentWidth)
	result := fmt.Sprintf("%s%s %s %s%s\n", cursor, lineNo, check, firstStyle.Render(firstWrapped[0]), age)
	for _, l := range firstWrapped[1:] {
		result += pad + contStyle.Render(l) + "\n"
	}
	for _, line := range noteLines[1:] {
		if line == "" {
			result += "\n"
			continue
		}
		wrapped := splitWrap(line, contentWidth, contentWidth)
		for _, wl := range wrapped {
			result += pad + contStyle.Render(wl) + "\n"
		}
	}
	return result
}

// hasPendingMonths reports whether any monthly target belongs to a
// month earlier than the current one — i.e., whether the user owes a
// reflection. Drives the ⚐ glyph on the Monthly Target tab.
func (m PlanViewModel) hasPendingMonths() bool {
	current := time.Now().Format("2006-01")
	for _, t := range m.plan.MonthlyTargets {
		if t.Month != "" && t.Month < current {
			return true
		}
	}
	return false
}
