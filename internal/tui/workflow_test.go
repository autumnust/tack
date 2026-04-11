package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/autumnust/tack/internal/grouping"
	"github.com/autumnust/tack/internal/model"
	"github.com/autumnust/tack/internal/planning"
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
	app.planView = NewPlanViewModel(app.plan, app.inbox, app.project)

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

// Test 20: Edit scratch note with 'e' key
func TestEditScratchNote(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40

	// Add a scratch note (this should switch to plan view, scratch tab)
	app = sendCommand(t, app, "scratch \"hello world\"")
	assertView(t, app, viewPlan)
	if len(app.plan.Scratch) != 1 {
		t.Fatalf("expected 1 scratch note, got %d", len(app.plan.Scratch))
	}
	if app.planView.section != sectionScratch {
		t.Fatalf("expected scratch section, got %d", app.planView.section)
	}

	// Press 'e' to edit
	app = sendKeys(t, app, "e")
	if !app.planView.IsEditing() {
		t.Fatalf("expected editing=true after 'e', statusMsg=%q", app.statusMsg)
	}
	assertStatus(t, app, "Editing")

	// Type replacement text
	app = sendKeys(t, app, "backspace", "backspace", "backspace", "backspace", "backspace",
		"backspace", "backspace", "backspace", "backspace", "backspace", "backspace",
		"g", "o", "o", "d", "b", "y", "e")
	app = sendKeys(t, app, "enter")

	if app.planView.IsEditing() {
		t.Error("expected editing=false after enter")
	}
	if app.plan.Scratch[0].Text != "goodbye" {
		t.Errorf("expected scratch text 'goodbye', got %q", app.plan.Scratch[0].Text)
	}
}

// Test 21: Edit scratch — simulate real user flow step by step
func TestEditScratchRealFlow(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40

	// Flow A: user starts on board, types :plan, tabs to scratch, adds note, presses e
	app = sendCommand(t, app, "plan")
	assertView(t, app, viewPlan)
	t.Logf("after :plan — view=%d section=%d flatItems=%d", app.view, app.planView.section, len(app.planView.flatItems))

	// Add a scratch note while in plan view
	app = sendCommand(t, app, "scratch \"test note\"")
	t.Logf("after :scratch — view=%d section=%d flatItems=%d cursorIdx=%d scratchLen=%d statusMsg=%q",
		app.view, app.planView.section, len(app.planView.flatItems), app.planView.cursorIdx, len(app.plan.Scratch), app.statusMsg)

	assertView(t, app, viewPlan)
	if app.planView.section != sectionScratch {
		t.Fatalf("expected sectionScratch(%d), got %d", sectionScratch, app.planView.section)
	}
	if len(app.planView.flatItems) == 0 {
		t.Fatal("flatItems is empty — cursor has nothing to select")
	}

	// Now press 'e' — send as a single rune
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}
	t.Logf("sending key: type=%d runes=%q string=%q", msg.Type, string(msg.Runes), msg.String())
	m, _ := app.Update(msg)
	app = m.(AppModel)
	t.Logf("after 'e' — editing=%v statusMsg=%q", app.planView.IsEditing(), app.statusMsg)

	if !app.planView.IsEditing() {
		t.Fatalf("edit mode not activated — statusMsg=%q", app.statusMsg)
	}

	// Flow B: user starts on board, types :scratch (never :plan first)
	app2 := newTestApp()
	app2.width = 80
	app2.height = 40
	app2 = sendCommand(t, app2, "scratch \"from board\"")
	t.Logf("Flow B after :scratch — view=%d section=%d flatItems=%d cursorIdx=%d",
		app2.view, app2.planView.section, len(app2.planView.flatItems), app2.planView.cursorIdx)

	m2, _ := app2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	app2 = m2.(AppModel)
	t.Logf("Flow B after 'e' — editing=%v statusMsg=%q", app2.planView.IsEditing(), app2.statusMsg)

	if !app2.planView.IsEditing() {
		t.Fatalf("Flow B: edit mode not activated — statusMsg=%q", app2.statusMsg)
	}

	// Flow C: scratch notes pre-exist on disk, user does :plan, tabs to scratch, presses e
	app3 := newTestApp()
	app3.width = 80
	app3.height = 40
	app3.plan.Scratch = []model.ScratchNote{
		{Text: "pre-existing note", CreatedAt: time.Now().Add(-1 * time.Hour)},
	}
	app3 = sendCommand(t, app3, "plan")
	assertView(t, app3, viewPlan)
	// Tab to scratch tab (WeekFocus -> Today -> Inbox -> Scratch)
	app3 = sendKeys(t, app3, "tab", "tab", "tab")
	t.Logf("Flow C after tabs — section=%d flatItems=%d cursorIdx=%d",
		app3.planView.section, len(app3.planView.flatItems), app3.planView.cursorIdx)
	if app3.planView.section != sectionScratch {
		t.Fatalf("Flow C: expected sectionScratch, got %d", app3.planView.section)
	}

	m3, _ := app3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	app3 = m3.(AppModel)
	t.Logf("Flow C after 'e' — editing=%v statusMsg=%q", app3.planView.IsEditing(), app3.statusMsg)
	if !app3.planView.IsEditing() {
		t.Fatalf("Flow C: edit mode not activated — statusMsg=%q", app3.statusMsg)
	}

	// Flow D: exact --plan startup simulation
	// Mimic NewApp with startInPlanMode=true, pre-existing scratch notes
	appD := newTestApp()
	appD.plan.Scratch = []model.ScratchNote{
		{Text: "existing note from disk", CreatedAt: time.Now().Add(-2 * time.Hour)},
	}
	// Re-create planView exactly like NewApp does when --plan
	appD.planView = NewPlanViewModel(appD.plan, appD.inbox, appD.project)
	appD.view = viewPlan
	appD.statusMsg = "Planning mode"

	// Simulate WindowSizeMsg (first msg in real terminal)
	mD, _ := appD.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	appD = mD.(AppModel)
	t.Logf("Flow D after WindowSizeMsg — width=%d height=%d view=%d", appD.width, appD.height, appD.view)

	// Tab to scratch: WeekFocus(0) -> Today(1) -> Inbox(2) -> Scratch(3)
	appD = sendKeys(t, appD, "tab", "tab", "tab")
	t.Logf("Flow D after tabs — view=%d section=%d flatItems=%d cursorIdx=%d scratchLen=%d",
		appD.view, appD.planView.section, len(appD.planView.flatItems), appD.planView.cursorIdx, len(appD.plan.Scratch))

	// Render to verify scratch note is visible
	output := appD.View()
	if !strings.Contains(output, "existing note from disk") {
		t.Logf("Flow D rendered view:\n%s", output)
		t.Fatal("Flow D: scratch note not visible in rendered output")
	}

	// Press 'e'
	mD2, _ := appD.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	appD = mD2.(AppModel)
	t.Logf("Flow D after 'e' — editing=%v statusMsg=%q view=%d", appD.planView.IsEditing(), appD.statusMsg, appD.view)

	if !appD.planView.IsEditing() {
		t.Logf("Flow D full state: view=%d section=%d flatItems=%d cursorIdx=%d editing=%v",
			appD.view, appD.planView.section, len(appD.planView.flatItems), appD.planView.cursorIdx, appD.planView.editing)
		t.Fatalf("Flow D: edit mode not activated — statusMsg=%q", appD.statusMsg)
	}
}

// --- Helper for tests that need planning store ---

func newTestAppWithStore(t *testing.T) AppModel {
	t.Helper()
	app := newTestApp()
	store, err := planning.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.planStore = store
	return app
}

// =====================================================
// Phase 4: Workflow test expansion
// =====================================================

// --- Command targeting tests ---

// Test 22: :mv from board cursor (no #N)
func TestMvFromCursor(t *testing.T) {
	app := newTestApp()
	assertView(t, app, viewBoard)

	// Navigate down to an issue (skip epic header)
	app = sendKeys(t, app, "j", "j")

	// Move via cursor target
	app = sendCommand(t, app, "mv done")
	assertOpsLen(t, app, 1)
	assertStatus(t, app, "Queued")
}

// Test 23: :mv with invalid status
func TestMvInvalidStatus(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "mv #101 bogus")
	assertStatus(t, app, "Unknown status")
	assertOpsLen(t, app, 0)
}

// Test 24: :mv with no args
func TestMvNoArgs(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "mv")
	assertStatus(t, app, "Usage")
}

// Test 25: :c #N "comment" (explicit issue from board)
func TestCommentExplicitIssue(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `c #101 "test comment"`)
	assertOpsLen(t, app, 1)
	assertStatus(t, app, "#101")
}

// Test 26: :c from board cursor
func TestCommentFromBoardCursor(t *testing.T) {
	app := newTestApp()
	// Navigate to a non-epic-header issue
	app = sendKeys(t, app, "j", "j")
	app = sendCommand(t, app, `c "cursor comment"`)
	assertOpsLen(t, app, 1)
	assertStatus(t, app, "Queued")
}

// --- Team management tests ---

// Test 27: :add member
func TestAddMember(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "add @dave")
	if len(app.persons) != 4 {
		t.Errorf("expected 4 persons after add, got %d", len(app.persons))
	}
	assertStatus(t, app, "Added dave")
}

// Test 28: :add duplicate
func TestAddMemberDuplicate(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "add @alice")
	assertStatus(t, app, "already")
}

// Test 29: :rm member
func TestRemoveMember(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "rm @charlie")
	if len(app.persons) != 2 {
		t.Errorf("expected 2 persons after remove, got %d", len(app.persons))
	}
	assertStatus(t, app, "Removed charlie")
}

// Test 30: :rm not found
func TestRemoveMemberNotFound(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "rm @nobody")
	assertStatus(t, app, "not in team")
}

// Test 31: :rm current person clamps personIdx (Bug 3 fix validation)
func TestRemoveCurrentPerson_ClampIdx(t *testing.T) {
	app := newTestApp()
	// Tab to person index 2 (charlie)
	app = sendKeys(t, app, "tab", "tab")
	if app.board.personIdx != 2 {
		t.Fatalf("expected personIdx=2, got %d", app.board.personIdx)
	}

	app = sendCommand(t, app, "rm @charlie")
	// personIdx should be clamped to valid range
	if app.board.personIdx >= len(app.persons) {
		t.Errorf("personIdx %d out of bounds (len=%d)", app.board.personIdx, len(app.persons))
	}
	// Board should render without panic
	output := app.View()
	if output == "" {
		t.Error("expected non-empty view after removing current person")
	}
}

// --- Grouping tests ---

// Test 32: :group label:<prefix>
func TestGroupLabel(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "group label:area:")
	assertStatus(t, app, "label:area:")
}

// Test 33: :group epic (back to default)
func TestGroupEpic(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "group label:x")
	app = sendCommand(t, app, "group epic")
	assertStatus(t, app, "epic")
}

// Test 34: :group unknown
func TestGroupUnknown(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "group bogus")
	assertStatus(t, app, "Unknown grouping")
}

// Test 35: unknown command
func TestUnknownCommand(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "asdf")
	assertStatus(t, app, "Unknown command")
}

// --- Focus tests ---

// Test 36: :focus N
func TestFocusAdd(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "focus 999")
	assertOpsLen(t, app, 1)
	if !containsInt(app.config.Focus, 999) {
		t.Error("expected 999 in config.Focus")
	}
}

// Test 37: :focus @Name N
func TestFocusPersonal(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "focus @alice 999")
	assertOpsLen(t, app, 1)
	found := false
	for _, t2 := range app.config.Team {
		if t2.Login == "alice" && containsInt(t2.Focus, 999) {
			found = true
		}
	}
	if !found {
		t.Error("expected 999 in alice's focus")
	}
}

// Test 38: :unfocus N
func TestUnfocusNumber(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "focus 100")
	app = sendCommand(t, app, "unfocus 100")
	if containsInt(app.config.Focus, 100) {
		t.Error("expected 100 removed from config.Focus")
	}
}

// Test 39: :focus clear
func TestFocusClear(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "focus 100")
	app = sendCommand(t, app, "focus clear")
	if len(app.config.Focus) != 0 {
		t.Errorf("expected empty focus after clear, got %v", app.config.Focus)
	}
}

// Test 40: :focus save
func TestFocusSave(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "focus save")
	assertOpsLen(t, app, 1)
	assertStatus(t, app, "save focus")
}

// --- Planning: sub/promote/done/del ---

// Test 41: :sub adds breakdown item
func TestSubAddBreakdownItem(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "Ship v1"`)

	// Navigate to the goal (first item in week focus)
	// Already on it after adding
	app = sendCommand(t, app, `sub "Write tests"`)
	if len(app.plan.WeekFocus) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(app.plan.WeekFocus))
	}
	if len(app.plan.WeekFocus[0].SubItems) != 1 {
		t.Fatalf("expected 1 sub-item, got %d", len(app.plan.WeekFocus[0].SubItems))
	}
	if app.plan.WeekFocus[0].SubItems[0].Text != "Write tests" {
		t.Errorf("expected sub text 'Write tests', got %q", app.plan.WeekFocus[0].SubItems[0].Text)
	}
}

// Test 42: :promote moves sub-item to today
func TestPromoteToToday(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "Ship v1"`)
	app = sendCommand(t, app, `sub "Write tests"`)

	// Navigate to the sub-item (j from goal header)
	app = sendKeys(t, app, "j")

	app = sendCommand(t, app, "promote")
	if len(app.plan.Today) != 1 {
		t.Fatalf("expected 1 today item after promote, got %d", len(app.plan.Today))
	}
	if app.plan.Today[0].Text != "Write tests" {
		t.Errorf("expected promoted text 'Write tests', got %q", app.plan.Today[0].Text)
	}
	assertStatus(t, app, "Promoted")
}

// Test 43: :done toggles today item by cursor
func TestDoneToggle(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "Task A"`)

	// Switch to today tab
	app = sendKeys(t, app, "tab")

	app = sendCommand(t, app, "done")
	if !app.plan.Today[0].Done {
		t.Error("expected item to be done")
	}
	assertStatus(t, app, "Completed")

	// Toggle back
	app = sendCommand(t, app, "done")
	if app.plan.Today[0].Done {
		t.Error("expected item to be undone")
	}
	assertStatus(t, app, "Uncompleted")
}

// Test 44: :done N by number
func TestDoneByNumber(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "A"`)
	app = sendCommand(t, app, `today "B"`)
	app = sendCommand(t, app, `today "C"`)

	app = sendCommand(t, app, "done 2")
	if !app.plan.Today[1].Done {
		t.Error("expected item 2 to be done")
	}
	if app.plan.Today[0].Done || app.plan.Today[2].Done {
		t.Error("only item 2 should be toggled")
	}
}

// Test 45: :done invalid number
func TestDoneInvalidNumber(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "A"`)
	app = sendCommand(t, app, "done 99")
	assertStatus(t, app, "Invalid")
}

// Test 46: :del week focus — cursor-based with sub-items (Bug 1 fix validation)
func TestDeleteWeekFocusCursorBased(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "Goal A"`)
	app = sendCommand(t, app, `sub "Sub 1"`)
	app = sendCommand(t, app, `goal "Goal B"`)

	// flatItems: [Goal A, Sub 1, Goal B] — cursor starts at 0 after last add refreshes
	// Navigate to Goal B (index 2 in flat list)
	app = sendKeys(t, app, "j", "j")

	app = sendCommand(t, app, "del")
	if len(app.plan.WeekFocus) != 1 {
		t.Fatalf("expected 1 goal after delete, got %d", len(app.plan.WeekFocus))
	}
	if app.plan.WeekFocus[0].Text != "Goal A" {
		t.Errorf("expected 'Goal A' to remain, got %q", app.plan.WeekFocus[0].Text)
	}
	assertStatus(t, app, "Removed from week focus")
}

// Test 47: :del sub-item (Bug 1 fix validation)
func TestDeleteWeekFocusSubItem(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "Goal A"`)
	app = sendCommand(t, app, `sub "Sub 1"`)
	app = sendCommand(t, app, `sub "Sub 2"`)

	// flatItems: [Goal A, Sub 1, Sub 2]
	// Navigate to Sub 1 (index 1)
	app = sendKeys(t, app, "j")

	app = sendCommand(t, app, "del")
	if len(app.plan.WeekFocus[0].SubItems) != 1 {
		t.Fatalf("expected 1 sub-item after delete, got %d", len(app.plan.WeekFocus[0].SubItems))
	}
	if app.plan.WeekFocus[0].SubItems[0].Text != "Sub 2" {
		t.Errorf("expected 'Sub 2' to remain, got %q", app.plan.WeekFocus[0].SubItems[0].Text)
	}
	assertStatus(t, app, "Removed breakdown item")
}

// Test 48: :del today item
func TestDeleteTodayItem(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "A"`)
	app = sendCommand(t, app, `today "B"`)

	// Switch to today tab
	app = sendKeys(t, app, "tab")

	app = sendCommand(t, app, "del")
	if len(app.plan.Today) != 1 {
		t.Fatalf("expected 1 today item after delete, got %d", len(app.plan.Today))
	}
}

// Test 49: :del scratch item
func TestDeleteScratchItem(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `scratch "note"`)
	// Already in plan view, scratch tab
	app = sendCommand(t, app, "del")
	if len(app.plan.Scratch) != 0 {
		t.Errorf("expected 0 scratch after delete, got %d", len(app.plan.Scratch))
	}
}

// Test 50: :del last item clamps cursor
func TestDeleteLastItemClampsCursor(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "only item"`)
	app = sendKeys(t, app, "tab") // today tab

	app = sendCommand(t, app, "del")
	if len(app.plan.Today) != 0 {
		t.Fatal("expected 0 items")
	}
	// Should not panic on View
	output := app.View()
	if output == "" {
		t.Error("expected non-empty view after deleting last item")
	}
}

// Test 51: :del by line number
func TestDeleteByLineNumber(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "A"`)
	app = sendCommand(t, app, `today "B"`)
	app = sendCommand(t, app, `today "C"`)
	// Switch to today tab so :del operates on today section
	app = sendKeys(t, app, "tab")

	app = sendCommand(t, app, "del 2")
	if len(app.plan.Today) != 2 {
		t.Fatalf("expected 2 items after del 2, got %d", len(app.plan.Today))
	}
	if app.plan.Today[0].Text != "A" || app.plan.Today[1].Text != "C" {
		t.Errorf("expected [A, C], got [%s, %s]", app.plan.Today[0].Text, app.plan.Today[1].Text)
	}
}

// Test 52: :inbox clear
func TestInboxClear(t *testing.T) {
	app := newTestApp()
	app.inbox = &model.Inbox{Items: []model.InboxItem{
		{Text: "item 1"},
		{Text: "item 2"},
	}}
	app = sendCommand(t, app, "inbox clear")
	if len(app.inbox.Items) != 0 {
		t.Errorf("expected empty inbox, got %d items", len(app.inbox.Items))
	}
	assertStatus(t, app, "Inbox cleared")
}

// --- Plan reorder tests ---

// Test 53: K moves item up in today
func TestReorderMoveUp(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "A"`)
	app = sendCommand(t, app, `today "B"`)
	app = sendKeys(t, app, "tab") // today tab
	app = sendKeys(t, app, "j")   // cursor on B

	app = sendKeys(t, app, "K")
	if app.plan.Today[0].Text != "B" || app.plan.Today[1].Text != "A" {
		t.Errorf("expected [B, A] after K, got [%s, %s]", app.plan.Today[0].Text, app.plan.Today[1].Text)
	}
}

// Test 54: J moves item down in today
func TestReorderMoveDown(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "A"`)
	app = sendCommand(t, app, `today "B"`)
	app = sendKeys(t, app, "tab") // today tab
	// cursor on A (index 0)

	app = sendKeys(t, app, "J")
	if app.plan.Today[0].Text != "B" || app.plan.Today[1].Text != "A" {
		t.Errorf("expected [B, A] after J, got [%s, %s]", app.plan.Today[0].Text, app.plan.Today[1].Text)
	}
}

// --- Edit tests ---

// Test 55: edit today item
func TestEditTodayItem(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "old text"`)
	app = sendKeys(t, app, "tab") // today tab

	app = sendKeys(t, app, "e")
	if !app.planView.IsEditing() {
		t.Fatalf("expected editing mode, statusMsg=%q", app.statusMsg)
	}
	// Clear and type new text
	for i := 0; i < 8; i++ {
		app = sendKeys(t, app, "backspace")
	}
	app = sendKeys(t, app, "n", "e", "w")
	app = sendKeys(t, app, "enter")

	if app.planView.IsEditing() {
		t.Error("expected editing=false after enter")
	}
	if app.plan.Today[0].Text != "new" {
		t.Errorf("expected 'new', got %q", app.plan.Today[0].Text)
	}
}

// Test 56: edit goal (freeform, not pinned)
func TestEditGoalItem(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "old goal"`)

	app = sendKeys(t, app, "e")
	if !app.planView.IsEditing() {
		t.Fatalf("expected editing mode, statusMsg=%q", app.statusMsg)
	}
	app = sendKeys(t, app, "enter") // confirm without changes
	if app.planView.IsEditing() {
		t.Error("expected editing ended")
	}
}

// Test 57: edit pinned issue rejected
func TestEditPinnedIssueRejected(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, "pin #101")
	// Cursor should be on the pinned item
	app = sendKeys(t, app, "e")
	if app.planView.IsEditing() {
		t.Error("should not be able to edit linked issue")
	}
	assertStatus(t, app, "Cannot edit")
}

// --- Review screen tests ---

// Test 58: review navigation
func TestReviewNavigation(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "mv #101 done")
	app = sendCommand(t, app, "mv #201 done")
	app = sendKeys(t, app, "q")
	assertView(t, app, viewReview)

	if app.review.cursorIdx != 0 {
		t.Errorf("expected cursor at 0, got %d", app.review.cursorIdx)
	}
	app = sendKeys(t, app, "j")
	if app.review.cursorIdx != 1 {
		t.Errorf("expected cursor at 1 after j, got %d", app.review.cursorIdx)
	}
	app = sendKeys(t, app, "k")
	if app.review.cursorIdx != 0 {
		t.Errorf("expected cursor at 0 after k, got %d", app.review.cursorIdx)
	}
}

// Test 59: review toggle
func TestReviewToggle(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "mv #101 done")
	app = sendKeys(t, app, "q")
	assertView(t, app, viewReview)

	// Initially checked
	ops := app.ops.Ops()
	if !ops[0].Checked {
		t.Error("expected op to be checked initially")
	}

	// Toggle with enter
	app = sendKeys(t, app, "enter")
	ops = app.ops.Ops()
	if ops[0].Checked {
		t.Error("expected op unchecked after enter")
	}
}

// Test 60: review check all / uncheck all
func TestReviewCheckAll(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "mv #101 done")
	app = sendCommand(t, app, "mv #201 done")
	app = sendKeys(t, app, "q")
	assertView(t, app, viewReview)

	// Uncheck all
	app = sendKeys(t, app, "n")
	if len(app.ops.CheckedOps()) != 0 {
		t.Error("expected 0 checked after n")
	}

	// Check all
	app = sendKeys(t, app, "a")
	if len(app.ops.CheckedOps()) != 2 {
		t.Errorf("expected 2 checked after a, got %d", len(app.ops.CheckedOps()))
	}
}

// Test 61: review discard
func TestReviewDiscard(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "mv #101 done")
	app = sendKeys(t, app, "q")
	assertView(t, app, viewReview)

	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	_ = m.(AppModel)
	if cmd == nil {
		t.Error("expected Quit command from discard")
	}
}

// --- Board interaction tests ---

// Test 62: board cursor stays valid after :mv
func TestBoardCursorAfterMv(t *testing.T) {
	app := newTestApp()
	app.width = 120
	app.height = 40

	app = sendCommand(t, app, "mv #101 done")
	// Board should render without panic
	output := app.View()
	if output == "" {
		t.Error("expected non-empty view after mv")
	}
}

// Test 63: selectedTarget from board
func TestBoardSelectedTargetFromBoard(t *testing.T) {
	app := newTestApp()
	// First item might be an epic header, navigate to a child
	app = sendKeys(t, app, "j", "j")

	target := app.selectedTarget()
	if target == nil {
		t.Fatal("expected selectedTarget to return an issue from board cursor")
	}
}

// Test 64: refresh key
func TestRefreshKey(t *testing.T) {
	app := newTestApp()
	app = sendKeys(t, app, "r")
	if !app.loading {
		t.Error("expected loading=true after r")
	}
	assertStatus(t, app, "Refreshing")
}

// Test 65: empty board (zero items)
func TestEmptyBoard(t *testing.T) {
	app := newTestApp()
	app.project = &model.Project{
		StatusField: model.FieldInfo{
			Options: []model.FieldOption{
				{Name: "Todo"}, {Name: "In Progress"}, {Name: "Done"},
			},
		},
	}
	app.persons = nil
	app.board = NewBoardModel(nil)
	app.width = 120
	app.height = 40

	// Should not panic
	output := app.View()
	if output == "" {
		t.Error("expected non-empty view for empty board")
	}
}

// Test 66: :stats command switches to detail
func TestStatsCommand(t *testing.T) {
	app := newTestAppWithStore(t)
	app.planStore.LogUsage(":mv")
	app.planStore.LogUsage(":c")

	app = sendCommand(t, app, "stats")
	assertView(t, app, viewDetail)
}

// Test 67: :recap command switches to detail
func TestRecapCommand(t *testing.T) {
	app := newTestAppWithStore(t)
	app.width = 120
	app.height = 40

	app = sendCommand(t, app, "recap")
	assertView(t, app, viewDetail)
	assertStatus(t, app, "Recap saved")
}

// Test 68: :del outside plan mode
func TestDeleteOutsidePlanMode(t *testing.T) {
	app := newTestApp()
	assertView(t, app, viewBoard)
	app = sendCommand(t, app, "del")
	assertStatus(t, app, "only works in planning mode")
}

// Test 69: rendering review screen
func TestReviewRendering(t *testing.T) {
	app := newTestApp()
	app.width = 120
	app.height = 40
	app = sendCommand(t, app, "mv #101 done")
	app = sendCommand(t, app, `c #101 "test"`)
	app = sendKeys(t, app, "q")
	assertView(t, app, viewReview)

	output := app.View()
	if output == "" {
		t.Error("review view empty")
	}
}

// Test 70: :mv fuzzy status matching
func TestMvFuzzyStatusMatch(t *testing.T) {
	app := newTestApp()
	// "d" should match "Done" (prefix match)
	app = sendCommand(t, app, "mv #101 d")
	assertOpsLen(t, app, 1)
	assertStatus(t, app, "Done")
}

// Test 71: :del inbox item
func TestDeleteInboxItem(t *testing.T) {
	app := newTestApp()
	app.inbox = &model.Inbox{Items: []model.InboxItem{
		{Text: "item 1"},
		{Text: "item 2"},
	}}
	app = sendCommand(t, app, "plan")
	// Tab to inbox (WeekFocus -> Today -> Inbox)
	app = sendKeys(t, app, "tab", "tab")
	app.planView.SetData(app.plan, app.inbox, app.project)

	app = sendCommand(t, app, "del")
	if len(app.inbox.Items) != 1 {
		t.Errorf("expected 1 inbox item after delete, got %d", len(app.inbox.Items))
	}
}

// Test 72: view rendering in review mode doesn't panic
func TestViewAllModesNoPanic(t *testing.T) {
	app := newTestApp()
	app.width = 120
	app.height = 40

	// Board
	assertView(t, app, viewBoard)
	_ = app.View()

	// Plan
	app = sendCommand(t, app, "plan")
	_ = app.View()

	// Plan tabs
	app = sendKeys(t, app, "tab") // Today
	_ = app.View()
	app = sendKeys(t, app, "tab") // Inbox
	_ = app.View()
	app = sendKeys(t, app, "tab") // Scratch
	_ = app.View()

	// Board + ops + review
	app = sendCommand(t, app, "board")
	app = sendCommand(t, app, "mv #101 done")
	app = sendKeys(t, app, "q")
	assertView(t, app, viewReview)
	_ = app.View()
}

// =====================================================
// Init() startup path tests
// =====================================================

// Test 73: Plan mode + no cache → Init() should NOT fetch
func TestInitPlanModeNoCache(t *testing.T) {
	app := newTestApp()
	app.view = viewPlan
	app.project = nil // no cache
	app.client = nil  // no GitHub client
	app.loading = true
	app.cacheStale = false

	cmd := app.Init()
	if cmd != nil {
		t.Error("Init() in plan mode with no cache should return nil (no fetch), got non-nil cmd")
	}
	// Note: Init() uses value receiver so m.loading=false doesn't persist,
	// but View() handles this with: m.loading && m.view != viewPlan
}

// Test 74: Plan mode + stale cache → Init() fetches in background
func TestInitPlanModeStaleCacheFetches(t *testing.T) {
	app := newTestApp()
	app.view = viewPlan
	// project is populated (from newTestApp), simulate stale cache
	app.cacheStale = true

	cmd := app.Init()
	if cmd == nil {
		t.Error("Init() in plan mode with stale cache should fetch in background")
	}
}

// Test 75: Plan mode + fresh cache → Init() does nothing
func TestInitPlanModeFreshCache(t *testing.T) {
	app := newTestApp()
	app.view = viewPlan
	app.cacheStale = false
	app.loading = false

	cmd := app.Init()
	if cmd != nil {
		t.Error("Init() in plan mode with fresh cache should return nil")
	}
}

// Test 76: Board mode + no cache → Init() fetches
func TestInitBoardModeNoCache(t *testing.T) {
	app := newTestApp()
	app.view = viewBoard
	app.loading = true // no cache loaded

	cmd := app.Init()
	if cmd == nil {
		t.Error("Init() in board mode with no cache should return fetch cmd")
	}
}

// Test 77: fetchData with nil client returns error, doesn't panic
func TestFetchDataNilClient(t *testing.T) {
	app := newTestApp()
	app.client = nil

	cmd := app.fetchData()
	if cmd == nil {
		t.Fatal("expected non-nil cmd from fetchData")
	}
	msg := cmd()
	done, ok := msg.(fetchDoneMsg)
	if !ok {
		t.Fatalf("expected fetchDoneMsg, got %T", msg)
	}
	if done.err == nil {
		t.Error("expected error from fetchData with nil client")
	}
	if !strings.Contains(done.err.Error(), "not available") {
		t.Errorf("expected 'not available' in error, got: %s", done.err)
	}
}
