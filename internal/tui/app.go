package tui

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/standup-kanban/standup-kanban/internal/cache"
	"github.com/standup-kanban/standup-kanban/internal/github"
	"github.com/standup-kanban/standup-kanban/internal/grouping"
	"github.com/standup-kanban/standup-kanban/internal/model"
)

type viewMode int

const (
	viewBoard viewMode = iota
	viewDetail
	viewReview
)

type AppModel struct {
	// Data
	config     model.Config
	configPath string
	client     *github.Client
	project    *model.Project
	persons    []model.PersonGroup
	strategy   grouping.Strategy

	// Pre-rendered detail content (issue number -> rendered string)
	renderedDetails map[int]string

	// Operation queue
	ops OpQueue

	// View state
	view    viewMode
	board   BoardModel
	detail  DetailModel
	command CommandModel
	review  ReviewModel

	// Layout
	width  int
	height int

	// Status
	loading    bool
	cacheStale bool
	statusMsg  string
	err        error
}

// Messages
type fetchDoneMsg struct {
	project *model.Project
	persons []model.PersonGroup
	err     error
}

type pushDoneMsg struct {
	summary string
	err     error
}

const cacheTTL = 5 * time.Minute

func NewApp(config model.Config, configPath string, client *github.Client) AppModel {
	app := AppModel{
		config:     config,
		configPath: configPath,
		client:     client,
		command:    NewCommandModel(),
		strategy:   grouping.ByEpic{},
		loading:    true,
	}

	// Set up completion names from team config (display names + logins)
	var names []string
	for _, t := range config.Team {
		if t.Name != "" {
			names = append(names, t.Name)
		}
		names = append(names, t.Login)
	}
	app.command.SetCompletionNames(names)

	// Load cache synchronously — no loading flash
	cached := cache.LoadAny(config.Project)
	if cached != nil && cached.Project != nil {
		project := cached.Project
		project.Items = cached.Items
		app.project = project
		app.persons = grouping.GroupByPerson(project.Items, config.TeamLogins(), app.strategy, app.displayNames(), app.focusSets())
		app.board = NewBoardModel(app.persons)
		app.loading = false
		age := time.Since(cached.FetchedAt).Truncate(time.Second)
		app.statusMsg = fmt.Sprintf("Loaded %d items from cache (%s old)", len(project.Items), age)
		app.cacheStale = age > cacheTTL
	}

	return app
}

func (m AppModel) Init() tea.Cmd {
	if !m.loading && m.cacheStale {
		// Have cached data but it's stale — background refresh
		return m.fetchData()
	}
	if !m.loading {
		// Fresh cache, nothing to do
		return nil
	}
	// No cache — must fetch
	return m.fetchData()
}

type preRenderDoneMsg struct {
	rendered map[int]string
}

func (m AppModel) fetchData() tea.Cmd {
	return func() tea.Msg {
		project, err := m.client.FetchProject(m.config.Project, m.config.StatusField)
		if err != nil {
			return fetchDoneMsg{err: err}
		}

		// Fetch sub-issues for focus tickets to resolve parent-child
		focusNums := m.allFocusNumbers()
		var focusList []int
		for n := range focusNums {
			focusList = append(focusList, n)
		}
		// Determine repo from items (all items are from same repo in this project)
		repo := ""
		if len(project.Items) > 0 {
			repo = project.Items[0].Repo
		}
		if repo != "" && len(focusList) > 0 {
			subIssueMap, err := m.client.FetchSubIssues(repo, focusList)
			if err == nil && len(subIssueMap) > 0 {
				project.ChildrenMap = subIssueMap
				// Resolve parent refs on items using sub-issue data
				// Also fetch parent titles
				for parentNum := range subIssueMap {
					for _, item := range project.Items {
						if item.Number == parentNum {
							// Parent is a project item — use its title
							break
						}
					}
				}
				github.ResolveParentsFromSubIssues(project.Items, subIssueMap, repo)
				// Set parent titles from REST data for parents not in project
				m.setParentTitlesFromSubIssueMap(project, focusList)
			}
		}

		// Save to cache (only focused items)
		cache.Save(m.config.Project, project, focusNums)
		persons := grouping.GroupByPerson(project.Items, m.config.TeamLogins(), m.strategy, m.displayNames(), m.focusSets())
		return fetchDoneMsg{project: project, persons: persons}
	}
}

// setParentTitlesFromSubIssueMap fetches titles for parent epics that aren't project items.
func (m AppModel) setParentTitlesFromSubIssueMap(project *model.Project, focusNums []int) {
	// Build a set of parent numbers that need titles
	needTitle := make(map[int]bool)
	for _, item := range project.Items {
		if item.Parent != nil && item.Parent.Title == "" {
			needTitle[item.Parent.Number] = true
		}
	}
	if len(needTitle) == 0 {
		return
	}
	// Fetch each parent issue title via REST
	repo := ""
	if len(project.Items) > 0 {
		repo = project.Items[0].Repo
	}
	if repo == "" {
		return
	}
	titleCache := make(map[int]string)
	for num := range needTitle {
		path := fmt.Sprintf("/repos/%s/issues/%d", repo, num)
		data, err := m.client.RestGet(path)
		if err != nil {
			continue
		}
		var issue struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(data, &issue); err == nil {
			titleCache[num] = issue.Title
		}
	}
	// Apply titles
	for i := range project.Items {
		if project.Items[i].Parent != nil {
			if t, ok := titleCache[project.Items[i].Parent.Number]; ok {
				project.Items[i].Parent.Title = t
			}
		}
	}
}

func (m AppModel) displayNames() map[string]string {
	dn := make(map[string]string, len(m.config.Team))
	for _, t := range m.config.Team {
		if t.Name != "" {
			dn[t.Login] = t.Name
		}
	}
	return dn
}

func (m AppModel) focusSets() map[string]map[int]bool {
	var global map[int]bool
	if len(m.config.Focus) > 0 {
		global = make(map[int]bool, len(m.config.Focus))
		for _, n := range m.config.Focus {
			global[n] = true
		}
	}

	fs := make(map[string]map[int]bool)
	for _, t := range m.config.Team {
		hasPersonal := len(t.Focus) > 0
		if !hasPersonal && global == nil {
			continue
		}
		s := make(map[int]bool)
		for n := range global {
			s[n] = true
		}
		for _, n := range t.Focus {
			s[n] = true
		}
		if len(s) > 0 {
			fs[t.Login] = s
		}
	}
	if len(fs) == 0 {
		return nil
	}
	return fs
}

func (m AppModel) allFocusNumbers() map[int]bool {
	s := make(map[int]bool)
	for _, n := range m.config.Focus {
		s[n] = true
	}
	for _, t := range m.config.Team {
		for _, n := range t.Focus {
			s[n] = true
		}
	}
	return s
}

func (m AppModel) preRenderAll() tea.Cmd {
	items := m.project.Items
	childrenMap := m.project.ChildrenMap
	width := m.width
	if width == 0 {
		width = 120 // reasonable default before first resize
	}
	return func() tea.Msg {
		rendered := make(map[int]string, len(items))
		// Create ONE glamour renderer for all items
		renderer := getOrCreateRenderer(width)
		for i := range items {
			rendered[items[i].Number] = preRenderItem(&items[i], renderer, childrenMap, items)
		}
		return preRenderDoneMsg{rendered: rendered}
	}
}

func (m AppModel) regroup() []model.PersonGroup {
	return grouping.GroupByPerson(m.project.Items, m.config.TeamLogins(), m.strategy, m.displayNames(), m.focusSets())
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
			if m.project != nil {
				m.statusMsg = fmt.Sprintf("Refresh failed: %s (using cache)", msg.err)
				return m, nil
			}
			m.err = msg.err
			return m, nil
		}
		m.project = msg.project
		m.persons = msg.persons
		if m.board.persons != nil {
			m.board.SetPersons(m.persons)
			m.statusMsg = fmt.Sprintf("Refreshed: %d items from %s", len(m.project.Items), m.project.Title)
		} else {
			m.board = NewBoardModel(m.persons)
			m.statusMsg = fmt.Sprintf("Loaded %d items from %s", len(m.project.Items), m.project.Title)
		}
		// Pre-render all detail views in background
		return m, m.preRenderAll()

	case preRenderDoneMsg:
		m.renderedDetails = msg.rendered
		return m, nil

	case pushDoneMsg:
		// Push complete — exit
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Error: %s", msg.err)
			m.view = viewBoard
			return m, nil
		}
		// Print summary and quit
		m.statusMsg = msg.summary
		return m, tea.Quit

	case tea.KeyMsg:
		// Command bar takes priority when active
		if m.command.IsActive() {
			result, cmd := m.command.Update(msg)
			if result != nil {
				return m.executeCommand(result)
			}
			return m, cmd
		}

		// Review screen has its own key handling
		if m.view == viewReview {
			return m.updateReview(msg)
		}

		switch msg.String() {
		case "q":
			return m.initiateQuit()
		case "ctrl+c":
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
		result := m.board.ToggleOrSelect()
		if result != nil {
			m.view = viewDetail
			if result.Issue != nil {
				if rendered, ok := m.renderedDetails[result.Issue.Number]; ok {
					m.detail = newDetailPrerendered(result.Issue, rendered, m.width, m.height)
				} else {
					m.detail = NewDetailModel(result.Issue, m.width, m.height, m.project.Items)
				}
			} else if result.Epic != nil {
				// Build a synthetic issue for the epic and use pre-rendered content
				epicIssue := &model.ProjectItem{
					Title:  result.Epic.Title,
					Number: result.Epic.Number,
					URL:    result.Epic.URL,
					Repo:   result.Epic.Repo,
				}
				if rendered, ok := m.renderedDetails[result.Epic.Number]; ok {
					m.detail = newDetailPrerendered(epicIssue, rendered, m.width, m.height)
				} else {
					m.detail = newEpicDetailModel(result.Epic, result.Children, m.width, m.height, m.project.ChildrenMap)
				}
			}
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
		if m.detail.HasNav() {
			m.detail.NavDown()
			m.detail.RefreshEpicContent(m.width)
		} else {
			m.detail.viewport.LineDown(1)
		}
	case "k", "up":
		if m.detail.HasNav() {
			m.detail.NavUp()
			m.detail.RefreshEpicContent(m.width)
		} else {
			m.detail.viewport.LineUp(1)
		}
	case "enter":
		if nav := m.detail.SelectedNavItem(); nav != nil {
			// Drill into the selected sub-issue
			for i := range m.project.Items {
				if m.project.Items[i].Number == nav.Number {
					issue := &m.project.Items[i]
					if rendered, ok := m.renderedDetails[issue.Number]; ok {
						m.detail = newDetailPrerendered(issue, rendered, m.width, m.height)
					} else {
						m.detail = NewDetailModel(issue, m.width, m.height, m.project.Items)
					}
					return m, nil
				}
			}
			// Sub-issue not in project — can't drill further
			m.statusMsg = fmt.Sprintf("#%d is not in the project — press 'o' to open in browser", nav.Number)
		}
	case "d":
		m.detail.viewport.HalfViewDown()
	case "u":
		m.detail.viewport.HalfViewUp()
	}
	return m, nil
}

func (m AppModel) updateReview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = viewBoard
		return m, nil
	case "j", "down":
		m.review.CursorDown()
	case "k", "up":
		m.review.CursorUp()
	case "enter", " ":
		m.review.Toggle()
	case "a":
		m.review.CheckAll()
	case "n":
		m.review.UncheckAll()
	case "y":
		return m.pushCheckedOps()
	case "d":
		// Discard all and quit
		return m, tea.Quit
	}
	return m, nil
}

func (m AppModel) initiateQuit() (tea.Model, tea.Cmd) {
	if m.ops.Len() == 0 {
		return m, tea.Quit
	}
	m.view = viewReview
	m.review = NewReviewModel(&m.ops)
	return m, nil
}

func (m AppModel) pushCheckedOps() (tea.Model, tea.Cmd) {
	m.statusMsg = "Pushing changes to GitHub..."
	return m, func() tea.Msg {
		summary, err := m.ops.ExecuteChecked(m.project, m.client, m.configPath, &m.config)
		return pushDoneMsg{summary: summary, err: err}
	}
}

func (m AppModel) openInBrowser() (tea.Model, tea.Cmd) {
	var url string
	target := m.selectedTarget()
	if target != nil {
		url = target.URL
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
	case "a", "assign":
		return m.cmdAssign(cmd.Args)
	case "focus":
		return m.cmdFocus(cmd.Args)
	case "unfocus":
		return m.cmdUnfocus(cmd.Args)
	case "undo":
		return m.cmdUndo()
	case "h", "help":
		return m.cmdHelp()
	case "q", "quit":
		return m.initiateQuit()
	default:
		m.statusMsg = fmt.Sprintf("Unknown command: %s", cmd.Action)
		return m, nil
	}
}

func (m AppModel) cmdMove(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :mv <status> or :mv #<number> <status>"
		return m, nil
	}

	var target *model.ProjectItem
	var newStatus string

	if strings.HasPrefix(args[0], "#") {
		// Explicit issue number: :mv #N <status>
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
		newStatus = strings.Join(args[1:], " ")
	} else {
		// No issue number: :mv <status> — use selected
		target = m.selectedTarget()
		if target == nil {
			m.statusMsg = "No issue selected. Use :mv #<number> <status> or select an issue first"
			return m, nil
		}
		newStatus = strings.Join(args, " ")
	}

	matchedStatus := matchStatus(newStatus, m.project.StatusField.Options)
	if matchedStatus == "" {
		available := make([]string, len(m.project.StatusField.Options))
		for i, o := range m.project.StatusField.Options {
			available[i] = o.Name
		}
		m.statusMsg = fmt.Sprintf("Unknown status %q. Available: %s", newStatus, strings.Join(available, ", "))
		return m, nil
	}

	// Queue the operation and apply locally
	m.ops.Push(PendingOp{
		Kind:      OpMove,
		IssueNum:  target.Number,
		ItemID:    target.ItemID,
		NewStatus: matchedStatus,
	})
	target.Status = matchedStatus
	m.persons = m.regroup()
	m.board.SetPersons(m.persons)
	m.statusMsg = fmt.Sprintf("Queued: mv #%d → %s (%d pending)", target.Number, matchedStatus, m.ops.Len())
	return m, nil
}

func matchStatus(input string, options []model.FieldOption) string {
	lower := strings.ToLower(input)
	for _, o := range options {
		if strings.ToLower(o.Name) == lower {
			return o.Name
		}
	}
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

	if target == nil {
		target = m.selectedTarget()
	}

	if target == nil {
		m.statusMsg = "No issue selected. Use :c #<number> \"comment\" or select an issue first"
		return m, nil
	}

	if body == "" {
		m.statusMsg = "Empty comment"
		return m, nil
	}

	// Queue and apply locally
	m.ops.Push(PendingOp{
		Kind:        OpComment,
		IssueNum:    target.Number,
		IssueID:     target.ID,
		CommentBody: body,
	})
	target.Comments = append(target.Comments, model.Comment{
		Author:    "(you, pending)",
		Body:      body,
		CreatedAt: time.Now(),
	})
	m.statusMsg = fmt.Sprintf("Queued: comment on #%d (%d pending)", target.Number, m.ops.Len())
	return m, nil
}

func (m AppModel) cmdAssign(args []string) (tea.Model, tea.Cmd) {
	// :assign @Name  — assign selected issue
	// :assign #N @Name — assign specific issue
	if len(args) == 0 {
		m.statusMsg = "Usage: :assign @Name or :assign #N @Name"
		return m, nil
	}

	var target *model.ProjectItem
	var nameArg string

	if strings.HasPrefix(args[0], "#") {
		// :assign #N @Name
		if len(args) < 2 {
			m.statusMsg = "Usage: :assign #N @Name"
			return m, nil
		}
		numStr := strings.TrimPrefix(args[0], "#")
		num, err := strconv.Atoi(numStr)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Invalid issue number: %s", args[0])
			return m, nil
		}
		for i := range m.project.Items {
			if m.project.Items[i].Number == num {
				target = &m.project.Items[i]
				break
			}
		}
		nameArg = args[1]
	} else {
		// :assign @Name — use selected issue
		nameArg = args[0]
		target = m.selectedTarget()
	}

	if target == nil {
		m.statusMsg = "No issue selected. Use :assign #N @Name or select an issue first"
		return m, nil
	}

	name := strings.TrimPrefix(nameArg, "@")
	login := m.resolveNameToLogin(name)

	// Queue and apply locally
	m.ops.Push(PendingOp{
		Kind:        OpAssign,
		IssueNum:    target.Number,
		IssueID:     target.ID,
		AssignLogin: login,
	})
	if !containsStr(target.Assignees, login) {
		target.Assignees = append(target.Assignees, login)
	}
	m.persons = m.regroup()
	m.board.SetPersons(m.persons)
	displayName := m.config.DisplayName(login)
	m.statusMsg = fmt.Sprintf("Queued: assign #%d to %s (%d pending)", target.Number, displayName, m.ops.Len())
	return m, nil
}

func (m AppModel) cmdFocus(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :focus <number> | :focus @Name <number> | :focus clear | :focus save"
		return m, nil
	}

	switch args[0] {
	case "clear":
		m.config.Focus = nil
		for i := range m.config.Team {
			m.config.Team[i].Focus = nil
		}
		m.ops.Push(PendingOp{Kind: OpFocusClear})
		m.persons = m.regroup()
		m.board.SetPersons(m.persons)
		m.statusMsg = fmt.Sprintf("Focus cleared (%d pending)", m.ops.Len())
		return m, nil

	case "save":
		m.ops.Push(PendingOp{Kind: OpFocusSave})
		m.statusMsg = fmt.Sprintf("Queued: save focus to config (%d pending)", m.ops.Len())
		return m, nil
	}

	// :focus @Name 123 or :focus 123
	if strings.HasPrefix(args[0], "@") {
		if len(args) < 2 {
			m.statusMsg = "Usage: :focus @Name <number>"
			return m, nil
		}
		name := strings.TrimPrefix(args[0], "@")
		num, err := strconv.Atoi(args[1])
		if err != nil {
			m.statusMsg = fmt.Sprintf("Invalid number: %s", args[1])
			return m, nil
		}
		login := m.resolveNameToLogin(name)
		for i := range m.config.Team {
			if m.config.Team[i].Login == login {
				if !containsInt(m.config.Team[i].Focus, num) {
					m.config.Team[i].Focus = append(m.config.Team[i].Focus, num)
				}
				break
			}
		}
		m.ops.Push(PendingOp{Kind: OpFocusAdd, FocusTarget: "@" + name, FocusNumber: num})
		m.persons = m.regroup()
		m.board.SetPersons(m.persons)
		m.statusMsg = fmt.Sprintf("Added focus %d for %s (%d pending)", num, name, m.ops.Len())
		return m, nil
	}

	// Global focus
	num, err := strconv.Atoi(args[0])
	if err != nil {
		m.statusMsg = fmt.Sprintf("Invalid number: %s", args[0])
		return m, nil
	}
	if !containsInt(m.config.Focus, num) {
		m.config.Focus = append(m.config.Focus, num)
	}
	m.ops.Push(PendingOp{Kind: OpFocusAdd, FocusNumber: num})
	m.persons = m.regroup()
	m.board.SetPersons(m.persons)
	m.statusMsg = fmt.Sprintf("Added global focus %d (%d pending)", num, m.ops.Len())
	return m, nil
}

func (m AppModel) cmdUnfocus(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :unfocus <number> | :unfocus @Name <number>"
		return m, nil
	}

	if strings.HasPrefix(args[0], "@") {
		if len(args) < 2 {
			m.statusMsg = "Usage: :unfocus @Name <number>"
			return m, nil
		}
		name := strings.TrimPrefix(args[0], "@")
		num, err := strconv.Atoi(args[1])
		if err != nil {
			m.statusMsg = fmt.Sprintf("Invalid number: %s", args[1])
			return m, nil
		}
		login := m.resolveNameToLogin(name)
		for i := range m.config.Team {
			if m.config.Team[i].Login == login {
				m.config.Team[i].Focus = removeInt(m.config.Team[i].Focus, num)
				break
			}
		}
		m.ops.Push(PendingOp{Kind: OpFocusRemove, FocusTarget: "@" + name, FocusNumber: num})
		m.persons = m.regroup()
		m.board.SetPersons(m.persons)
		m.statusMsg = fmt.Sprintf("Removed focus %d for %s (%d pending)", num, name, m.ops.Len())
		return m, nil
	}

	num, err := strconv.Atoi(args[0])
	if err != nil {
		m.statusMsg = fmt.Sprintf("Invalid number: %s", args[0])
		return m, nil
	}
	m.config.Focus = removeInt(m.config.Focus, num)
	m.ops.Push(PendingOp{Kind: OpFocusRemove, FocusNumber: num})
	m.persons = m.regroup()
	m.board.SetPersons(m.persons)
	m.statusMsg = fmt.Sprintf("Removed global focus %d (%d pending)", num, m.ops.Len())
	return m, nil
}

func (m AppModel) cmdUndo() (tea.Model, tea.Cmd) {
	op := m.ops.Pop()
	if op == nil {
		m.statusMsg = "Nothing to undo"
		return m, nil
	}
	// For simplicity, undo just removes from queue.
	// Full state reversal would require snapshots — for now, suggest :r to refresh.
	m.statusMsg = fmt.Sprintf("Undone: %s (%d pending). Press r to refresh from GitHub for full revert.", op.Description(), m.ops.Len())
	return m, nil
}

func (m AppModel) cmdAddMember(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :add @username"
		return m, nil
	}
	login := strings.TrimPrefix(args[0], "@")

	for _, t := range m.config.Team {
		if t.Login == login {
			m.statusMsg = fmt.Sprintf("%s is already in the team view", login)
			return m, nil
		}
	}

	m.config.Team = append(m.config.Team, model.TeamMember{Login: login, Name: login})
	m.persons = m.regroup()
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

	newTeam := make([]model.TeamMember, 0, len(m.config.Team))
	found := false
	for _, t := range m.config.Team {
		if t.Login == login {
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
	m.persons = m.regroup()
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
	m.persons = m.regroup()
	m.board.SetPersons(m.persons)
	m.statusMsg = fmt.Sprintf("Grouping by: %s", m.strategy.Name())
	return m, nil
}

func (m AppModel) cmdHelp() (tea.Model, tea.Cmd) {
	section := func(title string) string {
		return epicStyle.Render(title)
	}
	key := func(k string) string {
		return cursorStyle.Render(fmt.Sprintf("%-28s", k))
	}

	lines := []string{
		detailHeaderStyle.Render("Standup Kanban — Help"),
		"",
		section("Navigation"),
		"  " + key("Tab / l") + "Next person",
		"  " + key("Shift+Tab / h") + "Previous person",
		"  " + key("j / Down") + "Move cursor down",
		"  " + key("k / Up") + "Move cursor up",
		"  " + key("Enter") + "Expand/collapse epic, or drill into issue",
		"  " + key("Esc") + "Back (detail -> board, or cancel command)",
		"",
		section("Detail View"),
		"  " + key("j / k") + "Scroll up/down",
		"  " + key("d / u") + "Half-page down/up",
		"  " + key("o") + "Open in browser",
		"",
		section("Commands (press : to enter)"),
		"  " + key(":mv <status>") + "Move selected issue (or :mv #N <status>)",
		"  " + key(":c \"comment\"") + "Comment on selected issue",
		"  " + key(":c #N \"comment\"") + "Comment on specific issue",
		"  " + key(":focus 123") + "Add to global focus",
		"  " + key(":focus @Name 123") + "Add to person's focus",
		"  " + key(":unfocus 123") + "Remove from global focus",
		"  " + key(":focus clear") + "Clear all focus filters",
		"  " + key(":focus save") + "Persist focus to config.yaml",
		"  " + key(":add @username") + "Add team member to view",
		"  " + key(":rm @username") + "Remove team member from view",
		"  " + key(":group epic") + "Group by parent ticket (default)",
		"  " + key(":group label:<prefix>") + "Group by label prefix",
		"  " + key(":a @Name") + "Assign selected issue (alias: :assign)",
		"  " + key(":a #N @Name") + "Assign specific issue",
		"  " + key(":undo") + "Undo last pending operation",
		"  " + key(":open") + "Open selected issue in browser",
		"  " + key(":h") + "Show this help",
		"",
		section("Global"),
		"  " + key("r") + "Refresh from GitHub",
		"  " + key("o") + "Open selected issue in browser",
		"  " + key(":") + "Enter command mode",
		"  " + key("q") + "Review pending changes & quit",
		"",
		helpStyle.Render("All :mv and :c commands are local-only until you quit."),
		helpStyle.Render("On quit, you review and confirm what gets pushed to GitHub."),
	}

	helpIssue := &model.ProjectItem{
		Title: "Help",
		Body:  strings.Join(lines, "\n"),
	}
	m.view = viewDetail
	// Build detail manually to skip glamour rendering
	m.detail = newDetailPrerendered(helpIssue, strings.Join(lines, "\n"), m.width, m.height)
	return m, nil
}

// resolveNameToLogin maps a display name (or login) back to a GitHub login.
// selectedTarget returns the project item currently under the cursor,
// checking board selection, detail view issue, and epic nav cursor.
func (m AppModel) selectedTarget() *model.ProjectItem {
	switch m.view {
	case viewBoard:
		return m.board.SelectedIssue()
	case viewDetail:
		// First check if we're on a navigable sub-issue in an epic view
		if nav := m.detail.SelectedNavItem(); nav != nil && nav.NodeID != "" {
			for i := range m.project.Items {
				if m.project.Items[i].Number == nav.Number {
					return &m.project.Items[i]
				}
			}
		}
		// Fall back to the detail view's issue itself
		if m.detail.issue != nil {
			return m.detail.issue
		}
	}
	return nil
}

func (m AppModel) resolveNameToLogin(name string) string {
	lower := strings.ToLower(name)
	for _, t := range m.config.Team {
		if strings.ToLower(t.Name) == lower || strings.ToLower(t.Login) == lower {
			return t.Login
		}
	}
	return name
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
	case viewReview:
		content = m.review.View(m.width, m.height)
	}

	// Status bar
	statusLeft := statusBarStyle.Render(m.statusMsg)
	var viewHint string
	switch m.view {
	case viewDetail:
		viewHint = helpStyle.Render("[detail] Esc=back  j/k=scroll  o=open  :=cmd")
	case viewReview:
		viewHint = helpStyle.Render("[review] Enter=toggle  a=all  n=none  y=push  d=discard  Esc=back")
	default:
		pending := ""
		if m.ops.Len() > 0 {
			pending = fmt.Sprintf(" [%d pending]", m.ops.Len())
		}
		viewHint = helpStyle.Render(commandHelp() + pending)
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

	available := m.height - 2
	contentStr := lipgloss.NewStyle().MaxHeight(available).Render(content)

	return contentStr + "\n" + bottom
}

// Helpers

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func containsStr(slice []string, val string) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func removeInt(slice []int, val int) []int {
	result := make([]int, 0, len(slice))
	for _, v := range slice {
		if v != val {
			result = append(result, v)
		}
	}
	return result
}
