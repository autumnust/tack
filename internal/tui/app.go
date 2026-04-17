package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/autumnust/tack/internal/cache"
	"github.com/autumnust/tack/internal/github"
	"github.com/autumnust/tack/internal/grouping"
	"github.com/autumnust/tack/internal/model"
	"github.com/autumnust/tack/internal/planning"
)

type viewMode int

const (
	viewBoard viewMode = iota
	viewDetail
	viewReview
	viewPlan
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

	// Planning
	planStore   *planning.Store
	plan        *model.Plan
	annotations *model.Annotations

	// View state
	view     viewMode
	prevView viewMode // for returning from detail/help
	board    BoardModel
	detail   DetailModel
	command  CommandModel
	review   ReviewModel
	planView PlanViewModel

	// Layout
	width  int
	height int

	// Status
	loading      bool
	cacheStale   bool
	statusMsg    string
	err          error
	confirmQuit  bool
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

type editorFinishedMsg struct {
	tmpPath string      // temp file to read back
	section planSection // which section was being edited
	idx     int         // -1 for new item, >=0 for editing existing
	subIdx  int         // -1 for top-level, >=0 for sub-item (week focus)
	err     error
}

const cacheTTL = 5 * time.Minute

func NewApp(config model.Config, configPath string, client *github.Client, startInPlanMode ...bool) AppModel {
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

	// Initialize planning store
	planDir := config.Planning.Dir
	if planDir == "" {
		planDir = "~/.tack"
	}
	if store, err := planning.NewStore(planDir); err == nil {
		app.planStore = store
		if plan, err := store.LoadPlan(); err == nil {
			store.Rollover(plan) // archive done items from previous days
			app.plan = plan
		} else {
			app.plan = &model.Plan{}
		}
		if ann, err := store.LoadAnnotations(); err == nil {
			app.annotations = ann
		} else {
			app.annotations = &model.Annotations{}
		}
	} else {
		app.plan = &model.Plan{}
		app.annotations = &model.Annotations{}
	}

	// Load cache synchronously — no loading flash
	cached := cache.LoadAny(config.Project)
	if cached != nil && cached.Project != nil {
		project := cached.Project
		project.Items = cached.Items
		app.project = project
		app.persons = grouping.GroupByPerson(project.Items, config.TeamLogins(), app.currentStrategy(), app.displayNames(), app.focusSets())
		app.board = NewBoardModel(app.persons)
		app.board.SetAnnotations(app.annotations)
		app.loading = false
		age := time.Since(cached.FetchedAt).Truncate(time.Second)
		app.statusMsg = fmt.Sprintf("Loaded %d items from cache (%s old)", len(project.Items), age)
		app.cacheStale = age > cacheTTL
	}

	app.planView = NewPlanViewModel(app.plan, app.project)
	if len(startInPlanMode) > 0 && startInPlanMode[0] {
		app.view = viewPlan
		app.statusMsg = "Planning mode"
	}

	return app
}

func (m AppModel) Init() tea.Cmd {
	if m.view == viewPlan {
		// Planning mode doesn't need GitHub data to start.
		// If we have cached data and it's stale, refresh in background.
		// If no cache at all, skip — don't block plan mode on GitHub.
		m.loading = false
		if m.project != nil && m.cacheStale {
			return m.fetchData()
		}
		return nil
	}
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
		if m.client == nil {
			return fetchDoneMsg{err: fmt.Errorf("GitHub client not available — run 'gh auth login' first")}
		}
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
		// Build strategy with parent numbers from this fetch
		strategy := m.strategy
		if epic, ok := strategy.(grouping.ByEpic); ok && project.ChildrenMap != nil {
			pn := make(map[int]bool, len(project.ChildrenMap))
			for k := range project.ChildrenMap {
				pn[k] = true
			}
			epic.ParentNumbers = pn
			strategy = epic
		}
		persons := grouping.GroupByPerson(project.Items, m.config.TeamLogins(), strategy, m.displayNames(), m.focusSets())
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

// buildDetailModel creates a detail view for any issue, with navigable
// sub-issues if the issue has children in ChildrenMap.
func (m AppModel) buildDetailModel(issue *model.ProjectItem) DetailModel {
	// Check if this issue has sub-issues
	var navItems []NavItem
	if m.project != nil && m.project.ChildrenMap != nil {
		if subIssues, ok := m.project.ChildrenMap[issue.Number]; ok && len(subIssues) > 0 {
			for _, si := range subIssues {
				ni := NavItem{
					Number: si.Number,
					Title:  si.Title,
					State:  si.State,
				}
				for _, item := range m.project.Items {
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
		}
	}

	if len(navItems) > 0 {
		// Build with navigable sub-issues + pre-rendered body.
		// Use pre-rendered content (which excludes sub-issues) or
		// render body-only to avoid duplicating the nav section.
		bodyContent := ""
		if rendered, ok := m.renderedDetails[issue.Number]; ok {
			bodyContent = rendered
		} else {
			bodyContent = renderBodyAndComments(issue, m.width)
		}
		return newPrerenderedEpicModel(issue, navItems, bodyContent, m.width, m.height)
	}

	// No sub-issues — standard detail view
	if rendered, ok := m.renderedDetails[issue.Number]; ok {
		return newDetailPrerendered(issue, rendered, m.width, m.height)
	}
	return NewDetailModel(issue, m.width, m.height, m.project.Items)
}

func (m AppModel) currentStrategy() grouping.Strategy {
	switch s := m.strategy.(type) {
	case grouping.ByEpic:
		// Inject parent numbers from project's ChildrenMap
		if m.project != nil && m.project.ChildrenMap != nil {
			pn := make(map[int]bool, len(m.project.ChildrenMap))
			for k := range m.project.ChildrenMap {
				pn[k] = true
			}
			s.ParentNumbers = pn
		}
		return s
	default:
		return m.strategy
	}
}

func (m AppModel) regroup() []model.PersonGroup {
	return grouping.GroupByPerson(m.project.Items, m.config.TeamLogins(), m.currentStrategy(), m.displayNames(), m.focusSets())
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.view == viewDetail {
			m.detail.SetSize(m.width, m.height)
		}
		m.planView.SetSize(m.width, m.height)
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
		m.board.SetAnnotations(m.annotations)
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

	case editorFinishedMsg:
		defer os.Remove(msg.tmpPath)
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Editor error: %s", msg.err)
			return m, nil
		}
		data, err := os.ReadFile(msg.tmpPath)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Error reading: %s", err)
			return m, nil
		}
		text := strings.TrimRight(string(data), "\n")
		if text == "" {
			m.statusMsg = "Empty text, discarded"
			return m, nil
		}
		switch msg.section {
		case sectionWeekFocus:
			if msg.idx < 0 {
				m.plan.WeekFocus = append(m.plan.WeekFocus, model.FocusItem{Text: text})
				m.statusMsg = "Added goal"
			} else if msg.subIdx >= 0 && msg.idx < len(m.plan.WeekFocus) {
				subs := m.plan.WeekFocus[msg.idx].SubItems
				if msg.subIdx < len(subs) {
					m.plan.WeekFocus[msg.idx].SubItems[msg.subIdx].Text = text
				}
				m.statusMsg = "Updated"
			} else if msg.idx < len(m.plan.WeekFocus) {
				m.plan.WeekFocus[msg.idx].Text = text
				m.statusMsg = "Updated"
			}
		case sectionToday:
			if msg.idx < 0 {
				m.plan.Today = append(m.plan.Today, model.TodoItem{
					Text:      text,
					CreatedAt: time.Now(),
				})
				m.statusMsg = "Task added"
			} else if msg.idx < len(m.plan.Today) {
				m.plan.Today[msg.idx].Text = text
				m.statusMsg = "Updated"
			}
		case sectionHibana:
			if msg.idx < 0 {
				m.plan.Scratch = append(m.plan.Scratch, model.ScratchNote{
					Text:      text,
					CreatedAt: time.Now(),
				})
				m.statusMsg = "Note added"
			} else if msg.idx < len(m.plan.Scratch) {
				m.plan.Scratch[msg.idx].Text = text
				m.statusMsg = "Note updated"
			}
		case sectionMonthlyTarget:
			if msg.idx < 0 {
				m.plan.MonthlyTargets = append(m.plan.MonthlyTargets, model.MonthlyTarget{
					Text:      text,
					CreatedAt: time.Now(),
				})
				m.statusMsg = "Target added"
			} else if msg.idx < len(m.plan.MonthlyTargets) {
				m.plan.MonthlyTargets[msg.idx].Text = text
				m.statusMsg = "Updated"
			}
		}
		m.planView.SetData(m.plan, m.project)
		if m.planView.section != msg.section {
			m.planView.SetSection(msg.section)
		}
		m.view = viewPlan
		return m, nil

	case tea.KeyMsg:
		// Quit confirmation takes highest priority
		if m.confirmQuit {
			m.confirmQuit = false
			switch msg.String() {
			case "q", "y":
				return m.initiateQuit()
			default:
				m.statusMsg = ""
				return m, nil
			}
		}

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
			if m.ops.Len() > 0 {
				return m.initiateQuit()
			}
			m.confirmQuit = true
			m.statusMsg = "Quit? (q/y to confirm, any other key to cancel)"
			return m, nil
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
			if m.view != viewPlan {
				return m.openInBrowser()
			}
		}

		switch m.view {
		case viewBoard:
			return m.updateBoard(msg)
		case viewDetail:
			return m.updateDetail(msg)
		case viewPlan:
			return m.updatePlan(msg)
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
	case "n":
		m.board.ToggleNotes()
		if m.board.ShowingNotes() {
			m.statusMsg = "Notes visible (n to hide)"
		} else {
			m.statusMsg = "Notes hidden"
		}
	case "enter":
		result := m.board.ToggleOrSelect()
		if result != nil {
			m.prevView = m.view
			m.view = viewDetail
			if result.Issue != nil {
				m.detail = m.buildDetailModel(result.Issue)
			} else if result.Epic != nil {
				epicIssue := &model.ProjectItem{
					Title:  result.Epic.Title,
					Number: result.Epic.Number,
					URL:    result.Epic.URL,
					Repo:   result.Epic.Repo,
				}
				m.detail = m.buildDetailModel(epicIssue)
			}
		}
	}
	return m, nil
}

func (m AppModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.view = m.prevView
		return m, nil
	case "tab":
		if m.detail.HasNav() {
			m.detail.ToggleFocus()
		}
	case "j", "down":
		if m.detail.HasNav() && m.detail.NavFocused() {
			m.detail.NavDown()
		} else {
			m.detail.viewport.LineDown(1)
		}
	case "k", "up":
		if m.detail.HasNav() && m.detail.NavFocused() {
			m.detail.NavUp()
		} else {
			m.detail.viewport.LineUp(1)
		}
	case "enter":
		if m.detail.NavFocused() {
			if nav := m.detail.SelectedNavItem(); nav != nil {
				// Drill into the selected sub-issue
				for i := range m.project.Items {
					if m.project.Items[i].Number == nav.Number {
						m.detail = m.buildDetailModel(&m.project.Items[i])
						return m, nil
					}
				}
				// Sub-issue not in project — can't drill further
				m.statusMsg = fmt.Sprintf("#%d is not in the project — press 'o' to open in browser", nav.Number)
			}
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
	// Purge completed focus items before saving
	m.purgeDoneFocusItems()

	// Always save planning data on quit
	m.savePlanningData()

	if m.ops.Len() == 0 {
		return m, tea.Quit
	}
	m.view = viewReview
	m.review = NewReviewModel(&m.ops)
	return m, nil
}

func (m AppModel) savePlanningData() {
	if m.planStore == nil {
		return
	}
	m.planStore.SavePlan(m.plan)
	m.planStore.SaveAnnotations(m.annotations)
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
	// Log command usage
	if m.planStore != nil {
		m.planStore.LogUsage(":" + cmd.Action)
	}

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
	case "note":
		return m.cmdNote(cmd.Args)
	case "delnote":
		return m.cmdDelNote(cmd.Args)
	case "pin":
		return m.cmdPin(cmd.Args)
	case "plan":
		return m.cmdSwitchToPlan()
	case "board":
		return m.cmdSwitchToBoard()
	case "goal":
		return m.cmdGoal(cmd.Args)
	case "today":
		return m.cmdToday(cmd.Args)
	case "done":
		return m.cmdDone(cmd.Args)
	case "hibana":
		return m.cmdHibana(cmd.Args)
	case "target":
		return m.cmdTarget(cmd.Args)
	case "sub":
		return m.cmdSub(cmd.Args)
	case "promote":
		return m.cmdPromote(cmd.Args)
	case "del", "delete":
		return m.cmdDelete(cmd.Args)
	case "stats":
		return m.cmdStats()
	case "recap":
		return m.cmdRecap()
	case "undo":
		return m.cmdUndo()
	case "h", "help":
		return m.cmdHelp()
	case "q", "quit":
		return m.initiateQuit()
	default:
		// Check if it's a line number jump (:<N>)
		if lineNum, err := strconv.Atoi(cmd.Action); err == nil {
			if m.view == viewPlan {
				if m.planView.JumpToLine(lineNum) {
					m.statusMsg = fmt.Sprintf("Jumped to line %d", lineNum)
				} else {
					m.statusMsg = fmt.Sprintf("Line %d not found", lineNum)
				}
			} else if m.view == viewDetail && m.detail.HasNav() {
				if lineNum >= 1 && lineNum <= len(m.detail.navItems) {
					m.detail.navCursor = lineNum - 1
					m.detail.FocusNav()
					m.statusMsg = fmt.Sprintf("Jumped to line %d", lineNum)
				} else {
					m.statusMsg = fmt.Sprintf("Line %d out of range (1-%d)", lineNum, len(m.detail.navItems))
				}
			} else {
				m.statusMsg = "Line jump not available in this view"
			}
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("Unknown command: %s", cmd.Action)
		return m, nil
	}
}

func (m AppModel) cmdMove(args []string) (tea.Model, tea.Cmd) {
	if m.view == viewPlan {
		return m.cmdPlanMove(args)
	}

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
		detailHeaderStyle.Render("Tack — Help"),
		"",
		section("Navigation"),
		"  " + key("Tab / l") + "Next person",
		"  " + key("Shift+Tab / h") + "Previous person",
		"  " + key("j / Down") + "Move cursor down",
		"  " + key("k / Up") + "Move cursor up",
		"  " + key("Enter") + "Expand/collapse epic, or drill into issue",
		"  " + key("n") + "Toggle private notes on board",
		"  " + key("Esc") + "Back (detail -> board, or cancel command)",
		"",
		section("Detail View"),
		"  " + key("j / k") + "Navigate sub-issues, or scroll body",
		"  " + key("Enter") + "Drill into selected sub-issue",
		"  " + key(":<N>") + "Jump to sub-issue by line number",
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
		"  " + key(":note \"text\"") + "Private annotation on selected issue",
		"  " + key(":delnote") + "Clear notes from selected issue",
		"  " + key(":pin") + "Pin selected issue to week focus",
		"  " + key(":undo") + "Undo last pending operation",
		"  " + key(":open") + "Open selected issue in browser",
		"  " + key(":h") + "Show this help",
		"",
		section("Mode Switching"),
		"  " + key(":plan") + "Switch to planning mode",
		"  " + key(":board") + "Switch to standup mode",
		"",
		section("Planning Mode"),
		"  " + key("Tab / h / l") + "Switch section (Week/Today/Hibana/Target)",
		"  " + key("j / k") + "Navigate items",
		"  " + key("J / K") + "Reorder items (move up/down)",
		"  " + key("o") + "New item (opens editor)",
		"  " + key("x") + "Toggle show/hide done items",
		"  " + key("Enter") + "Toggle done (focus / today / breakdown / target items)",
		"  " + key(":goal \"text\"") + "Add to week focus (or :goal #N, max 3)",
		"  " + key(":sub \"text\"") + "Add breakdown item to selected goal",
		"  " + key(":promote") + "Promote breakdown item to today",
		"  " + key(":today \"task\"") + "Add to today (or :today #N)",
		"  " + key(":done / :done N") + "Toggle done",
		"  " + key(":hibana \"note\"") + "Add note (or :hibana to open editor)",
		"  " + key(":target \"text\"") + "Add monthly target",
		"  " + key(":mv <tab>") + "Move item to tab (today/goal/hibana/target)",
		"  " + key(":mv goal N") + "Move item as sub-item of goal #N",
		"  " + key(":del") + "Delete selected item",
		"  " + key(":recap") + "Generate weekly recap",
		"  " + key(":stats") + "Show command usage stats",
		"",
		section("Global"),
		"  " + key("r") + "Refresh from GitHub",
		"  " + key("o") + "Open selected issue in browser",
		"  " + key(":") + "Enter command mode",
		"  " + key("q") + "Review pending changes & quit",
		"",
		helpStyle.Render("All :mv and :c commands are local-only until you quit."),
		helpStyle.Render("Planning data is saved automatically on quit."),
	}

	helpIssue := &model.ProjectItem{
		Title: "Help",
		Body:  strings.Join(lines, "\n"),
	}
	m.prevView = m.view
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

// --- Planning mode key handler ---

func (m AppModel) updatePlan(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle search input mode
	if m.planView.IsSearching() {
		switch msg.Type {
		case tea.KeyEscape:
			m.planView.ClearSearch()
			m.statusMsg = ""
		case tea.KeyEnter:
			m.planView.ConfirmSearch()
			n := len(m.planView.flatItems)
			// Don't count header items in the match count
			for _, fi := range m.planView.flatItems {
				if fi.header {
					n--
				}
			}
			m.statusMsg = fmt.Sprintf("Search: %s (%d matches)", m.planView.SearchQuery(), n)
		case tea.KeyBackspace:
			q := m.planView.SearchQuery()
			if len(q) > 0 {
				m.planView.UpdateSearchQuery(q[:len(q)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.planView.UpdateSearchQuery(m.planView.SearchQuery() + string(msg.Runes))
			} else if msg.Type == tea.KeySpace {
				m.planView.UpdateSearchQuery(m.planView.SearchQuery() + " ")
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "tab", "l":
		m.planView.NextSection()
	case "shift+tab", "h":
		m.planView.PrevSection()
	case "j", "down":
		m.planView.CursorDown()
	case "k", "up":
		m.planView.CursorUp()
	case "J":
		if m.planView.MoveDown() {
			m.statusMsg = "Moved down"
		}
	case "K":
		if m.planView.MoveUp() {
			m.statusMsg = "Moved up"
		}
	case "enter":
		if msg, ok := m.planView.ToggleDone(); ok {
			m.statusMsg = msg
		}
	case "e":
		return m.openPlanEditor()
	case "o":
		return m.insertPlanItem()
	case "x":
		if m.planView.section == sectionHibana {
			m.planView.ToggleHibanaExpanded()
			if m.planView.HibanaExpanded() {
				m.statusMsg = "Expanded view"
			} else {
				m.statusMsg = "Collapsed view"
			}
		} else {
			m.planView.ToggleShowDone()
			if m.planView.ShowingDone() {
				m.statusMsg = "Showing done items"
			} else {
				m.statusMsg = "Hiding done items"
			}
		}
	case "/":
		if m.planView.section == sectionHibana {
			m.planView.StartSearch()
			m.statusMsg = ""
		}
	case "esc":
		if m.planView.HasSearchFilter() {
			m.planView.ClearSearch()
			m.statusMsg = ""
		}
	}
	return m, nil
}

// --- Mode switching ---

func (m AppModel) cmdSwitchToPlan() (tea.Model, tea.Cmd) {
	m.planView = NewPlanViewModel(m.plan, m.project)
	m.view = viewPlan
	m.statusMsg = "Planning mode"
	return m, nil
}

func (m AppModel) cmdSwitchToBoard() (tea.Model, tea.Cmd) {
	m.purgeDoneFocusItems()
	m.view = viewBoard
	m.statusMsg = "Standup mode"
	return m, nil
}

// purgeDoneFocusItems removes completed weekly focus items.
func (m *AppModel) purgeDoneFocusItems() {
	active := m.plan.WeekFocus[:0]
	for _, f := range m.plan.WeekFocus {
		if !f.Done {
			active = append(active, f)
		}
	}
	m.plan.WeekFocus = active
}

// --- Standup annotation commands ---

func (m AppModel) cmdNote(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :note \"your private note\""
		return m, nil
	}
	target := m.selectedTarget()
	if target == nil {
		m.statusMsg = "No issue selected"
		return m, nil
	}

	note := strings.Join(args, " ")
	now := time.Now()

	// Find or create annotation
	found := false
	for i := range m.annotations.Items {
		if m.annotations.Items[i].IssueNum == target.Number {
			m.annotations.Items[i].Notes = append(m.annotations.Items[i].Notes, note)
			m.annotations.Items[i].UpdatedAt = now
			found = true
			break
		}
	}
	if !found {
		m.annotations.Items = append(m.annotations.Items, model.Annotation{
			IssueNum:  target.Number,
			IssueRepo: target.Repo,
			Notes:     []string{note},
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	m.board.SetAnnotations(m.annotations)
	m.statusMsg = fmt.Sprintf("Note added to #%d (private)", target.Number)
	return m, nil
}

func (m AppModel) cmdDelNote(args []string) (tea.Model, tea.Cmd) {
	target := m.selectedTarget()
	if target == nil {
		m.statusMsg = "No issue selected"
		return m, nil
	}

	for i := range m.annotations.Items {
		if m.annotations.Items[i].IssueNum == target.Number {
			m.annotations.Items = append(m.annotations.Items[:i], m.annotations.Items[i+1:]...)
			m.board.SetAnnotations(m.annotations)
			m.statusMsg = fmt.Sprintf("Notes cleared from #%d", target.Number)
			return m, nil
		}
	}
	m.statusMsg = fmt.Sprintf("No notes on #%d", target.Number)
	return m, nil
}

func (m AppModel) cmdPin(args []string) (tea.Model, tea.Cmd) {
	var issueNum int
	var repo, title string

	if len(args) > 0 && strings.HasPrefix(args[0], "#") {
		numStr := strings.TrimPrefix(args[0], "#")
		if n, err := strconv.Atoi(numStr); err == nil {
			issueNum = n
			for _, item := range m.project.Items {
				if item.Number == n {
					repo = item.Repo
					title = item.Title
					break
				}
			}
		}
	} else {
		target := m.selectedTarget()
		if target == nil {
			m.statusMsg = "No issue selected. Use :pin or :pin #N"
			return m, nil
		}
		issueNum = target.Number
		repo = target.Repo
		title = target.Title
	}

	// Check if already pinned
	for _, f := range m.plan.WeekFocus {
		if f.IssueNum == issueNum {
			m.statusMsg = fmt.Sprintf("#%d is already in week focus", issueNum)
			return m, nil
		}
	}

	if m.weekFocusFull() {
		m.statusMsg = fmt.Sprintf("Week focus is full (%d/%d active). Use :del to remove one first.", m.weekFocusActiveCount(), m.maxWeekFocus())
		return m, nil
	}

	m.plan.WeekFocus = append(m.plan.WeekFocus, model.FocusItem{
		Text:      title,
		IssueNum:  issueNum,
		IssueRepo: repo,
		Pinned:    true,
	})

	m.statusMsg = fmt.Sprintf("Pinned #%d to week focus", issueNum)
	return m, nil
}

// --- Planning mode commands ---

func (m AppModel) maxWeekFocus() int {
	if m.config.Planning.MaxWeekFocus > 0 {
		return m.config.Planning.MaxWeekFocus
	}
	return 3
}

func (m AppModel) weekFocusActiveCount() int {
	n := 0
	for _, f := range m.plan.WeekFocus {
		if !f.Done {
			n++
		}
	}
	return n
}

func (m AppModel) weekFocusFull() bool {
	return m.weekFocusActiveCount() >= m.maxWeekFocus()
}

func (m AppModel) cmdGoal(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :goal \"description\" or :goal #N"
		return m, nil
	}

	// When on Monthly Target section, redirect to :target
	if m.view == viewPlan && m.planView.section == sectionMonthlyTarget {
		return m.cmdTarget(args)
	}

	if strings.HasPrefix(args[0], "#") {
		return m.cmdPin(args)
	}

	if m.weekFocusFull() {
		m.statusMsg = fmt.Sprintf("Week focus is full (%d/%d active). Use :del to remove one first.", m.weekFocusActiveCount(), m.maxWeekFocus())
		return m, nil
	}

	text := strings.Join(args, " ")
	m.plan.WeekFocus = append(m.plan.WeekFocus, model.FocusItem{Text: text})
	m.planView.SetData(m.plan, m.project)
	m.statusMsg = fmt.Sprintf("Added goal: %s", text)
	return m, nil
}

func (m AppModel) cmdToday(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :today \"task description\" or :today #N"
		return m, nil
	}

	item := model.TodoItem{Text: strings.Join(args, " "), CreatedAt: time.Now()}

	// Check if first arg is an issue reference
	if strings.HasPrefix(args[0], "#") {
		numStr := strings.TrimPrefix(args[0], "#")
		if n, err := strconv.Atoi(numStr); err == nil {
			item.IssueNum = n
			if len(args) > 1 {
				item.Text = strings.Join(args[1:], " ")
			} else {
				// Use issue title
				for _, pi := range m.project.Items {
					if pi.Number == n {
						item.Text = pi.Title
						break
					}
				}
			}
		}
	}

	m.plan.Today = append(m.plan.Today, item)
	m.planView.SetData(m.plan, m.project)
	m.statusMsg = fmt.Sprintf("Added to today: %s", item.Text)
	return m, nil
}

func (m AppModel) cmdDone(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		// Toggle current cursor item in Today section
		if m.view == viewPlan && m.planView.section == sectionToday {
			fi := m.planView.currentFlat()
			if fi != nil {
				idx := fi.focusIdx
				if idx >= 0 && idx < len(m.plan.Today) {
					m.plan.Today[idx].Done = !m.plan.Today[idx].Done
					m.planView.SetData(m.plan, m.project)
					if m.plan.Today[idx].Done {
						m.statusMsg = fmt.Sprintf("Completed: %s", m.plan.Today[idx].Text)
					} else {
						m.statusMsg = fmt.Sprintf("Uncompleted: %s", m.plan.Today[idx].Text)
					}
					return m, nil
				}
			}
		}
		m.statusMsg = "Usage: :done <N> or select a today item and :done"
		return m, nil
	}

	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 || n > len(m.plan.Today) {
		m.statusMsg = fmt.Sprintf("Invalid item number (1-%d)", len(m.plan.Today))
		return m, nil
	}
	m.plan.Today[n-1].Done = !m.plan.Today[n-1].Done
	m.planView.SetData(m.plan, m.project)
	if m.plan.Today[n-1].Done {
		m.statusMsg = fmt.Sprintf("Completed: %s", m.plan.Today[n-1].Text)
	} else {
		m.statusMsg = fmt.Sprintf("Uncompleted: %s", m.plan.Today[n-1].Text)
	}
	return m, nil
}

func (m AppModel) cmdHibana(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		// No args: open editor for a new note
		return m.openEditorForNewHibana()
	}
	text := strings.Join(args, " ")
	m.plan.Scratch = append(m.plan.Scratch, model.ScratchNote{
		Text:      text,
		CreatedAt: time.Now(),
	})
	m.planView.SetData(m.plan, m.project)
	m.planView.SetSection(sectionHibana)
	m.view = viewPlan
	m.statusMsg = "Note added"
	return m, nil
}

func (m AppModel) cmdTarget(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :target \"your monthly target\""
		return m, nil
	}
	text := strings.Join(args, " ")
	m.plan.MonthlyTargets = append(m.plan.MonthlyTargets, model.MonthlyTarget{
		Text:      text,
		CreatedAt: time.Now(),
	})
	m.planView.SetData(m.plan, m.project)
	m.planView.SetSection(sectionMonthlyTarget)
	m.view = viewPlan
	m.statusMsg = "Monthly target added"
	return m, nil
}

func (m AppModel) openPlanEditor() (tea.Model, tea.Cmd) {
	fi := m.planView.currentFlat()
	if fi == nil {
		m.statusMsg = "No item selected"
		return m, nil
	}

	section := m.planView.section
	idx := fi.focusIdx
	subIdx := fi.subIdx

	// Determine current text; reject issue-linked items
	var text string
	switch section {
	case sectionWeekFocus:
		if subIdx >= 0 {
			sub := m.plan.WeekFocus[idx].SubItems[subIdx]
			if sub.IssueNum > 0 {
				m.statusMsg = "Cannot edit issue-linked item"
				return m, nil
			}
			text = sub.Text
		} else {
			item := m.plan.WeekFocus[idx]
			if item.IssueNum > 0 {
				m.statusMsg = "Cannot edit issue-linked item"
				return m, nil
			}
			text = item.Text
		}
	case sectionToday:
		item := m.plan.Today[idx]
		if item.IssueNum > 0 {
			m.statusMsg = "Cannot edit issue-linked item"
			return m, nil
		}
		text = item.Text
	case sectionHibana:
		text = m.plan.Scratch[idx].Text
	case sectionMonthlyTarget:
		text = m.plan.MonthlyTargets[idx].Text
	default:
		m.statusMsg = "Cannot edit this item"
		return m, nil
	}

	return m.launchEditor(text, section, idx, subIdx)
}

func (m AppModel) insertPlanItem() (tea.Model, tea.Cmd) {
	section := m.planView.section
	switch section {
	case sectionWeekFocus:
		if m.weekFocusFull() {
			m.statusMsg = fmt.Sprintf("Week focus is full (%d/%d active). Use :del to remove one first.", m.weekFocusActiveCount(), m.maxWeekFocus())
			return m, nil
		}
	case sectionToday, sectionHibana, sectionMonthlyTarget:
		// no constraints
	default:
		m.statusMsg = "Cannot add item here"
		return m, nil
	}
	return m.launchEditor("", section, -1, -1)
}

func (m AppModel) openEditorForNewHibana() (tea.Model, tea.Cmd) {
	return m.launchEditor("", sectionHibana, -1, -1)
}

func (m AppModel) launchEditor(text string, section planSection, idx, subIdx int) (tea.Model, tea.Cmd) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	f, err := os.CreateTemp("", "tack-plan-*.md")
	if err != nil {
		m.statusMsg = fmt.Sprintf("Error creating temp file: %s", err)
		return m, nil
	}
	tmpFile := f.Name()
	if text != "" {
		f.WriteString(text)
	}
	f.Close()

	c := exec.Command(editor, tmpFile)
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{tmpPath: tmpFile, section: section, idx: idx, subIdx: subIdx, err: err}
	})
}

func (m AppModel) cmdSub(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :sub \"breakdown task\" (adds to selected week focus item)"
		return m, nil
	}

	// Determine which focus item to add to
	focusIdx := -1
	if m.view == viewPlan && m.planView.section == sectionWeekFocus {
		fi := m.planView.currentFlat()
		if fi != nil {
			focusIdx = fi.focusIdx
		}
	}

	if focusIdx < 0 || focusIdx >= len(m.plan.WeekFocus) {
		m.statusMsg = "Navigate to a week focus item first"
		return m, nil
	}

	text := strings.Join(args, " ")
	sub := model.SubItem{Text: text}

	// Check if it's an issue ref
	if strings.HasPrefix(args[0], "#") {
		numStr := strings.TrimPrefix(args[0], "#")
		if n, err := strconv.Atoi(numStr); err == nil {
			sub.IssueNum = n
			if len(args) > 1 {
				sub.Text = strings.Join(args[1:], " ")
			} else {
				for _, pi := range m.project.Items {
					if pi.Number == n {
						sub.Text = pi.Title
						break
					}
				}
			}
		}
	}

	m.plan.WeekFocus[focusIdx].SubItems = append(m.plan.WeekFocus[focusIdx].SubItems, sub)
	m.planView.SetData(m.plan, m.project)
	m.statusMsg = fmt.Sprintf("Added breakdown to goal %d: %s", focusIdx+1, sub.Text)
	return m, nil
}

func (m AppModel) cmdPromote(args []string) (tea.Model, tea.Cmd) {
	sub, _, ok := m.planView.PromoteItem()
	if !ok {
		m.statusMsg = "Navigate to a breakdown item under week focus to promote"
		return m, nil
	}

	// Mark as done in breakdown
	sub.Done = true

	// Add to Today
	m.plan.Today = append(m.plan.Today, model.TodoItem{
		Text:     sub.Text,
		IssueNum: sub.IssueNum,
	})
	m.planView.SetData(m.plan, m.project)
	m.statusMsg = fmt.Sprintf("Promoted to today: %s", sub.Text)
	return m, nil
}

func (m AppModel) cmdStats() (tea.Model, tea.Cmd) {
	if m.planStore == nil {
		m.statusMsg = "No planning store configured"
		return m, nil
	}
	stats, err := m.planStore.LoadUsageStats()
	if err != nil {
		m.statusMsg = fmt.Sprintf("Error loading stats: %s", err)
		return m, nil
	}
	if len(stats) == 0 {
		m.statusMsg = "No usage data yet"
		return m, nil
	}

	// Sort by frequency
	type entry struct {
		cmd   string
		count int
	}
	var entries []entry
	for cmd, count := range stats {
		entries = append(entries, entry{cmd, count})
	}
	// Simple sort (descending)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].count > entries[i].count {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	section := func(title string) string {
		return epicStyle.Render(title)
	}
	key := func(k string) string {
		return cursorStyle.Render(fmt.Sprintf("%-20s", k))
	}

	var lines []string
	lines = append(lines, detailHeaderStyle.Render("Command Usage Stats"))
	lines = append(lines, "")
	lines = append(lines, section("Command Frequency"))
	for _, e := range entries {
		bar := strings.Repeat("█", min(e.count, 40))
		lines = append(lines, fmt.Sprintf("  %s %s %d", key(e.cmd), lipgloss.NewStyle().Foreground(colorSecondary).Render(bar), e.count))
	}

	helpIssue := &model.ProjectItem{Title: "Stats", Body: strings.Join(lines, "\n")}
	m.prevView = m.view
	m.view = viewDetail
	m.detail = newDetailPrerendered(helpIssue, strings.Join(lines, "\n"), m.width, m.height)
	return m, nil
}

func (m AppModel) cmdRecap() (tea.Model, tea.Cmd) {
	if m.planStore == nil {
		m.statusMsg = "No planning store configured"
		return m, nil
	}

	var sb strings.Builder
	now := time.Now()
	weekNum := fmt.Sprintf("%d-W%02d", now.Year(), (now.YearDay()+6)/7)

	sb.WriteString(fmt.Sprintf("# Weekly Recap — %s\n\n", weekNum))

	// Week Focus summary
	sb.WriteString("## Week Focus\n\n")
	if len(m.plan.WeekFocus) == 0 {
		sb.WriteString("No weekly goals set.\n\n")
	}
	for _, f := range m.plan.WeekFocus {
		title := f.Text
		if f.IssueNum > 0 {
			title = fmt.Sprintf("#%d %s", f.IssueNum, f.Text)
		}
		done := 0
		total := len(f.SubItems)
		for _, s := range f.SubItems {
			if s.Done {
				done++
			}
		}
		progress := ""
		if total > 0 {
			progress = fmt.Sprintf(" [%d/%d]", done, total)
		}
		sb.WriteString(fmt.Sprintf("- %s%s\n", title, progress))
		for _, s := range f.SubItems {
			check := "[ ]"
			if s.Done {
				check = "[x]"
			}
			sb.WriteString(fmt.Sprintf("  - %s %s\n", check, s.Text))
		}
	}

	// Completed items
	sb.WriteString("\n## Completed This Week\n\n")
	if len(m.plan.Completed) == 0 {
		sb.WriteString("No completed items.\n\n")
	}
	for _, item := range m.plan.Completed {
		sb.WriteString(fmt.Sprintf("- [x] %s\n", item.Text))
	}

	// Carry-over (undone items)
	sb.WriteString("\n## Carry-Over (Unfinished)\n\n")
	hasCarryOver := false
	for _, item := range m.plan.Today {
		if !item.Done {
			sb.WriteString(fmt.Sprintf("- [ ] %s\n", item.Text))
			hasCarryOver = true
		}
	}
	if !hasCarryOver {
		sb.WriteString("All clear!\n")
	}

	recap := sb.String()

	// Save to file
	if err := m.planStore.SaveRecap(weekNum, recap); err != nil {
		m.statusMsg = fmt.Sprintf("Recap error: %s", err)
		return m, nil
	}

	// Display in detail view
	helpIssue := &model.ProjectItem{Title: "Recap", Body: recap}
	m.prevView = m.view
	m.view = viewDetail
	m.detail = NewDetailModel(helpIssue, m.width, m.height, nil)
	m.statusMsg = fmt.Sprintf("Recap saved to recaps/%s.md", weekNum)
	return m, nil
}

// parsePlanTarget maps a user-facing tab name to a planSection.
func parsePlanTarget(name string) (planSection, bool) {
	switch strings.ToLower(name) {
	case "today":
		return sectionToday, true
	case "goal", "focus":
		return sectionWeekFocus, true
	case "hibana", "scratch":
		return sectionHibana, true
	case "target":
		return sectionMonthlyTarget, true
	}
	return 0, false
}

// cmdPlanMove moves an item between plan tabs.
// Syntax:
//
//	:mv today           — move cursor item to Today
//	:mv goal            — move cursor item to Week Focus
//	:mv goal 2          — move cursor item as sub-item of goal #2
//	:mv 3 today         — move item #3 to Today
//	:mv 3 goal 2        — move item #3 as sub-item of goal #2
func (m AppModel) cmdPlanMove(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.statusMsg = "Usage: :mv <tab> or :mv <N> <tab> [sub-N]"
		return m, nil
	}

	// Parse: optional source line number, required target tab, optional sub-item number
	srcLineNum := -1 // -1 means cursor
	targetIdx := 0
	if n, err := strconv.Atoi(args[0]); err == nil {
		srcLineNum = n
		targetIdx = 1
	}

	if targetIdx >= len(args) {
		m.statusMsg = "Usage: :mv <tab> or :mv <N> <tab> [sub-N]"
		return m, nil
	}

	destSection, ok := parsePlanTarget(args[targetIdx])
	if !ok {
		m.statusMsg = fmt.Sprintf("Unknown target: %s (use today, goal, hibana, target)", args[targetIdx])
		return m, nil
	}

	// Optional sub-item number for goal: :mv goal 2
	destSubIdx := -1
	if targetIdx+1 < len(args) {
		if n, err := strconv.Atoi(args[targetIdx+1]); err == nil {
			destSubIdx = n - 1 // 1-indexed to 0-indexed
		}
	}

	// Can't move to same section (unless moving into a sub-item)
	if destSection == m.planView.section && destSubIdx < 0 {
		m.statusMsg = "Already in this tab"
		return m, nil
	}

	// Check destination constraints before removing source
	if destSection == sectionWeekFocus && destSubIdx < 0 && m.weekFocusFull() {
		m.statusMsg = fmt.Sprintf("Week focus is full (%d/%d active)", m.weekFocusActiveCount(), m.maxWeekFocus())
		return m, nil
	}

	// Resolve source item text and remove it
	var srcText string
	if srcLineNum > 0 {
		idx := srcLineNum - 1
		switch m.planView.section {
		case sectionWeekFocus:
			if idx < len(m.plan.WeekFocus) {
				srcText = m.plan.WeekFocus[idx].Text
				m.plan.WeekFocus = append(m.plan.WeekFocus[:idx], m.plan.WeekFocus[idx+1:]...)
			}
		case sectionToday:
			if idx < len(m.plan.Today) {
				srcText = m.plan.Today[idx].Text
				m.plan.Today = append(m.plan.Today[:idx], m.plan.Today[idx+1:]...)
			}
		case sectionHibana:
			if idx < len(m.plan.Scratch) {
				srcText = m.plan.Scratch[idx].Text
				m.plan.Scratch = append(m.plan.Scratch[:idx], m.plan.Scratch[idx+1:]...)
			}
		case sectionMonthlyTarget:
			if idx < len(m.plan.MonthlyTargets) {
				srcText = m.plan.MonthlyTargets[idx].Text
				m.plan.MonthlyTargets = append(m.plan.MonthlyTargets[:idx], m.plan.MonthlyTargets[idx+1:]...)
			}
		}
	} else {
		fi := m.planView.currentFlat()
		if fi == nil {
			m.statusMsg = "No item selected"
			return m, nil
		}
		switch m.planView.section {
		case sectionWeekFocus:
			if fi.subIdx >= 0 {
				srcText = m.plan.WeekFocus[fi.focusIdx].SubItems[fi.subIdx].Text
				subs := &m.plan.WeekFocus[fi.focusIdx].SubItems
				*subs = append((*subs)[:fi.subIdx], (*subs)[fi.subIdx+1:]...)
			} else {
				srcText = m.plan.WeekFocus[fi.focusIdx].Text
				m.plan.WeekFocus = append(m.plan.WeekFocus[:fi.focusIdx], m.plan.WeekFocus[fi.focusIdx+1:]...)
			}
		case sectionToday:
			srcText = m.plan.Today[fi.focusIdx].Text
			m.plan.Today = append(m.plan.Today[:fi.focusIdx], m.plan.Today[fi.focusIdx+1:]...)
		case sectionHibana:
			srcText = m.plan.Scratch[fi.focusIdx].Text
			m.plan.Scratch = append(m.plan.Scratch[:fi.focusIdx], m.plan.Scratch[fi.focusIdx+1:]...)
		case sectionMonthlyTarget:
			srcText = m.plan.MonthlyTargets[fi.focusIdx].Text
			m.plan.MonthlyTargets = append(m.plan.MonthlyTargets[:fi.focusIdx], m.plan.MonthlyTargets[fi.focusIdx+1:]...)
		}
	}

	if srcText == "" {
		m.statusMsg = "No item to move"
		return m, nil
	}

	// Add to destination
	destName := sectionName(destSection)
	switch destSection {
	case sectionWeekFocus:
		if destSubIdx >= 0 {
			// Add as sub-item of a specific goal
			if destSubIdx >= len(m.plan.WeekFocus) {
				m.statusMsg = fmt.Sprintf("Goal %d does not exist", destSubIdx+1)
				return m, nil
			}
			m.plan.WeekFocus[destSubIdx].SubItems = append(m.plan.WeekFocus[destSubIdx].SubItems, model.SubItem{Text: srcText})
			destName = fmt.Sprintf("goal %d", destSubIdx+1)
		} else {
			m.plan.WeekFocus = append(m.plan.WeekFocus, model.FocusItem{Text: srcText})
		}
	case sectionToday:
		m.plan.Today = append(m.plan.Today, model.TodoItem{Text: srcText, CreatedAt: time.Now()})
	case sectionHibana:
		m.plan.Scratch = append(m.plan.Scratch, model.ScratchNote{Text: srcText, CreatedAt: time.Now()})
	case sectionMonthlyTarget:
		m.plan.MonthlyTargets = append(m.plan.MonthlyTargets, model.MonthlyTarget{Text: srcText, CreatedAt: time.Now()})
	}

	m.planView.SetData(m.plan, m.project)
	m.statusMsg = fmt.Sprintf("Moved to %s: %s", destName, srcText)
	return m, nil
}

func (m AppModel) cmdDelete(args []string) (tea.Model, tea.Cmd) {
	if m.view != viewPlan {
		m.statusMsg = ":del only works in planning mode"
		return m, nil
	}

	// Explicit line number arg: :del N (1-indexed into the current section's data)
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil {
			idx := n - 1
			switch m.planView.section {
			case sectionWeekFocus:
				if idx >= 0 && idx < len(m.plan.WeekFocus) {
					removed := m.plan.WeekFocus[idx].Text
					m.plan.WeekFocus = append(m.plan.WeekFocus[:idx], m.plan.WeekFocus[idx+1:]...)
					m.statusMsg = fmt.Sprintf("Removed from week focus: %s", removed)
				}
			case sectionToday:
				if idx >= 0 && idx < len(m.plan.Today) {
					removed := m.plan.Today[idx].Text
					m.plan.Today = append(m.plan.Today[:idx], m.plan.Today[idx+1:]...)
					m.statusMsg = fmt.Sprintf("Removed from today: %s", removed)
				}
			case sectionHibana:
				if idx >= 0 && idx < len(m.plan.Scratch) {
					m.plan.Scratch = append(m.plan.Scratch[:idx], m.plan.Scratch[idx+1:]...)
					m.statusMsg = "Note removed"
				}
			case sectionMonthlyTarget:
				if idx >= 0 && idx < len(m.plan.MonthlyTargets) {
					removed := m.plan.MonthlyTargets[idx].Text
					m.plan.MonthlyTargets = append(m.plan.MonthlyTargets[:idx], m.plan.MonthlyTargets[idx+1:]...)
					m.statusMsg = fmt.Sprintf("Removed target: %s", removed)
				}
			}
			m.planView.SetData(m.plan, m.project)
			return m, nil
		}
	}

	// Cursor-based deletion: resolve via flatItem to get correct data index
	fi := m.planView.currentFlat()
	if fi == nil {
		m.statusMsg = "Nothing to delete"
		return m, nil
	}

	switch m.planView.section {
	case sectionWeekFocus:
		if fi.subIdx >= 0 {
			// Deleting a sub-item
			if fi.focusIdx >= 0 && fi.focusIdx < len(m.plan.WeekFocus) {
				subs := &m.plan.WeekFocus[fi.focusIdx].SubItems
				if fi.subIdx < len(*subs) {
					removed := (*subs)[fi.subIdx].Text
					*subs = append((*subs)[:fi.subIdx], (*subs)[fi.subIdx+1:]...)
					m.statusMsg = fmt.Sprintf("Removed breakdown item: %s", removed)
				}
			}
		} else {
			// Deleting a top-level goal
			if fi.focusIdx >= 0 && fi.focusIdx < len(m.plan.WeekFocus) {
				removed := m.plan.WeekFocus[fi.focusIdx].Text
				m.plan.WeekFocus = append(m.plan.WeekFocus[:fi.focusIdx], m.plan.WeekFocus[fi.focusIdx+1:]...)
				m.statusMsg = fmt.Sprintf("Removed from week focus: %s", removed)
			}
		}
	case sectionToday:
		idx := fi.focusIdx
		if idx >= 0 && idx < len(m.plan.Today) {
			removed := m.plan.Today[idx].Text
			m.plan.Today = append(m.plan.Today[:idx], m.plan.Today[idx+1:]...)
			m.statusMsg = fmt.Sprintf("Removed from today: %s", removed)
		}
	case sectionHibana:
		idx := fi.focusIdx
		if idx >= 0 && idx < len(m.plan.Scratch) {
			m.plan.Scratch = append(m.plan.Scratch[:idx], m.plan.Scratch[idx+1:]...)
			m.statusMsg = "Note removed"
		}
	case sectionMonthlyTarget:
		idx := fi.focusIdx
		if idx >= 0 && idx < len(m.plan.MonthlyTargets) {
			removed := m.plan.MonthlyTargets[idx].Text
			m.plan.MonthlyTargets = append(m.plan.MonthlyTargets[:idx], m.plan.MonthlyTargets[idx+1:]...)
			m.statusMsg = fmt.Sprintf("Removed target: %s", removed)
		}
	}
	m.planView.SetData(m.plan, m.project)
	return m, nil
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
		return fmt.Sprintf("\n  Error: %s\n\n  Config: %s\n  Project: %s\n  Check your config and GitHub authentication.\n  Press q to quit.\n", m.err, m.configPath, m.config.Project)
	}

	if m.loading && m.view != viewPlan {
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
	case viewPlan:
		content = m.planView.View(m.width, m.height)
	}

	// Status bar
	statusLeft := statusBarStyle.Render(m.statusMsg)
	var viewHint string
	switch m.view {
	case viewDetail:
		if m.detail.HasNav() {
			viewHint = helpStyle.Render("[detail] Esc=back  Tab=switch pane  j/k=nav  Enter=drill  o=open  :=cmd")
		} else {
			viewHint = helpStyle.Render("[detail] Esc=back  j/k=scroll  o=open  :=cmd")
		}
	case viewReview:
		viewHint = helpStyle.Render("[review] Enter=toggle  a=all  n=none  y=push  d=discard  Esc=back")
	case viewPlan:
		if m.planView.section == sectionHibana {
			viewHint = helpStyle.Render("[plan] Tab=section  j/k=nav  o=new  e=edit  x=expand  /=search  :=cmd")
		} else {
			viewHint = helpStyle.Render("[plan] Tab=section  j/k=nav  o=new  e=edit  x=toggle done  :=cmd")
		}
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
	} else if m.view == viewPlan && m.planView.IsSearching() {
		searchPrompt := "/" + m.planView.SearchQuery() + "█"
		bottom = commandBarStyle.Render(searchPrompt)
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
