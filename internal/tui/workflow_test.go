package tui

import (
	"os"
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
			{ID: "n-100", ItemID: "pi-100", Title: "Epic Alpha", Number: 100, Status: "In Progress", Assignees: []string{"alice"}, Repo: "test-org/repo", URL: "https://github.com/test-org/repo/issues/100", Body: "This epic covers the Alpha milestone deliverables."},
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
	}

	var names []string
	for _, t := range cfg.Team {
		if t.Name != "" {
			names = append(names, t.Name)
		}
		names = append(names, t.Login)
	}
	app.command.SetCompletionNames(names)
	app.planView = NewPlanViewModel(app.plan, app.project)

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
	app = sendKeys(t, app, "q", "q")
	assertView(t, app, viewReview)

	// Press Esc — back to board
	app = sendKeys(t, app, "esc")
	assertView(t, app, viewBoard)
}

// Test 8: Quit with no ops exits immediately (doesn't panic)
func TestQuitNoOps(t *testing.T) {
	app := newTestApp()
	assertOpsLen(t, app, 0)

	// First q prompts confirmation
	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	app = m.(AppModel)
	if cmd != nil {
		t.Error("expected no command on first q (confirmation prompt)")
	}

	// Second q confirms quit
	m, cmd = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	app = m.(AppModel)
	if cmd == nil {
		t.Error("expected Quit command on second q, got nil")
	}
}

// Test 8b: Quit confirmation cancelled by other key
func TestQuitConfirmCancel(t *testing.T) {
	app := newTestApp()

	// First q shows confirmation
	app = sendKeys(t, app, "q")
	assertStatus(t, app, "Quit?")

	// Any other key cancels
	app = sendKeys(t, app, "j")
	if app.confirmQuit {
		t.Error("expected confirmQuit to be cleared")
	}
	assertView(t, app, viewBoard)
}

// Test 8c: Quit confirmed with y
func TestQuitConfirmWithY(t *testing.T) {
	app := newTestApp()

	app = sendKeys(t, app, "q")
	assertStatus(t, app, "Quit?")

	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	_ = m.(AppModel)
	if cmd == nil {
		t.Error("expected Quit command on y confirm, got nil")
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

// Test 10b: Max cap does not count finished focus items (:goal)
func TestGoalMaxCapIgnoresDone(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")

	app = sendCommand(t, app, "goal \"Goal 1\"")
	app = sendCommand(t, app, "goal \"Goal 2\"")
	app = sendCommand(t, app, "goal \"Goal 3\"")
	if len(app.plan.WeekFocus) != 3 {
		t.Fatalf("expected 3 goals, got %d", len(app.plan.WeekFocus))
	}

	// Mark two as done
	app.plan.WeekFocus[0].Done = true
	app.plan.WeekFocus[1].Done = true

	// Should be able to add two more (only 1 active)
	app = sendCommand(t, app, "goal \"Goal 4\"")
	if len(app.plan.WeekFocus) != 4 {
		t.Errorf("expected 4 goals (2 done + 2 active), got %d", len(app.plan.WeekFocus))
	}

	app = sendCommand(t, app, "goal \"Goal 5\"")
	if len(app.plan.WeekFocus) != 5 {
		t.Errorf("expected 5 goals (2 done + 3 active), got %d", len(app.plan.WeekFocus))
	}

	// Now at cap again (3 active) — should be rejected
	app = sendCommand(t, app, "goal \"Goal 6\"")
	if len(app.plan.WeekFocus) != 5 {
		t.Errorf("expected 5 goals (cap on active), got %d", len(app.plan.WeekFocus))
	}
	assertStatus(t, app, "full")
}

// Test 10c: Max cap does not count finished focus items (:pin)
func TestPinMaxCapIgnoresDone(t *testing.T) {
	app := newTestApp()

	app = sendCommand(t, app, "pin #101")
	app = sendCommand(t, app, "pin #102")
	app = sendCommand(t, app, "pin #200")
	if len(app.plan.WeekFocus) != 3 {
		t.Fatalf("expected 3 pinned items, got %d", len(app.plan.WeekFocus))
	}

	// Mark two as done
	app.plan.WeekFocus[0].Done = true
	app.plan.WeekFocus[1].Done = true

	// Should be able to pin more (only 1 active)
	app = sendCommand(t, app, "pin #201")
	if len(app.plan.WeekFocus) != 4 {
		t.Errorf("expected 4 items (2 done + 2 active), got %d", len(app.plan.WeekFocus))
	}
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

// Test 20: Hibana note via :hibana command
func TestHibanaAddNote(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40

	// Add a note via :hibana "text"
	app = sendCommand(t, app, "hibana \"hello world\"")
	assertView(t, app, viewPlan)
	if len(app.plan.Scratch) != 1 {
		t.Fatalf("expected 1 note, got %d", len(app.plan.Scratch))
	}
	if app.planView.section != sectionHibana {
		t.Fatalf("expected hibana section, got %d", app.planView.section)
	}
	if app.plan.Scratch[0].Text != "hello world" {
		t.Errorf("expected text 'hello world', got %q", app.plan.Scratch[0].Text)
	}
}

// Test 21: Hibana editor flow — 'e' key returns a command (vim), editorFinishedMsg updates note
func TestHibanaEditorFlow(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40

	// Add a note and navigate to it
	app = sendCommand(t, app, "hibana \"test note\"")
	assertView(t, app, viewPlan)
	if app.planView.section != sectionHibana {
		t.Fatalf("expected sectionHibana, got %d", app.planView.section)
	}

	// Press 'e' — launches vim (returns a command)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}
	m, cmd := app.Update(msg)
	app = m.(AppModel)
	if cmd == nil {
		t.Fatal("expected a command (editor launch) from 'e' key")
	}

	// Simulate editorFinishedMsg with updated text
	tmpFile := t.TempDir() + "/note.md"
	os.WriteFile(tmpFile, []byte("updated note\nwith newlines"), 0644)
	m2, _ := app.Update(editorFinishedMsg{tmpPath: tmpFile, section: sectionHibana, idx: 0, subIdx: -1, err: nil})
	app = m2.(AppModel)

	if app.plan.Scratch[0].Text != "updated note\nwith newlines" {
		t.Errorf("expected updated text, got %q", app.plan.Scratch[0].Text)
	}
	assertStatus(t, app, "Note updated")

	// Tab navigation to Hibana (WeekFocus -> Today -> Hibana)
	app2 := newTestApp()
	app2.plan.Scratch = []model.ScratchNote{
		{Text: "existing note", CreatedAt: time.Now().Add(-1 * time.Hour)},
	}
	app2.planView = NewPlanViewModel(app2.plan, app2.project)
	app2.view = viewPlan
	mD, _ := app2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app2 = mD.(AppModel)

	app2 = sendKeys(t, app2, "tab", "tab")
	if app2.planView.section != sectionHibana {
		t.Fatalf("expected sectionHibana after 2 tabs, got %d", app2.planView.section)
	}

	// Render to verify note is visible
	output := app2.View()
	if !strings.Contains(output, "existing note") {
		t.Fatal("note not visible in rendered output")
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

	// Show done items, then toggle back
	app = sendKeys(t, app, "x")
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

// --- Plan move tests ---

// Test: :mv cursor item from hibana to today
func TestPlanMoveCursorHibanaToToday(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `hibana "deploy pipeline"`)
	app = sendCommand(t, app, `hibana "review PR"`)

	// On hibana tab, cursor at item 1
	app = sendCommand(t, app, `mv today`)
	if len(app.plan.Scratch) != 1 {
		t.Fatalf("expected 1 scratch note after move, got %d", len(app.plan.Scratch))
	}
	if len(app.plan.Today) != 1 {
		t.Fatalf("expected 1 today item, got %d", len(app.plan.Today))
	}
	if app.plan.Today[0].Text != "deploy pipeline" {
		t.Errorf("expected 'deploy pipeline' in today, got %q", app.plan.Today[0].Text)
	}
	assertStatus(t, app, "Moved to Today")
}

// Test: :mv by line number from hibana to goal
func TestPlanMoveByNumberHibanaToGoal(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `hibana "idea A"`)
	app = sendCommand(t, app, `hibana "idea B"`)

	app = sendCommand(t, app, `mv 2 goal`)
	if len(app.plan.Scratch) != 1 {
		t.Fatalf("expected 1 scratch note after move, got %d", len(app.plan.Scratch))
	}
	if len(app.plan.WeekFocus) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(app.plan.WeekFocus))
	}
	if app.plan.WeekFocus[0].Text != "idea B" {
		t.Errorf("expected 'idea B' in goals, got %q", app.plan.WeekFocus[0].Text)
	}
}

// Test: :mv to goal sub-item
func TestPlanMoveToGoalSubItem(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "Ship v1"`)

	// Switch to hibana and add a note
	app = sendKeys(t, app, "tab", "tab") // Today -> Hibana
	app = sendCommand(t, app, `hibana "write docs"`)

	app = sendCommand(t, app, `mv goal 1`)
	if len(app.plan.Scratch) != 0 {
		t.Fatalf("expected 0 scratch notes after move, got %d", len(app.plan.Scratch))
	}
	if len(app.plan.WeekFocus[0].SubItems) != 1 {
		t.Fatalf("expected 1 sub-item, got %d", len(app.plan.WeekFocus[0].SubItems))
	}
	if app.plan.WeekFocus[0].SubItems[0].Text != "write docs" {
		t.Errorf("expected 'write docs' as sub-item, got %q", app.plan.WeekFocus[0].SubItems[0].Text)
	}
}

// Test: :mv to goal respects max cap
func TestPlanMoveGoalMaxCap(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "G1"`)
	app = sendCommand(t, app, `goal "G2"`)
	app = sendCommand(t, app, `goal "G3"`)

	// Switch to hibana
	app = sendKeys(t, app, "tab", "tab")
	app = sendCommand(t, app, `hibana "overflow"`)

	app = sendCommand(t, app, `mv goal`)
	assertStatus(t, app, "full")
	if len(app.plan.Scratch) != 1 {
		t.Errorf("expected scratch note to remain after blocked move, got %d", len(app.plan.Scratch))
	}
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

// Test 55: edit today item opens editor
func TestEditTodayItem(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "old text"`)
	app = sendKeys(t, app, "tab") // today tab

	// 'e' should launch external editor (returns a command)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}
	m, cmd := app.Update(msg)
	app = m.(AppModel)
	if cmd == nil {
		t.Fatal("expected a command (editor launch) from 'e' key")
	}

	// Simulate editorFinishedMsg
	tmpFile := t.TempDir() + "/note.md"
	os.WriteFile(tmpFile, []byte("new text"), 0644)
	m2, _ := app.Update(editorFinishedMsg{tmpPath: tmpFile, section: sectionToday, idx: 0, subIdx: -1, err: nil})
	app = m2.(AppModel)
	if app.plan.Today[0].Text != "new text" {
		t.Errorf("expected 'new text', got %q", app.plan.Today[0].Text)
	}
}

// Test 56: edit goal opens editor
func TestEditGoalItem(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "old goal"`)

	// 'e' should launch external editor
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}
	_, cmd := app.Update(msg)
	if cmd == nil {
		t.Fatal("expected a command (editor launch) from 'e' key")
	}
}

// Test 57: edit pinned issue rejected
func TestEditPinnedIssueRejected(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, "pin #101")
	// Rebuild planView so the pinned item is visible
	app.planView.SetData(app.plan, app.project)
	// Cursor should be on the pinned item in week focus
	app = sendKeys(t, app, "e")
	assertStatus(t, app, "Cannot edit")
}

// Test 58: editor preserves cursor position across all tabs
func TestEditorPreservesCursor(t *testing.T) {
	sections := []struct {
		name    string
		section planSection
		setup   func(app AppModel) AppModel
	}{
		{
			"hibana",
			sectionHibana,
			func(app AppModel) AppModel {
				app.plan.Scratch = []model.ScratchNote{
					{Text: "note 0"}, {Text: "note 1"}, {Text: "note 2"},
				}
				return app
			},
		},
		{
			"today",
			sectionToday,
			func(app AppModel) AppModel {
				app.plan.Today = []model.TodoItem{
					{Text: "task 0"}, {Text: "task 1"}, {Text: "task 2"},
				}
				return app
			},
		},
		{
			"week_focus",
			sectionWeekFocus,
			func(app AppModel) AppModel {
				app.plan.WeekFocus = []model.FocusItem{
					{Text: "goal 0"}, {Text: "goal 1"}, {Text: "goal 2"},
				}
				return app
			},
		},
		{
			"monthly_target",
			sectionMonthlyTarget,
			func(app AppModel) AppModel {
				app.plan.MonthlyTargets = []model.MonthlyTarget{
					{Text: "target 0"}, {Text: "target 1"}, {Text: "target 2"},
				}
				return app
			},
		},
	}

	for _, tc := range sections {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp()
			app.width = 80
			app.height = 40
			app = tc.setup(app)
			app.planView.SetSection(tc.section)
			app.planView.SetData(app.plan, app.project)
			app.view = viewPlan

			// Move cursor to item at index 2
			app = sendKeys(t, app, "j", "j")
			if app.planView.cursorIdx != 2 {
				t.Fatalf("expected cursor at 2 before edit, got %d", app.planView.cursorIdx)
			}

			// Simulate editor round-trip on that item
			tmpFile := t.TempDir() + "/edit.md"
			os.WriteFile(tmpFile, []byte("edited text"), 0644)
			m, _ := app.Update(editorFinishedMsg{
				tmpPath: tmpFile, section: tc.section, idx: 2, subIdx: -1, err: nil,
			})
			app = m.(AppModel)

			if app.planView.cursorIdx != 2 {
				t.Errorf("cursor jumped to %d after edit, expected 2", app.planView.cursorIdx)
			}
		})
	}
}

// --- Review screen tests ---

// Test 58: review navigation
func TestReviewNavigation(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "mv #101 done")
	app = sendCommand(t, app, "mv #201 done")
	app = sendKeys(t, app, "q", "q")
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
	app = sendKeys(t, app, "q", "q")
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
	app = sendKeys(t, app, "q", "q")
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
	app = sendKeys(t, app, "q", "q")
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
	app = sendKeys(t, app, "q", "q")
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
	app = sendKeys(t, app, "tab") // Hibana
	_ = app.View()

	// Board + ops + review
	app = sendCommand(t, app, "board")
	app = sendCommand(t, app, "mv #101 done")
	app = sendKeys(t, app, "q", "q")
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

// Test: :target adds a monthly target and switches to the target tab
func TestTargetAddAndSection(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40

	app = sendCommand(t, app, `target "Ship v1.0"`)
	assertView(t, app, viewPlan)
	if app.planView.section != sectionMonthlyTarget {
		t.Fatalf("expected sectionMonthlyTarget, got %d", app.planView.section)
	}
	if len(app.plan.MonthlyTargets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(app.plan.MonthlyTargets))
	}
	if app.plan.MonthlyTargets[0].Text != "Ship v1.0" {
		t.Errorf("expected text 'Ship v1.0', got %q", app.plan.MonthlyTargets[0].Text)
	}
	if app.plan.MonthlyTargets[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	assertStatus(t, app, "Monthly target added")
}

// Test: Toggle done on monthly target via Enter key
func TestTargetToggleDone(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `target "Ship v1.0"`)

	// Press Enter to toggle done
	app = sendKeys(t, app, "enter")
	if !app.plan.MonthlyTargets[0].Done {
		t.Fatal("expected target to be done after Enter")
	}
	if app.plan.MonthlyTargets[0].DoneAt.IsZero() {
		t.Error("expected DoneAt to be set")
	}
	assertStatus(t, app, "Completed: Ship v1.0")

	// Show done items, then toggle back
	app = sendKeys(t, app, "x", "enter")
	if app.plan.MonthlyTargets[0].Done {
		t.Fatal("expected target to be undone after second Enter")
	}
	assertStatus(t, app, "Uncompleted: Ship v1.0")
}

// Test: Delete monthly target via cursor
func TestTargetDeleteCursor(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `target "A"`)
	app = sendCommand(t, app, `target "B"`)

	// Cursor is on first item, delete it
	app = sendCommand(t, app, "del")
	if len(app.plan.MonthlyTargets) != 1 {
		t.Fatalf("expected 1 target after delete, got %d", len(app.plan.MonthlyTargets))
	}
	if app.plan.MonthlyTargets[0].Text != "B" {
		t.Errorf("expected remaining target 'B', got %q", app.plan.MonthlyTargets[0].Text)
	}
}

// Test: Delete monthly target by line number
func TestTargetDeleteByLineNumber(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `target "A"`)
	app = sendCommand(t, app, `target "B"`)
	app = sendCommand(t, app, `target "C"`)

	app = sendCommand(t, app, "del 2")
	if len(app.plan.MonthlyTargets) != 2 {
		t.Fatalf("expected 2 targets after del 2, got %d", len(app.plan.MonthlyTargets))
	}
	if app.plan.MonthlyTargets[0].Text != "A" || app.plan.MonthlyTargets[1].Text != "C" {
		t.Errorf("expected [A, C], got [%s, %s]", app.plan.MonthlyTargets[0].Text, app.plan.MonthlyTargets[1].Text)
	}
}

// Test: Reorder monthly targets with J/K
func TestTargetReorder(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, `target "First"`)
	app = sendCommand(t, app, `target "Second"`)

	// Cursor is on First (idx 0), press J to swap down
	app = sendKeys(t, app, "J")
	if app.plan.MonthlyTargets[0].Text != "Second" || app.plan.MonthlyTargets[1].Text != "First" {
		t.Errorf("expected [Second, First] after J, got [%s, %s]",
			app.plan.MonthlyTargets[0].Text, app.plan.MonthlyTargets[1].Text)
	}

	// Now cursor is on First (idx 1), press K to swap back up
	app = sendKeys(t, app, "K")
	if app.plan.MonthlyTargets[0].Text != "First" || app.plan.MonthlyTargets[1].Text != "Second" {
		t.Errorf("expected [First, Second] after K, got [%s, %s]",
			app.plan.MonthlyTargets[0].Text, app.plan.MonthlyTargets[1].Text)
	}
}

// Test: Tab navigation cycles through all 4 sections including monthly target
func TestTabCyclesToMonthlyTarget(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	assertView(t, app, viewPlan)

	// Start at WeekFocus (0), tab through all sections
	if app.planView.section != sectionWeekFocus {
		t.Fatalf("expected sectionWeekFocus, got %d", app.planView.section)
	}
	app = sendKeys(t, app, "tab") // -> Today
	if app.planView.section != sectionToday {
		t.Fatalf("expected sectionToday, got %d", app.planView.section)
	}
	app = sendKeys(t, app, "tab") // -> Hibana
	if app.planView.section != sectionHibana {
		t.Fatalf("expected sectionHibana, got %d", app.planView.section)
	}
	app = sendKeys(t, app, "tab") // -> Monthly Target
	if app.planView.section != sectionMonthlyTarget {
		t.Fatalf("expected sectionMonthlyTarget, got %d", app.planView.section)
	}
	app = sendKeys(t, app, "tab") // -> wraps back to WeekFocus
	if app.planView.section != sectionWeekFocus {
		t.Fatalf("expected wrap to sectionWeekFocus, got %d", app.planView.section)
	}
}

// Test: Vertical cursor wraps around (bottom→top, top→bottom)
func TestPlanCursorWrap(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app.plan.Scratch = []model.ScratchNote{
		{Text: "note 0"}, {Text: "note 1"}, {Text: "note 2"},
	}
	app.planView.SetSection(sectionHibana)
	app.planView.SetData(app.plan, app.project)
	app.view = viewPlan

	// Move to last item
	app = sendKeys(t, app, "j", "j")
	if app.planView.cursorIdx != 2 {
		t.Fatalf("expected cursor at 2, got %d", app.planView.cursorIdx)
	}

	// One more j wraps to top
	app = sendKeys(t, app, "j")
	if app.planView.cursorIdx != 0 {
		t.Errorf("expected cursor to wrap to 0, got %d", app.planView.cursorIdx)
	}

	// k from top wraps to bottom
	app = sendKeys(t, app, "k")
	if app.planView.cursorIdx != 2 {
		t.Errorf("expected cursor to wrap to 2, got %d", app.planView.cursorIdx)
	}
}

// Test: Done focus items are purged on :board switch
func TestDoneFocusPurgedOnBoardSwitch(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "Goal 1"`)
	app = sendCommand(t, app, `goal "Goal 2"`)
	app = sendCommand(t, app, `goal "Goal 3"`)
	app.plan.WeekFocus[0].Done = true
	app.plan.WeekFocus[2].Done = true

	app = sendCommand(t, app, "board")
	assertView(t, app, viewBoard)
	if len(app.plan.WeekFocus) != 1 {
		t.Errorf("expected 1 focus item after purge, got %d", len(app.plan.WeekFocus))
	}
	if app.plan.WeekFocus[0].Text != "Goal 2" {
		t.Errorf("expected 'Goal 2' to survive, got %q", app.plan.WeekFocus[0].Text)
	}
}

// Test: Done focus items are purged on quit
func TestDoneFocusPurgedOnQuit(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `goal "Goal A"`)
	app = sendCommand(t, app, `goal "Goal B"`)
	app.plan.WeekFocus[0].Done = true

	// q triggers initiateQuit which purges done items
	app = sendKeys(t, app, "q", "q")
	if len(app.plan.WeekFocus) != 1 {
		t.Errorf("expected 1 focus item after quit purge, got %d", len(app.plan.WeekFocus))
	}
	if app.plan.WeekFocus[0].Text != "Goal B" {
		t.Errorf("expected 'Goal B' to survive, got %q", app.plan.WeekFocus[0].Text)
	}
}

// Test: Edit monthly target via editorFinishedMsg
func TestTargetEdit(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, `target "draft"`)

	// Simulate editor returning updated text
	tmpFile := t.TempDir() + "/target.md"
	os.WriteFile(tmpFile, []byte("finalized target"), 0644)
	m, _ := app.Update(editorFinishedMsg{tmpPath: tmpFile, section: sectionMonthlyTarget, idx: 0, subIdx: -1, err: nil})
	app = m.(AppModel)

	if app.plan.MonthlyTargets[0].Text != "finalized target" {
		t.Errorf("expected 'finalized target', got %q", app.plan.MonthlyTargets[0].Text)
	}
	assertStatus(t, app, "Updated")
}

// Test: :target with no args shows usage
func TestTargetNoArgs(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "target")
	assertStatus(t, app, "Usage:")
}

// Test: Monthly target renders without panic when empty
func TestTargetEmptyView(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")

	// Navigate to monthly target tab
	app.planView.SetSection(sectionMonthlyTarget)
	output := app.View()
	if !strings.Contains(output, "Monthly Target") {
		t.Error("expected 'Monthly Target' tab label in output")
	}
	if !strings.Contains(output, ":target") {
		t.Error("expected empty-state hint mentioning :target")
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

// Test: Epic detail opened from board should show parent body, not "no description"
func TestEpicDetailShowsBody(t *testing.T) {
	app := newTestApp()
	app.width = 120
	app.height = 40

	// Simulate what the board Enter handler does for epics: creates a synthetic
	// ProjectItem with no Body, then calls buildDetailModel.
	epicRef := &model.ParentRef{
		Number: 100,
		Title:  "Epic Alpha",
		Repo:   "test-org/repo",
	}
	syntheticIssue := &model.ProjectItem{
		Title:  epicRef.Title,
		Number: epicRef.Number,
		URL:    epicRef.URL,
		Repo:   epicRef.Repo,
	}

	// Without pre-rendered content, the fallback should still find the body
	app.detail = app.buildDetailModel(syntheticIssue)
	app.view = viewDetail

	output := app.View()
	if strings.Contains(output, "no description") {
		t.Error("epic detail should show body content, not 'no description'")
	}
	if !strings.Contains(output, "Alpha milestone") {
		t.Error("expected epic body text 'Alpha milestone' in detail view")
	}
}

// Test: Detail split pane — Tab toggles focus, j/k is section-aware
func TestDetailSplitPaneFocus(t *testing.T) {
	app := newTestApp()
	app.width = 120
	app.height = 40

	// Build detail for epic #100 which has sub-issues
	app.detail = app.buildDetailModel(&app.project.Items[0])
	app.view = viewDetail
	app.prevView = viewBoard

	if !app.detail.HasNav() {
		t.Fatal("expected nav items for epic #100")
	}

	// Initially nav should be focused
	if !app.detail.NavFocused() {
		t.Error("expected nav to be focused initially")
	}

	// j should move nav cursor, not scroll body
	app = sendKeys(t, app, "j")
	if app.detail.navCursor != 1 {
		t.Errorf("expected navCursor=1 after j in nav pane, got %d", app.detail.navCursor)
	}

	// Tab should switch focus to body
	app = sendKeys(t, app, "tab")
	if app.detail.NavFocused() {
		t.Error("expected body focused after Tab")
	}

	// j should now scroll body, not move nav cursor
	prevCursor := app.detail.navCursor
	app = sendKeys(t, app, "j")
	if app.detail.navCursor != prevCursor {
		t.Error("j in body pane should not move nav cursor")
	}

	// Tab back to nav
	app = sendKeys(t, app, "tab")
	if !app.detail.NavFocused() {
		t.Error("expected nav focused after second Tab")
	}

	// View should render without panic and contain exactly one Sub-Issues section
	output := app.View()
	if output == "" {
		t.Error("expected non-empty view")
	}
	count := strings.Count(output, "Sub-Issues")
	if count != 1 {
		t.Errorf("expected 'Sub-Issues' once, got %d", count)
	}
}

// Test: Detail view for issue with sub-issues should not duplicate sub-issues section
func TestDetailSubIssuesNoDuplication(t *testing.T) {
	app := newTestApp()
	app.width = 120
	app.height = 40

	// Pre-render all items (simulates normal startup)
	cmd := app.preRenderAll()
	if cmd != nil {
		msg := cmd()
		if prMsg, ok := msg.(preRenderDoneMsg); ok {
			app.renderedDetails = prMsg.rendered
		}
	}

	// Build detail for issue #100 which has sub-issues in ChildrenMap
	app.detail = app.buildDetailModel(&app.project.Items[0]) // Epic Alpha #100
	app.view = viewDetail
	app.prevView = viewBoard

	output := app.View()
	count := strings.Count(output, "Sub-Issues")
	if count != 1 {
		t.Errorf("expected 'Sub-Issues' to appear exactly once in detail view, got %d occurrences", count)
	}
}
