package planning

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autumnust/tack/internal/model"
)

// mockSyncer records calls for test assertions.
type mockSyncer struct {
	pullCount         int
	commitAndPushArgs []commitAndPushCall
}

type commitAndPushCall struct {
	msg   string
	paths []string
}

func (m *mockSyncer) Pull() error {
	m.pullCount++
	return nil
}

func (m *mockSyncer) CommitAndPush(msg string, paths []string) error {
	m.commitAndPushArgs = append(m.commitAndPushArgs, commitAndPushCall{msg, paths})
	return nil
}

func storeWithMock(t *testing.T) (*Store, *mockSyncer) {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockSyncer{}
	s.SetSyncer(mock)
	return s, mock
}

// --- Pull on load ---

func TestLoadPlan_PullsCalled(t *testing.T) {
	s, mock := storeWithMock(t)

	if _, err := s.LoadPlan(); err != nil {
		t.Fatal(err)
	}
	if mock.pullCount != 1 {
		t.Errorf("expected 1 Pull call, got %d", mock.pullCount)
	}
}

func TestLoadAnnotations_PullsCalled(t *testing.T) {
	s, mock := storeWithMock(t)

	if _, err := s.LoadAnnotations(); err != nil {
		t.Fatal(err)
	}
	if mock.pullCount != 1 {
		t.Errorf("expected 1 Pull call, got %d", mock.pullCount)
	}
}

// --- CommitAndPush on save ---

func TestSavePlan_CommitsAndPushes(t *testing.T) {
	s, mock := storeWithMock(t)
	plan := &model.Plan{
		Scratch: []model.ScratchNote{{Text: "test", CreatedAt: time.Now()}},
	}

	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if len(mock.commitAndPushArgs) != 1 {
		t.Fatalf("expected 1 CommitAndPush call, got %d", len(mock.commitAndPushArgs))
	}
	call := mock.commitAndPushArgs[0]
	if len(call.paths) != 1 || call.paths[0] != "plan.yaml" {
		t.Errorf("expected paths=[plan.yaml], got %v", call.paths)
	}
}

func TestSaveAnnotations_CommitsAndPushes(t *testing.T) {
	s, mock := storeWithMock(t)
	ann := &model.Annotations{
		Items: []model.Annotation{{IssueNum: 1, Notes: []string{"n"}}},
	}

	if err := s.SaveAnnotations(ann); err != nil {
		t.Fatal(err)
	}
	if len(mock.commitAndPushArgs) != 1 {
		t.Fatalf("expected 1 CommitAndPush call, got %d", len(mock.commitAndPushArgs))
	}
	call := mock.commitAndPushArgs[0]
	if len(call.paths) != 1 || call.paths[0] != "annotations.yaml" {
		t.Errorf("expected paths=[annotations.yaml], got %v", call.paths)
	}
}

func TestSaveRecap_CommitsAndPushes(t *testing.T) {
	s, mock := storeWithMock(t)

	if err := s.SaveRecap("2026-W15", "# recap"); err != nil {
		t.Fatal(err)
	}
	if len(mock.commitAndPushArgs) != 1 {
		t.Fatalf("expected 1 CommitAndPush call, got %d", len(mock.commitAndPushArgs))
	}
	call := mock.commitAndPushArgs[0]
	expected := filepath.Join("recaps", "2026-W15.md")
	if len(call.paths) != 1 || call.paths[0] != expected {
		t.Errorf("expected paths=[%s], got %v", expected, call.paths)
	}
	// Verify file was actually written
	data, err := os.ReadFile(filepath.Join(s.Dir(), expected))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# recap" {
		t.Errorf("expected recap content '# recap', got %q", string(data))
	}
}

// --- Nil syncer (no-op) ---

func TestNilSyncer_LoadsAndSavesWithoutError(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.SetSyncer(nil) // explicitly disable

	plan := &model.Plan{
		Today: []model.TodoItem{{Text: "task", CreatedAt: time.Now()}},
	}
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Today) != 1 || loaded.Today[0].Text != "task" {
		t.Error("round-trip with nil syncer failed")
	}
}

// --- Hibana flow (load + append + save) ---

func TestHibanaFlow_PullsThenCommitsAndPushes(t *testing.T) {
	s, mock := storeWithMock(t)

	// Simulate the hibana flow from main.go:
	// 1. LoadPlan (should pull)
	plan, err := s.LoadPlan()
	if err != nil {
		t.Fatal(err)
	}
	// 2. Append scratch note
	plan.Scratch = append(plan.Scratch, model.ScratchNote{
		Text:      "quick thought",
		CreatedAt: time.Now(),
	})
	// 3. SavePlan (should commit+push)
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}

	if mock.pullCount != 1 {
		t.Errorf("expected 1 Pull, got %d", mock.pullCount)
	}
	if len(mock.commitAndPushArgs) != 1 {
		t.Errorf("expected 1 CommitAndPush, got %d", len(mock.commitAndPushArgs))
	}
}

// --- TUI quit flow (save plan + save annotations) ---

func TestTUIQuitFlow_CommitsAndPushesBoth(t *testing.T) {
	s, mock := storeWithMock(t)

	plan := &model.Plan{
		Today: []model.TodoItem{{Text: "done", Done: true, CreatedAt: time.Now()}},
	}
	ann := &model.Annotations{
		Items: []model.Annotation{{IssueNum: 10, Notes: []string{"note"}}},
	}

	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAnnotations(ann); err != nil {
		t.Fatal(err)
	}

	if len(mock.commitAndPushArgs) != 2 {
		t.Fatalf("expected 2 CommitAndPush calls, got %d", len(mock.commitAndPushArgs))
	}
	if mock.commitAndPushArgs[0].paths[0] != "plan.yaml" {
		t.Errorf("first commit should be plan.yaml, got %v", mock.commitAndPushArgs[0].paths)
	}
	if mock.commitAndPushArgs[1].paths[0] != "annotations.yaml" {
		t.Errorf("second commit should be annotations.yaml, got %v", mock.commitAndPushArgs[1].paths)
	}
}

// --- GitSyncer detection ---

func TestNewGitSyncer_NonGitDir_ReturnsNil(t *testing.T) {
	dir := t.TempDir() // not a git repo
	gs := NewGitSyncer(dir)
	if gs != nil {
		t.Error("expected nil GitSyncer for non-git directory")
	}
}

func TestSyncMsg_Format(t *testing.T) {
	msg := SyncMsg("update plan")
	today := time.Now().Format("2006-01-02")
	if len(msg) == 0 {
		t.Fatal("expected non-empty message")
	}
	if !contains(msg, "tack: update plan") {
		t.Errorf("expected message to contain 'tack: update plan', got %q", msg)
	}
	if !contains(msg, today) {
		t.Errorf("expected message to contain today's date %s, got %q", today, msg)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
