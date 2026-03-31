package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/standup-kanban/standup-kanban/internal/github"
	"github.com/standup-kanban/standup-kanban/internal/grouping"
	"github.com/standup-kanban/standup-kanban/internal/model"
)

type viewMode int

const (
	viewBoard viewMode = iota
	viewDetail
)

type AppModel struct {
	// Data
	config   model.Config
	client   *github.Client
	project  *model.Project
	persons  []model.PersonGroup
	strategy grouping.Strategy

	// View state
	view    viewMode
	board   BoardModel
	detail  DetailModel
	command CommandModel

	// Layout
	width  int
	height int

	// Status
	loading   bool
	statusMsg string
	err       error
}

// Messages
type fetchDoneMsg struct {
	project *model.Project
	persons []model.PersonGroup
	err     error
}

type actionDoneMsg struct {
	msg string
	err error
}

func NewApp(config model.Config, client *github.Client) AppModel {
	return AppModel{
		config:   config,
		client:   client,
		command:  NewCommandModel(),
		strategy: grouping.ByEpic{},
		loading:  true,
	}
}

func (m AppModel) Init() tea.Cmd {
	return m.fetchData()
}

func (m AppModel) fetchData() tea.Cmd {
	return func() tea.Msg {
		project, err := m.client.FetchProject(m.config.Project, m.config.StatusField)
		if err != nil {
			return fetchDoneMsg{err: err}
		}
		persons := grouping.GroupByPerson(project.Items, m.config.Team, m.strategy)
		return fetchDoneMsg{project: project, persons: persons}
	}
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.view == viewDetail {
			m.detail.SetSize(m.width, m.height)
		}
		return m, nil

	case fetchDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.project = msg.project
		m.persons = msg.persons
		m.board = NewBoardModel(m.persons)
		m.statusMsg = fmt.Sprintf("Loaded %d items from %s", len(m.project.Items), m.project.Title)
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Error: %s", msg.err)
		} else {
			m.statusMsg = msg.msg
		}
		return m, nil

	case tea.KeyMsg:
		// Command bar takes priority when active
		if m.command.IsActive() {
			result, cmd := m.command.Update(msg)
			if result != nil {
				return m.executeCommand(result)
			}
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case ":":
			m.command.Activate()
			return m, nil
		case "r":
			m.loading = true
			m.statusMsg = "Refreshing..."
			return m, m.fetchData()
		case "o":
			return m.openInBrowser()
		}

		switch m.view {
		case viewBoard:
			return m.updateBoard(msg)
		case viewDetail:
			return m.updateDetail(msg)
		}
	}

	return m, nil
}

func (m AppModel) updateBoard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "l":
		m.board.NextPerson()
	case "shift+tab", "h":
		m.board.PrevPerson()
	case "j", "down":
		m.board.CursorDown()
	case "k", "up":
		m.board.CursorUp()
	case "enter":
		issue := m.board.ToggleOrSelect()
		if issue != nil {
			m.view = viewDetail
			m.detail = NewDetailModel(issue, m.width, m.height)
		}
	}
	return m, nil
}

func (m AppModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.view = viewBoard
		return m, nil
	case "j", "down":
		m.detail.viewport.LineDown(1)
	case "k", "up":
		m.detail.viewport.LineUp(1)
	case "d":
		m.detail.viewport.HalfViewDown()
	case "u":
		m.detail.viewport.HalfViewUp()
	}
	return m, nil
}

func (m AppModel) openInBrowser() (tea.Model, tea.Cmd) {
	var url string
	switch m.view {
	case viewBoard:
		issue := m.board.SelectedIssue()
		if issue != nil {
			url = issue.URL
		}
	case viewDetail:
		if m.detail.issue != nil {
			url = m.detail.issue.URL
		}
	}
	if url == "" {
		m.statusMsg = "No issue selected"
		return m, nil
	}

	opener := "open"
	if runtime.GOOS == "linux" {
		opener = "xdg-open"
	}
	cmd := exec.Command(opener, url)
	cmd.Start()
	m.statusMsg = fmt.Sprintf("Opened %s", url)
	return m, nil
}

func (m AppModel) executeCommand(cmd *CommandResult) (tea.Model, tea.Cmd) {
	switch cmd.Action {
	case "mv", "move":
		return m.cmdMove(cmd.Args)
	case "c", "comment":
		return m.cmdComment(cmd.Args)
	case "open":
		return m.openInBrowser()
	case "add":
		return m.cmdAddMember(cmd.Args)
	case "rm", "remove":
		return m.cmdRemoveMember(cmd.Args)
	case "group":
		return m.cmdGroup(cmd.Args)
	case "q", "quit":
		return m, tea.Quit
	default:
		m.statusMsg = fmt.Sprintf("Unknown command: %s", cmd.Action)
		return m, nil
	}
}

func (m AppModel) cmdMove(args []string) (tea.Model, tea.Cmd) {
	if len(args) < 2 {
		m.statusMsg = "Usage: :mv #<number> <status>"
		return m, nil
	}
	numStr := strings.TrimPrefix(args[0], "#")
	num, err := strconv.Atoi(numStr)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Invalid issue number: %s", args[0])
		return m, nil
	}
	newStatus := strings.Join(args[1:], " ")

	// Find the item
	var target *model.ProjectItem
	for i := range m.project.Items {
		if m.project.Items[i].Number == num {
			target = &m.project.Items[i]
			break
		}
	}
	if target == nil {
		m.statusMsg = fmt.Sprintf("Issue #%d not found in project", num)
		return m, nil
	}

	// Match status (case-insensitive prefix match)
	matchedStatus := matchStatus(newStatus, m.project.StatusField.Options)
	if matchedStatus == "" {
		available := make([]string, len(m.project.StatusField.Options))
		for i, o := range m.project.StatusField.Options {
			available[i] = o.Name
		}
		m.statusMsg = fmt.Sprintf("Unknown status %q. Available: %s", newStatus, strings.Join(available, ", "))
		return m, nil
	}

	m.statusMsg = fmt.Sprintf("Moving #%d to %s...", num, matchedStatus)
	return m, func() tea.Msg {
		err := m.client.MoveItem(m.project, target.ItemID, matchedStatus)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		target.Status = matchedStatus
		return actionDoneMsg{msg: fmt.Sprintf("Moved #%d to %s", num, matchedStatus)}
	}
}

func matchStatus(input string, options []model.FieldOption) string {
	lower := strings.ToLower(input)
	// Exact match first
	for _, o := range options {
		if strings.ToLower(o.Name) == lower {
			return o.Name
		}
	}
	// Prefix match
	for _, o := range options {
		if strings.HasPrefix(strings.ToLower(o.Name), lower) {
			return o.Name
		}
	}
	return ""
}

func (m AppModel) cmdComment(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :c \"your comment\" or :c #<number> \"your comment\""
		return m, nil
	}

	var target *model.ProjectItem
	body := strings.Join(args, " ")

	// Check if first arg is an issue number
	if strings.HasPrefix(args[0], "#") {
		numStr := strings.TrimPrefix(args[0], "#")
		if num, err := strconv.Atoi(numStr); err == nil {
			for i := range m.project.Items {
				if m.project.Items[i].Number == num {
					target = &m.project.Items[i]
					break
				}
			}
			body = strings.Join(args[1:], " ")
		}
	}

	// If no issue number given, use currently selected
	if target == nil {
		switch m.view {
		case viewBoard:
			target = m.board.SelectedIssue()
		case viewDetail:
			target = m.detail.issue
		}
	}

	if target == nil {
		m.statusMsg = "No issue selected. Use :c #<number> \"comment\" or select an issue first"
		return m, nil
	}

	if body == "" {
		m.statusMsg = "Empty comment"
		return m, nil
	}

	issueNum := target.Number
	nodeID := target.ID
	m.statusMsg = fmt.Sprintf("Commenting on #%d...", issueNum)
	return m, func() tea.Msg {
		err := m.client.AddComment(nodeID, body)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{msg: fmt.Sprintf("Commented on #%d", issueNum)}
	}
}

func (m AppModel) cmdAddMember(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :add @username"
		return m, nil
	}
	login := strings.TrimPrefix(args[0], "@")

	// Check if already in team
	for _, t := range m.config.Team {
		if t == login {
			m.statusMsg = fmt.Sprintf("%s is already in the team view", login)
			return m, nil
		}
	}

	m.config.Team = append(m.config.Team, login)
	m.persons = grouping.GroupByPerson(m.project.Items, m.config.Team, m.strategy)
	m.board.SetPersons(m.persons)
	m.statusMsg = fmt.Sprintf("Added %s to team view", login)
	return m, nil
}

func (m AppModel) cmdRemoveMember(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :rm @username"
		return m, nil
	}
	login := strings.TrimPrefix(args[0], "@")

	newTeam := make([]string, 0, len(m.config.Team))
	found := false
	for _, t := range m.config.Team {
		if t == login {
			found = true
			continue
		}
		newTeam = append(newTeam, t)
	}
	if !found {
		m.statusMsg = fmt.Sprintf("%s not in team view", login)
		return m, nil
	}

	m.config.Team = newTeam
	m.persons = grouping.GroupByPerson(m.project.Items, m.config.Team, m.strategy)
	m.board.SetPersons(m.persons)
	m.statusMsg = fmt.Sprintf("Removed %s from team view", login)
	return m, nil
}

func (m AppModel) cmdGroup(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = fmt.Sprintf("Current grouping: %s. Usage: :group epic | :group label:<prefix>", m.strategy.Name())
		return m, nil
	}
	switch {
	case args[0] == "epic":
		m.strategy = grouping.ByEpic{}
	case strings.HasPrefix(args[0], "label:"):
		prefix := strings.TrimPrefix(args[0], "label:")
		m.strategy = grouping.ByLabel{Prefix: prefix}
	default:
		m.statusMsg = fmt.Sprintf("Unknown grouping: %s", args[0])
		return m, nil
	}
	m.persons = grouping.GroupByPerson(m.project.Items, m.config.Team, m.strategy)
	m.board.SetPersons(m.persons)
	m.statusMsg = fmt.Sprintf("Grouping by: %s", m.strategy.Name())
	return m, nil
}

func (m AppModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %s\n\n  Check your config.yaml and GitHub authentication.\n  Press q to quit.\n", m.err)
	}

	if m.loading {
		return "\n  Loading project data from GitHub...\n"
	}

	var content string
	switch m.view {
	case viewBoard:
		content = m.board.View(m.width, m.height)
	case viewDetail:
		content = m.detail.View(m.width)
	}

	// Status bar
	statusLeft := statusBarStyle.Render(m.statusMsg)
	var viewHint string
	if m.view == viewDetail {
		viewHint = helpStyle.Render("[detail] Esc=back  j/k=scroll  o=open  :=cmd")
	} else {
		viewHint = commandHelp()
	}
	statusRight := lipgloss.PlaceHorizontal(m.width-lipgloss.Width(statusLeft), lipgloss.Right, viewHint)
	statusBar := lipgloss.JoinHorizontal(lipgloss.Top, statusLeft, statusRight)

	// Command bar or status
	var bottom string
	if m.command.IsActive() {
		bottom = m.command.View()
	} else {
		bottom = statusBar
	}

	// Layout: content fills available space, bottom bar at end
	available := m.height - 2 // 1 for bottom bar, 1 buffer
	contentStr := lipgloss.NewStyle().MaxHeight(available).Render(content)

	return contentStr + "\n" + bottom
}
