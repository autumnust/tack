package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/standup-kanban/standup-kanban/internal/model"
)

type listItemKind int

const (
	kindEpicHeader listItemKind = iota
	kindIssue
)

type listItem struct {
	kind     listItemKind
	issue    *model.ProjectItem
	parent   *model.ParentRef
	isLast   bool // last child in epic (for └ vs ├)
	epicKey  string
	groupIdx int
}

type BoardModel struct {
	persons      []model.PersonGroup
	personIdx    int
	cursorIdx    int
	expanded     map[string]bool // "epicKey" -> expanded
	visibleItems []listItem
	scrollOffset int
	viewHeight   int
}

func NewBoardModel(persons []model.PersonGroup) BoardModel {
	b := BoardModel{
		persons:  persons,
		expanded: make(map[string]bool),
	}
	// Expand all epics by default
	for pi, p := range persons {
		for gi, g := range p.Groups {
			if g.Parent != nil {
				b.expanded[epicKey(pi, gi)] = true
			}
		}
	}
	b.rebuildVisible()
	return b
}

func epicKey(personIdx, groupIdx int) string {
	return fmt.Sprintf("%d:%d", personIdx, groupIdx)
}

func (b *BoardModel) SetPersons(persons []model.PersonGroup) {
	b.persons = persons
	// Keep expanded state, add new epics as expanded
	for pi, p := range persons {
		for gi, g := range p.Groups {
			key := epicKey(pi, gi)
			if g.Parent != nil {
				if _, ok := b.expanded[key]; !ok {
					b.expanded[key] = true
				}
			}
		}
	}
	b.rebuildVisible()
}

func (b *BoardModel) rebuildVisible() {
	b.visibleItems = nil
	if b.personIdx >= len(b.persons) {
		return
	}
	p := b.persons[b.personIdx]
	for gi, g := range p.Groups {
		if g.Parent != nil {
			key := epicKey(b.personIdx, gi)
			b.visibleItems = append(b.visibleItems, listItem{
				kind:     kindEpicHeader,
				parent:   g.Parent,
				epicKey:  key,
				groupIdx: gi,
			})
			if b.expanded[key] {
				for ii := range g.Issues {
					b.visibleItems = append(b.visibleItems, listItem{
						kind:     kindIssue,
						issue:    &p.Groups[gi].Issues[ii],
						isLast:   ii == len(g.Issues)-1,
						groupIdx: gi,
					})
				}
			}
		} else {
			// Standalone issues
			for ii := range g.Issues {
				b.visibleItems = append(b.visibleItems, listItem{
					kind:     kindIssue,
					issue:    &p.Groups[gi].Issues[ii],
					isLast:   true,
					groupIdx: gi,
				})
			}
		}
	}

	// Clamp cursor
	if b.cursorIdx >= len(b.visibleItems) {
		b.cursorIdx = len(b.visibleItems) - 1
	}
	if b.cursorIdx < 0 {
		b.cursorIdx = 0
	}
}

func (b *BoardModel) NextPerson() {
	if b.personIdx < len(b.persons)-1 {
		b.personIdx++
		b.cursorIdx = 0
		b.scrollOffset = 0
		b.rebuildVisible()
	}
}

func (b *BoardModel) PrevPerson() {
	if b.personIdx > 0 {
		b.personIdx--
		b.cursorIdx = 0
		b.scrollOffset = 0
		b.rebuildVisible()
	}
}

func (b *BoardModel) CursorDown() {
	if b.cursorIdx < len(b.visibleItems)-1 {
		b.cursorIdx++
		b.ensureVisible()
	}
}

func (b *BoardModel) CursorUp() {
	if b.cursorIdx > 0 {
		b.cursorIdx--
		b.ensureVisible()
	}
}

func (b *BoardModel) ensureVisible() {
	if b.cursorIdx < b.scrollOffset {
		b.scrollOffset = b.cursorIdx
	}
	if b.viewHeight > 0 && b.cursorIdx >= b.scrollOffset+b.viewHeight {
		b.scrollOffset = b.cursorIdx - b.viewHeight + 1
	}
}

type SelectResult struct {
	Issue  *model.ProjectItem // non-nil if a specific issue was selected
	Epic   *model.ParentRef   // non-nil if an epic header was drilled into
	Children []model.ProjectItem // children of the epic
}

// ToggleOrSelect handles Enter key.
// First Enter on collapsed epic: expand. Enter on expanded epic: drill in.
// Enter on issue: drill into issue.
func (b *BoardModel) ToggleOrSelect() *SelectResult {
	if b.cursorIdx >= len(b.visibleItems) {
		return nil
	}
	item := b.visibleItems[b.cursorIdx]
	if item.kind == kindEpicHeader {
		if !b.expanded[item.epicKey] {
			// Expand
			b.expanded[item.epicKey] = true
			b.rebuildVisible()
			return nil
		}
		// Already expanded — drill into epic
		var children []model.ProjectItem
		if b.personIdx < len(b.persons) {
			p := b.persons[b.personIdx]
			if item.groupIdx < len(p.Groups) {
				children = p.Groups[item.groupIdx].Issues
			}
		}
		return &SelectResult{Epic: item.parent, Children: children}
	}
	return &SelectResult{Issue: item.issue}
}

func (b *BoardModel) SelectedIssue() *model.ProjectItem {
	if b.cursorIdx >= len(b.visibleItems) {
		return nil
	}
	item := b.visibleItems[b.cursorIdx]
	if item.kind == kindIssue {
		return item.issue
	}
	return nil
}

func (b *BoardModel) CurrentPerson() string {
	if b.personIdx < len(b.persons) {
		return b.persons[b.personIdx].Login
	}
	return ""
}

func (b *BoardModel) View(width, height int) string {
	b.viewHeight = height - 4 // reserve for header + footer

	var sb strings.Builder

	// Tab bar
	var tabs []string
	for i, p := range b.persons {
		issueCount := 0
		for _, g := range p.Groups {
			issueCount += len(g.Issues)
		}
		name := p.DisplayName
		if name == "" {
			name = p.Login
		}
		label := fmt.Sprintf("%s (%d)", name, issueCount)
		if i == b.personIdx {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(label))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	sb.WriteString("\n\n")

	// Items list
	if len(b.visibleItems) == 0 {
		sb.WriteString(helpStyle.Render("  No issues for this person"))
	}

	endIdx := len(b.visibleItems)
	if b.viewHeight > 0 && b.scrollOffset+b.viewHeight < endIdx {
		endIdx = b.scrollOffset + b.viewHeight
	}

	for i := b.scrollOffset; i < endIdx; i++ {
		item := b.visibleItems[i]
		cursor := "  "
		if i == b.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}

		switch item.kind {
		case kindEpicHeader:
			arrow := "▼"
			if !b.expanded[item.epicKey] {
				arrow = "▶"
			}
			childCount := 0
			if b.personIdx < len(b.persons) {
				p := b.persons[b.personIdx]
				if item.groupIdx < len(p.Groups) {
					childCount = len(p.Groups[item.groupIdx].Issues)
				}
			}
			title := fmt.Sprintf("%s #%d %s (%d)",
				arrow, item.parent.Number, item.parent.Title, childCount)
			sb.WriteString(cursor + epicStyle.Render(title))

		case kindIssue:
			branch := "├─"
			if item.isLast {
				branch = "└─"
			}
			// For standalone issues (no parent), no branch
			if b.personIdx < len(b.persons) {
				g := b.persons[b.personIdx].Groups[item.groupIdx]
				if g.Parent == nil {
					branch = "─"
				}
			}

			done := isDone(item.issue.Status, item.issue.State)
			num := issueNumStyle.Render(fmt.Sprintf("#%d", item.issue.Number))
			title := truncate(item.issue.Title, width-30)
			status := renderStatus(item.issue.Status)
			if done {
				doneStyle := lipgloss.NewStyle().Foreground(colorSuccess)
				num = doneStyle.Render(fmt.Sprintf("#%d", item.issue.Number))
				title = doneStyle.Render(title)
			} else {
				title = issueTitleStyle.Render(title)
			}
			line := fmt.Sprintf("  %s %s %s  %s", branch, num, title, status)
			sb.WriteString(cursor + line)
		}
		sb.WriteString("\n")
	}

	// Scroll indicator
	if len(b.visibleItems) > b.viewHeight && b.viewHeight > 0 {
		total := len(b.visibleItems)
		pct := 0
		if total > 0 {
			pct = (b.scrollOffset * 100) / total
		}
		sb.WriteString(helpStyle.Render(fmt.Sprintf("\n  ↕ %d/%d (%d%%)", b.scrollOffset+1, total, pct)))
	}

	return sb.String()
}

func renderStatus(status string) string {
	s := statusStyle(status)
	switch strings.ToLower(status) {
	case "done":
		return s.Render("● Done")
	case "in progress":
		return s.Render("● In Progress")
	case "in review":
		return s.Render("● In Review")
	case "todo":
		return s.Render("○ Todo")
	default:
		if status == "" {
			return helpStyle.Render("○ —")
		}
		return s.Render("○ " + status)
	}
}

func isDone(status, state string) bool {
	lower := strings.ToLower(status)
	return lower == "done" || lower == "closed" || state == "closed"
}

func truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
