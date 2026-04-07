package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/standup-kanban/standup-kanban/internal/grouping"
	"github.com/standup-kanban/standup-kanban/internal/model"
)

// --- Test helpers ---

func testConfig() model.Config {
	return model.Config{
		Project:     "https://github.com/orgs/test-org/projects/1",
		StatusField: "Status",
		Focus:       []int{100, 200},
		Planning: model.PlanningConfig{
			Dir:          "",
			MaxWeekFocus: 3,
		},
		Team: []model.TeamMember{
			{Login: "alice", Name: "Alice"},
			{Login: "bob", Name: "Bob"},
			{Login: "charlie", Name: "Charlie"},
		},
	}
}

func testProject() *model.Project {
	return &model.Project{
		ID:    "proj-1",
		Title: "Test Project",
		StatusField: model.FieldInfo{
			ID:   "sf-1",
			Name: "Status",
			Options: []model.FieldOption{
				{ID: "opt-1", Name: "Todo"},
				{ID: "opt-2", Name: "In Progress"},
				{ID: "opt-3", Name: "Done"},
			},
		},
		ChildrenMap: map[int][]model.SubIssue{
			100: {
				{Number: 101, Title: "Child 1 of 100", State: "open"},
				{Number: 102, Title: "Child 2 of 100", State: "closed"},
			},
			200: {
				{Number: 201, Title: "Child 1 of 200", State: "open"},
			},
		},
		Items: []model.ProjectItem{
			{ID: "n-100", ItemID: "pi-100", Title: "Epic Alpha", Number: 100, Status: "In Progress", Assignees: []string{"alice"}, Repo: "test-org/repo", URL: "https://github.com/test-org/repo/issues/100"},
			{ID: "n-101", ItemID: "pi-101", Title: "Child 1 of 100", Number: 101, Status: "In Progress", Assignees: []string{"alice"}, Repo: "test-org/repo", URL: "https://github.com/test-org/repo/issues/101", Parent: &model.ParentRef{Number: 100, Title: "Epic Alpha", Repo: "test-org/repo"}},
			{ID: "n-102", ItemID: "pi-102", Title: "Child 2 of 100", Number: 102, Status: "Done", State: "closed", Assignees: []string{"bob"}, Repo: "test-org/repo", URL: "https://github.com/test-org/repo/issues/102", Parent: &model.ParentRef{Number: 100, Title: "Epic Alpha", Repo: "test-org/repo"}},
			{ID: "n-200", ItemID: "pi-200", Title: "Epic Beta", Number: 200, Status: "Todo", Assignees: []string{"bob"}, Repo: "test-org/repo", URL: "https://github.com/test-org/repo/issues/200"},
			{ID: "n-201", ItemID: "pi-201", Title: "Child 1 of 200", Number: 201, Status: "Todo", Assignees: []string{"bob"}, Repo: "test-org/repo", URL: "https://github.com/test-org/repo/issues/201", Parent: &model.ParentRef{Number: 200, Title: "Epic Beta", Repo: "test-org/repo"}},
			{ID: "n-300", ItemID: "pi-300", Title: "Standalone task", Number: 300, Status: "In Progress", Assignees: []string{"charlie"}, Repo: "test-org/repo", URL: "https://github.com/test-org/repo/issues/300"},
		},
	}
}

// newTestApp creates an app pre-populated with test data (no network).
func newTestApp() AppModel {
	cfg := testConfig()
	app := AppModel{
		config:      cfg,
		configPath:  "/tmp/test-config.yaml",
		command:     NewCommandModel(),
		strategy: grouping.ByEpic{},
		plan:        &model.Plan{},
		annotations: &model.Annotations{},
		inbox:       &model.Inbox{},
	}

	var names []string
	for _, t := range cfg.Team {
		if t.Name != "" {
			names = append(names, t.Name)
		}
		names = append(names, t.Login)
	}
	app.command.SetCompletionNames(names)

	// Pre-populate with test project data
	app.project = testProject()
	app.persons = app.regroup()
	app.board = NewBoardModel(app.persons)
	app.loading = false
	app.statusMsg = "Test loaded"

	return app
}

// sendKeys sends a sequence of key events to the app and returns the final state.
// Supports: literal chars, "enter", "esc", "tab", "shift+tab", "up", "down", "backspace"
func sendKeys(t *testing.T, app AppModel, keys ...string) AppModel {
	t.Helper()
	for _, key := range keys {
		var msg tea.KeyMsg
		switch key {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEscape}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			// Single character or key name like "j", "k", "J", "K", "q", etc.
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		m, _ := app.Update(msg)
		app = m.(AppModel)
	}
	return app
}

// sendCommand types a full command (e.g. `:mv #101 done`) by activating command mode,
// typing the text, and pressing Enter.
func sendCommand(t *testing.T, app AppModel, command string) AppModel {
	t.Helper()
	// Press : to activate command bar
	app = sendKeys(t, app, ":")
	// Type each character
	for _, ch := range command {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
		m, _ := app.Update(msg)
		app = m.(AppModel)
	}
	// Press Enter
	app = sendKeys(t, app, "enter")
	return app
}

func assertView(t *testing.T, app AppModel, expected viewMode) {
	t.Helper()
	names := map[viewMode]string{viewBoard: "board", viewDetail: "detail", viewReview: "review", viewPlan: "plan"}
	if app.view != expected {
		t.Errorf("expected view=%s, got view=%s", names[expected], names[app.view])
	}
}

func assertStatus(t *testing.T, app AppModel, contains string) {
	t.Helper()
	if !strings.Contains(app.statusMsg, contains) {
		t.Errorf("expected statusMsg to contain %q, got %q", contains, app.statusMsg)
	}
}

func assertOpsLen(t *testing.T, app AppModel, expected int) {
	t.Helper()
	if app.ops.Len() != expected {
		t.Errorf("expected %d pending ops, got %d", expected, app.ops.Len())
	}
}

// --- Workflow Tests ---

// Test 1: Plan mode → :h → Esc returns to plan mode (not board)
func TestPlanModeHelpRoundtrip(t *testing.T) {
	app := newTestApp()

	// Switch to plan mode
	app = sendCommand(t, app, "plan")
	assertView(t, app, viewPlan)

	// Open help
	app = sendCommand(t, app, "h")
	assertView(t, app, viewDetail)

	// Esc should return to plan
	app = sendKeys(t, app, "esc")
	assertView(t, app, viewPlan)
}

// Test 2: Board mode → :h → Esc returns to board
func TestBoardModeHelpRoundtrip(t *testing.T) {
	app := newTestApp()
	assertView(t, app, viewBoard)

	app = sendCommand(t, app, "h")
	assertView(t, app, viewDetail)

	app = sendKeys(t, app, "esc")
	assertView(t, app, viewBoard)
}

// Test 3: Team filtering — only configured team members show
func TestTeamFiltering(t *testing.T) {
	app := newTestApp()
	// Should have exactly 3 persons (alice, bob, charlie)
	if len(app.persons) != 3 {
		t.Errorf("expected 3 persons, got %d", len(app.persons))
	}
	// Check names
	names := make([]string, len(app.persons))
	for i, p := range app.persons {
		names[i] = p.Login
	}
	for _, expected := range []string{"alice", "bob", "charlie"} {
		found := false
		for _, n := range names {
			if n == expected {
				found = true
			}
		}
		if !found {
			t.Errorf("expected person %s not found in %v", expected, names)
		}
	}
}

// Test 4: Navigate and drill into issue
func TestDrillIntoIssue(t *testing.T) {
	app := newTestApp()
	assertView(t, app, viewBoard)

	// Navigate down to an issue
	app = sendKeys(t, app, "j")

	// Press enter to drill in (might expand epic first)
	app = sendKeys(t, app, "enter")
	// If we hit an epic header, it expands; press enter again on a child
	if app.view == viewBoard {
		app = sendKeys(t, app, "j", "enter")
	}
	assertView(t, app, viewDetail)

	// Esc returns to board
	app = sendKeys(t, app, "esc")
	assertView(t, app, viewBoard)
}

// Test 5: :mv queues operation locally (doesn't panic without client)
func TestMoveQueuesLocally(t *testing.T) {
	app := newTestApp()
	assertOpsLen(t, app, 0)

	app = sendCommand(t, app, "mv #101 done")
	assertOpsLen(t, app, 1)
	assertStatus(t, app, "Queued")
	assertStatus(t, app, "#101")
}

// Test 6: :c queues comment locally
func TestCommentQueuesLocally(t *testing.T) {
	app := newTestApp()

	// Select an issue first
	app = sendKeys(t, app, "j", "enter") // expand/enter
	if app.view == viewBoard {
		app = sendKeys(t, app, "j", "enter")
	}

	app = sendCommand(t, app, "c \"test comment\"")
	assertOpsLen(t, app, 1)
}

// Test 7: Quit with pending ops shows review, discard exits
func TestQuitWithOpsShowsReview(t *testing.T) {
	app := newTestApp()

	// Create a pending op
	app = sendCommand(t, app, "mv #101 done")
	assertOpsLen(t, app, 1)

	// Press q — should show review
	app = sendKeys(t, app, "q")
	assertView(t, app, viewReview)

	// Press Esc — back to board
	app = sendKeys(t, app, "esc")
	assertView(t, app, viewBoard)
}

// Test 8: Quit with no ops exits immediately (doesn't panic)
func TestQuitNoOps(t *testing.T) {
	app := newTestApp()
	assertOpsLen(t, app, 0)

	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	app = m.(AppModel)
	// Should get a Quit command
	if cmd == nil {
		t.Error("expected Quit command, got nil")
	}
}

// Test 9: Mode switching plan → board → plan
func TestModeSwitching(t *testing.T) {
	app := newTestApp()
	assertView(t, app, viewBoard)

	app = sendCommand(t, app, "plan")
	assertView(t, app, viewPlan)

	app = sendCommand(t, app, "board")
	assertView(t, app, viewBoard)

	app = sendCommand(t, app, "plan")
	assertView(t, app, viewPlan)
}

// Test 10: Planning — :goal adds item, respects max cap
func TestGoalMaxCap(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")

	app = sendCommand(t, app, "goal \"Goal 1\"")
	if len(app.plan.WeekFocus) != 1 {
		t.Errorf("expected 1 goal, got %d", len(app.plan.WeekFocus))
	}

	app = sendCommand(t, app, "goal \"Goal 2\"")
	app = sendCommand(t, app, "goal \"Goal 3\"")
	if len(app.plan.WeekFocus) != 3 {
		t.Errorf("expected 3 goals, got %d", len(app.plan.WeekFocus))
	}

	// 4th should be rejected
	app = sendCommand(t, app, "goal \"Goal 4\"")
	if len(app.plan.WeekFocus) != 3 {
		t.Errorf("expected 3 goals (cap), got %d", len(app.plan.WeekFocus))
	}
	assertStatus(t, app, "full")
}

// Test 11: Planning — :today adds with CreatedAt set
func TestTodayCreatedAt(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, "today \"Do something\"")

	if len(app.plan.Today) != 1 {
		t.Fatalf("expected 1 today item, got %d", len(app.plan.Today))
	}
	if app.plan.Today[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if app.plan.Today[0].Text != "Do something" {
		t.Errorf("expected text 'Do something', got %q", app.plan.Today[0].Text)
	}
}

// Test 12: :note annotates selected issue
func TestNoteAnnotation(t *testing.T) {
	app := newTestApp()

	// Navigate to an issue and drill in
	app = sendKeys(t, app, "j", "enter")
	if app.view == viewBoard {
		app = sendKeys(t, app, "j", "enter")
	}

	app = sendCommand(t, app, "note \"important context\"")
	assertStatus(t, app, "Note added")

	found := false
	for _, a := range app.annotations.Items {
		for _, n := range a.Notes {
			if n == "important context" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected annotation to be saved")
	}
}

// Test 13: :pin adds issue to week focus
func TestPinToWeekFocus(t *testing.T) {
	app := newTestApp()

	app = sendCommand(t, app, "pin #101")
	if len(app.plan.WeekFocus) != 1 {
		t.Fatalf("expected 1 week focus item, got %d", len(app.plan.WeekFocus))
	}
	if app.plan.WeekFocus[0].IssueNum != 101 {
		t.Errorf("expected pinned issue 101, got %d", app.plan.WeekFocus[0].IssueNum)
	}

	// Pinning same issue again should be rejected
	app = sendCommand(t, app, "pin #101")
	if len(app.plan.WeekFocus) != 1 {
		t.Errorf("expected still 1 (duplicate rejected), got %d", len(app.plan.WeekFocus))
	}
	assertStatus(t, app, "already")
}

// Test 14: Tab navigation between team members
func TestTabNavigation(t *testing.T) {
	app := newTestApp()

	// Should start at first person
	if app.board.personIdx != 0 {
		t.Errorf("expected personIdx=0, got %d", app.board.personIdx)
	}

	app = sendKeys(t, app, "tab")
	if app.board.personIdx != 1 {
		t.Errorf("expected personIdx=1 after tab, got %d", app.board.personIdx)
	}

	app = sendKeys(t, app, "tab")
	if app.board.personIdx != 2 {
		t.Errorf("expected personIdx=2 after second tab, got %d", app.board.personIdx)
	}

	// Shift+tab goes back
	app = sendKeys(t, app, "shift+tab")
	if app.board.personIdx != 1 {
		t.Errorf("expected personIdx=1 after shift+tab, got %d", app.board.personIdx)
	}
}

// Test 15: :assign queues assign operation
func TestAssignQueues(t *testing.T) {
	app := newTestApp()

	app = sendCommand(t, app, "a #101 @Alice")
	assertOpsLen(t, app, 1)
	assertStatus(t, app, "assign")
}

// Test 16: :undo removes last op
func TestUndo(t *testing.T) {
	app := newTestApp()

	app = sendCommand(t, app, "mv #101 done")
	assertOpsLen(t, app, 1)

	app = sendCommand(t, app, "undo")
	assertOpsLen(t, app, 0)
	assertStatus(t, app, "Undone")
}

// Test 17: Overdue items get proper age tracking
func TestOverdueTodoItems(t *testing.T) {
	app := newTestApp()

	// Add items with backdated CreatedAt
	app.plan.Today = []model.TodoItem{
		{Text: "Fresh task", CreatedAt: time.Now()},
		{Text: "Yesterday task", CreatedAt: time.Now().Add(-25 * time.Hour)},
		{Text: "Old task", CreatedAt: time.Now().Add(-73 * time.Hour)},
	}

	// Render plan view and check it doesn't panic
	app = sendCommand(t, app, "plan")
	assertView(t, app, viewPlan)

	// Switch to Today tab
	app = sendKeys(t, app, "tab")

	// Verify rendering doesn't panic
	output := app.View()
	if output == "" {
		t.Error("expected non-empty view output")
	}
}

// Test 18: Line number jump in detail view
func TestLineJump(t *testing.T) {
	app := newTestApp()

	// Build detail directly for issue #100 which has children in ChildrenMap
	app.detail = app.buildDetailModel(&app.project.Items[0]) // Epic Alpha #100
	app.prevView = viewBoard
	app.view = viewDetail

	if !app.detail.HasNav() {
		t.Skip("detail view has no nav items for epic #100")
	}

	// Jump to line 2
	app = sendCommand(t, app, "2")
	if app.detail.navCursor != 1 {
		t.Errorf("expected navCursor=1 after :2, got %d", app.detail.navCursor)
	}

	// Jump to line 1
	app = sendCommand(t, app, "1")
	if app.detail.navCursor != 0 {
		t.Errorf("expected navCursor=0 after :1, got %d", app.detail.navCursor)
	}
}

// Test 19: View rendering doesn't panic in any mode
func TestViewRenderingNoPanic(t *testing.T) {
	app := newTestApp()
	app.width = 120
	app.height = 40

	// Board
	output := app.View()
	if output == "" {
		t.Error("board view empty")
	}

	// Plan
	app = sendCommand(t, app, "plan")
	output = app.View()
	if output == "" {
		t.Error("plan view empty")
	}

	// Help
	app = sendCommand(t, app, "h")
	output = app.View()
	if output == "" {
		t.Error("help view empty")
	}

	// Back to plan, then board
	app = sendKeys(t, app, "esc")
	app = sendCommand(t, app, "board")
	output = app.View()
	if output == "" {
		t.Error("board view after roundtrip empty")
	}
}
