package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/autumnust/tack/internal/model"
)

// NavItem is a navigable item in the detail view (sub-issue).
type NavItem struct {
	Number int
	Title  string
	Status string
	State  string
	Assignees []string
	// If this is a project item, its node ID for actions
	NodeID string
	ItemID string
}

type DetailModel struct {
	issue    *model.ProjectItem
	viewport viewport.Model
	ready    bool

	// Navigable sub-issues (upper pane)
	navItems   []NavItem
	navCursor  int
	navFocused bool // true = upper pane focused, false = lower pane (body)
}

// SelectedNavItem returns the currently highlighted nav item, or nil.
func (m *DetailModel) SelectedNavItem() *NavItem {
	if len(m.navItems) == 0 || m.navCursor < 0 || m.navCursor >= len(m.navItems) {
		return nil
	}
	return &m.navItems[m.navCursor]
}

func (m *DetailModel) NavDown() {
	if m.navCursor < len(m.navItems)-1 {
		m.navCursor++
	}
}

func (m *DetailModel) NavUp() {
	if m.navCursor > 0 {
		m.navCursor--
	}
}

func (m *DetailModel) HasNav() bool {
	return len(m.navItems) > 0
}

func (m *DetailModel) NavFocused() bool {
	return m.navFocused
}

func (m *DetailModel) ToggleFocus() {
	m.navFocused = !m.navFocused
}

func (m *DetailModel) FocusNav() {
	m.navFocused = true
}

// navSectionLines returns the number of terminal lines the nav section occupies.
// Header + blank + items + blank + divider = len(navItems) + 4
func (m *DetailModel) navSectionLines() int {
	if len(m.navItems) == 0 {
		return 0
	}
	return len(m.navItems) + 4
}

func NewDetailModel(issue *model.ProjectItem, width, height int, allItems []model.ProjectItem) DetailModel {
	vp := viewport.New(width, height-4)
	vp.SetContent(renderDetail(issue, width, allItems))
	return DetailModel{
		issue:    issue,
		viewport: vp,
		ready:    true,
	}
}

func newDetailPrerendered(issue *model.ProjectItem, content string, width, height int) DetailModel {
	vp := viewport.New(width, height-4)
	vp.SetContent(content)
	return DetailModel{
		issue:    issue,
		viewport: vp,
		ready:    true,
	}
}

// newEpicDetailModel creates an interactive detail view for an epic with navigable sub-issues.
func newEpicDetailModel(parent *model.ParentRef, projectChildren []model.ProjectItem, width, height int, childrenMap map[int][]model.SubIssue) DetailModel {
	var navItems []NavItem

	// Build nav items from sub-issues map (authoritative)
	if subIssues, ok := childrenMap[parent.Number]; ok && len(subIssues) > 0 {
		for _, si := range subIssues {
			ni := NavItem{
				Number: si.Number,
				Title:  si.Title,
				State:  si.State,
			}
			// Enrich with project item data if available
			for _, item := range projectChildren {
				if item.Number == si.Number {
					ni.Status = item.Status
					ni.Assignees = item.Assignees
					ni.NodeID = item.ID
					ni.ItemID = item.ItemID
					break
				}
			}
			navItems = append(navItems, ni)
		}
	} else {
		// Fallback to project children
		for _, item := range projectChildren {
			navItems = append(navItems, NavItem{
				Number:    item.Number,
				Title:     item.Title,
				Status:    item.Status,
				State:     item.State,
				Assignees: item.Assignees,
				NodeID:    item.ID,
				ItemID:    item.ItemID,
			})
		}
	}

	issue := &model.ProjectItem{
		Title:  parent.Title,
		Number: parent.Number,
		URL:    parent.URL,
		Repo:   parent.Repo,
	}

	dm := DetailModel{
		issue:      issue,
		navItems:   navItems,
		navFocused: true,
		ready:      true,
	}

	navHeight := dm.navSectionLines()
	vpHeight := height - 4 - navHeight
	if vpHeight < 3 {
		vpHeight = 3
	}
	vp := viewport.New(width, vpHeight)
	dm.viewport = vp
	return dm
}

// newPrerenderedEpicModel creates an epic detail with pre-rendered body but interactive nav items.
func newPrerenderedEpicModel(issue *model.ProjectItem, navItems []NavItem, bodyContent string, width, height int) DetailModel {
	dm := DetailModel{
		issue:      issue,
		navItems:   navItems,
		navFocused: true,
		ready:      true,
	}

	navHeight := dm.navSectionLines()
	vpHeight := height - 4 - navHeight
	if vpHeight < 3 {
		vpHeight = 3
	}
	vp := viewport.New(width, vpHeight)
	vp.SetContent(bodyContent)
	dm.viewport = vp
	return dm
}

func isDoneStr(status, state string) bool {
	lower := strings.ToLower(status)
	return lower == "done" || lower == "closed" || state == "closed"
}

func (m *DetailModel) SetSize(width, height int) {
	navHeight := m.navSectionLines()
	vpHeight := height - 4 - navHeight
	if vpHeight < 3 {
		vpHeight = 3
	}
	m.viewport.Width = width
	m.viewport.Height = vpHeight
}

// renderNavSection renders the upper pane: sub-issues list with cursor.
func (m DetailModel) renderNavSection(width int) string {
	var sb strings.Builder

	childHeader := lipgloss.NewStyle().Bold(true).Foreground(colorSecondary).
		Render(fmt.Sprintf("── Sub-Issues (%d) ", len(m.navItems)))
	sb.WriteString(childHeader)
	sb.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", 40)))
	sb.WriteString("\n\n")

	for i, ni := range m.navItems {
		branch := "├─"
		if i == len(m.navItems)-1 {
			branch = "└─"
		}

		cursor := "  "
		if m.navFocused && i == m.navCursor {
			cursor = cursorStyle.Render("► ")
		}

		// Line number (1-indexed, right-aligned)
		lineNo := lipgloss.NewStyle().Foreground(colorMuted).Width(3).Align(lipgloss.Right).
			Render(fmt.Sprintf("%d", i+1))

		done := isDoneStr(ni.Status, ni.State)
		num := issueNumStyle.Render(fmt.Sprintf("#%d", ni.Number))
		title := ni.Title
		if done {
			doneStyle := lipgloss.NewStyle().Foreground(colorSuccess)
			num = doneStyle.Render(fmt.Sprintf("#%d", ni.Number))
			title = doneStyle.Render(title)
		} else {
			title = issueTitleStyle.Render(title)
		}

		// renderStatus collapses the status+state semantics itself —
		// a closed issue always renders as Done regardless of its
		// project Status field.
		status := renderStatus(ni.Status, ni.State)

		assignees := ""
		if len(ni.Assignees) > 0 {
			assignees = detailMetaStyle.Render(fmt.Sprintf(" (%s)", strings.Join(ni.Assignees, ", ")))
		}

		sb.WriteString(fmt.Sprintf("%s%s %s %s %s  %s%s\n", cursor, lineNo, branch, num, title, status, assignees))
	}
	sb.WriteString("\n")

	return sb.String()
}

func (m DetailModel) View(width int) string {
	var sb strings.Builder

	if m.issue.Number != 0 {
		header := fmt.Sprintf("#%d %s", m.issue.Number, m.issue.Title)
		sb.WriteString(detailHeaderStyle.Render(header))
		sb.WriteString("\n")

		meta := []string{
			fmt.Sprintf("Status: %s", renderStatus(m.issue.Status, m.issue.State)),
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
	}

	if len(m.navItems) > 0 {
		sb.WriteString(m.renderNavSection(width))
		divider := lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", min(width-4, 80)))
		sb.WriteString(divider)
		sb.WriteString("\n")
	}

	sb.WriteString(m.viewport.View())

	return sb.String()
}

func getOrCreateRenderer(width int) *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(min(width-4, 100)),
	)
	if err != nil {
		return nil
	}
	return r
}

func renderMarkdown(renderer *glamour.TermRenderer, text string) string {
	if renderer == nil {
		return text
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}
	return rendered
}

func renderDetail(issue *model.ProjectItem, width int, allItems []model.ProjectItem) string {
	renderer := getOrCreateRenderer(width)
	var sb strings.Builder

	children := findChildren(issue.Number, allItems)
	if len(children) > 0 {
		childHeader := lipgloss.NewStyle().Bold(true).Foreground(colorSecondary).
			Render(fmt.Sprintf("── Child Tickets (%d) ", len(children)))
		sb.WriteString(childHeader)
		sb.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", 40)))
		sb.WriteString("\n\n")

		for i, child := range children {
			branch := "├─"
			if i == len(children)-1 {
				branch = "└─"
			}
			num := issueNumStyle.Render(fmt.Sprintf("#%d", child.Number))
			title := issueTitleStyle.Render(child.Title)
			status := renderStatus(child.Status, child.State)
			assignees := ""
			if len(child.Assignees) > 0 {
				assignees = detailMetaStyle.Render(fmt.Sprintf(" (%s)", strings.Join(child.Assignees, ", ")))
			}
			sb.WriteString(fmt.Sprintf("  %s %s %s  %s%s\n", branch, num, title, status, assignees))
		}
		sb.WriteString("\n")
	}

	if issue.Body != "" {
		sb.WriteString(renderMarkdown(renderer, issue.Body))
	} else {
		sb.WriteString(helpStyle.Render("  (no description)"))
	}

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
			rendered := renderMarkdown(renderer, c.Body)
			for _, line := range strings.Split(rendered, "\n") {
				sb.WriteString("    " + line + "\n")
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func preRenderItem(issue *model.ProjectItem, renderer *glamour.TermRenderer, childrenMap map[int][]model.SubIssue, allItems []model.ProjectItem) string {
	// Note: sub-issues from childrenMap are NOT rendered here because
	// buildDetailModel adds them as interactive nav items in the upper pane.
	// Rendering them here too would cause duplication.
	var sb strings.Builder

	if issue.Body != "" {
		sb.WriteString(renderMarkdown(renderer, issue.Body))
	} else {
		sb.WriteString(helpStyle.Render("  (no description)"))
	}

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
			rendered := renderMarkdown(renderer, c.Body)
			for _, line := range strings.Split(rendered, "\n") {
				sb.WriteString("    " + line + "\n")
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// renderBodyAndComments renders only the body and comments of an issue,
// without any child/sub-issue sections. Used as fallback when the interactive
// nav already handles sub-issues.
func renderBodyAndComments(issue *model.ProjectItem, width int) string {
	renderer := getOrCreateRenderer(width)
	var sb strings.Builder

	if issue.Body != "" {
		sb.WriteString(renderMarkdown(renderer, issue.Body))
	} else {
		sb.WriteString(helpStyle.Render("  (no description)"))
	}

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
			rendered := renderMarkdown(renderer, c.Body)
			for _, line := range strings.Split(rendered, "\n") {
				sb.WriteString("    " + line + "\n")
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func findChildren(parentNumber int, allItems []model.ProjectItem) []model.ProjectItem {
	var children []model.ProjectItem
	for _, item := range allItems {
		if item.Parent != nil && item.Parent.Number == parentNumber {
			children = append(children, item)
		}
	}
	return children
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
