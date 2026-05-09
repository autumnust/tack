package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/autumnust/tack/internal/model"
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
	showNotes    bool               // toggle private annotations
	showDone     bool               // toggle done/closed rows
	annotations  *model.Annotations // reference to annotations data
	personNotes  map[string]bool
}

func NewBoardModel(persons []model.PersonGroup) BoardModel {
	b := BoardModel{
		persons:     persons,
		expanded:    make(map[string]bool),
		personNotes: make(map[string]bool),
		showDone:    true, // matches plan view's default
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

func (b *BoardModel) SetAnnotations(ann *model.Annotations) {
	b.annotations = ann
}

func (b *BoardModel) ToggleNotes() {
	b.showNotes = !b.showNotes
}

func (b *BoardModel) ShowingNotes() bool {
	return b.showNotes
}

func (b *BoardModel) ToggleShowDone() {
	b.showDone = !b.showDone
	b.rebuildVisible()
}

func (b *BoardModel) ShowingDone() bool {
	return b.showDone
}

func (b *BoardModel) SetPersonNotes(presence map[string]bool) {
	b.personNotes = presence
}

func (b *BoardModel) SetPersons(persons []model.PersonGroup) {
	b.persons = persons
	// Clamp personIdx if the list shrank (e.g. after :rm)
	if len(b.persons) == 0 {
		b.personIdx = 0
	} else if b.personIdx >= len(b.persons) {
		b.personIdx = len(b.persons) - 1
	}
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

	// keepIssue applies the showDone toggle. Done items are hidden when
	// showDone is off; everything else passes through.
	keepIssue := func(it model.ProjectItem) bool {
		if !b.showDone && isDone(it.Status, it.State) {
			return false
		}
		return true
	}

	// epicHasVisibleChild reports whether the epic has at least one
	// non-filtered child in the current showDone setting. Hide otherwise-
	// empty epics so the filter doesn't leave dangling headers.
	epicHasVisibleChild := func(g model.IssueGroup) bool {
		for _, it := range g.Issues {
			if keepIssue(it) {
				return true
			}
		}
		return false
	}

	for gi, g := range p.Groups {
		if g.Parent != nil {
			if !b.showDone && !epicHasVisibleChild(g) {
				continue
			}
			key := epicKey(b.personIdx, gi)
			b.visibleItems = append(b.visibleItems, listItem{
				kind:     kindEpicHeader,
				parent:   g.Parent,
				epicKey:  key,
				groupIdx: gi,
			})
			if b.expanded[key] {
				visible := make([]int, 0, len(g.Issues))
				for ii := range g.Issues {
					if keepIssue(g.Issues[ii]) {
						visible = append(visible, ii)
					}
				}
				for n, ii := range visible {
					b.visibleItems = append(b.visibleItems, listItem{
						kind:     kindIssue,
						issue:    &p.Groups[gi].Issues[ii],
						isLast:   n == len(visible)-1,
						groupIdx: gi,
					})
				}
			}
		} else {
			// Standalone issues
			for ii := range g.Issues {
				if !keepIssue(g.Issues[ii]) {
					continue
				}
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

func (b *BoardModel) CurrentPersonDisplayName() string {
	if b.personIdx < len(b.persons) {
		p := b.persons[b.personIdx]
		if p.DisplayName != "" {
			return p.DisplayName
		}
		return p.Login
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
		if b.personNotes[p.Login] {
			label += " ✎"
		}
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
			var num string
			if item.issue.IsUpstash() {
				// Upstash items have no GitHub number; mark them with a pink
				// ◇ glyph so the user can tell at a glance what's local-only
				// (elevation-ready) vs a real GitHub issue.
				num = upstashGlyphStyle.Render("◇")
			} else {
				num = issueNumStyle.Render(fmt.Sprintf("#%d", item.issue.Number))
			}
			title := truncate(item.issue.Title, width-30)
			status := renderStatus(item.issue.Status, item.issue.State)
			switch {
			case done:
				doneStyle := lipgloss.NewStyle().Foreground(colorSuccess)
				if !item.issue.IsUpstash() {
					num = doneStyle.Render(fmt.Sprintf("#%d", item.issue.Number))
				}
				title = doneStyle.Render(title)
			case item.issue.IsUpstash():
				title = upstashTitleStyle.Render(title)
			default:
				title = issueTitleStyle.Render(title)
			}
			noteHint := ""
			if !item.issue.IsUpstash() {
				notes := b.notesForIssue(item.issue.Number)
				if len(notes) > 0 && !b.showNotes {
					noteHint = helpStyle.Render("  ✎")
				}
			}
			line := fmt.Sprintf("  %s %s %s  %s%s", branch, num, title, status, noteHint)
			sb.WriteString(cursor + line)
		}
		sb.WriteString("\n")

		// Show annotations inline when toggled on
		if b.showNotes && item.kind == kindIssue && !item.issue.IsUpstash() {
			notes := b.notesForIssue(item.issue.Number)
			noteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
			for _, n := range notes {
				sb.WriteString("        " + noteStyle.Render("✎ "+n) + "\n")
			}
		}
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

func (b *BoardModel) notesForIssue(num int) []string {
	if b.annotations == nil {
		return nil
	}
	for _, a := range b.annotations.Items {
		if a.IssueNum == num {
			return a.Notes
		}
	}
	return nil
}

// renderStatus is the single source of truth for what a project item's
// status looks like in the board / planview. A closed GitHub issue is
// authoritatively done regardless of what the project's Status field
// says — issues commonly get closed without anyone moving them on the
// project board, and the user's mental model is "if it's closed it's
// done." Anything still open uses the project Status verbatim.
func renderStatus(status, state string) string {
	if isDone(status, state) {
		return statusStyle("Done").Render("● Done")
	}
	s := statusStyle(status)
	switch strings.ToLower(status) {
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

// isDone reports whether a project item should be treated as completed.
// It is case-insensitive on both arguments so values from any source
// (GitHub returns "CLOSED"; some surfaces normalize to "closed";
// project Status is human-set and may be "Done"/"done"/"DONE")
// converge to the same answer.
func isDone(status, state string) bool {
	st := strings.ToLower(state)
	stat := strings.ToLower(status)
	return st == "closed" || stat == "done" || stat == "closed"
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
