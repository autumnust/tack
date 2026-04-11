package planning

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autumnust/tack/internal/model"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewStore_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("expected directory to be created")
	}
}

func TestSavePlan_LoadPlan_Roundtrip(t *testing.T) {
	s := tempStore(t)
	plan := &model.Plan{
		WeekFocus: []model.FocusItem{
			{Text: "Ship v1", IssueNum: 42, SubItems: []model.SubItem{
				{Text: "Write tests", Done: true},
			}},
		},
		Today: []model.TodoItem{
			{Text: "Review PR", Done: false, CreatedAt: time.Now().Truncate(time.Second)},
		},
		Scratch: []model.ScratchNote{
			{Text: "Random thought", CreatedAt: time.Now().Truncate(time.Second)},
		},
	}

	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.LoadPlan()
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.WeekFocus) != 1 {
		t.Fatalf("expected 1 week focus, got %d", len(loaded.WeekFocus))
	}
	if loaded.WeekFocus[0].Text != "Ship v1" {
		t.Errorf("expected 'Ship v1', got %q", loaded.WeekFocus[0].Text)
	}
	if len(loaded.WeekFocus[0].SubItems) != 1 || !loaded.WeekFocus[0].SubItems[0].Done {
		t.Error("sub-item round-trip failed")
	}
	if len(loaded.Today) != 1 || loaded.Today[0].Text != "Review PR" {
		t.Error("today round-trip failed")
	}
	if len(loaded.Scratch) != 1 || loaded.Scratch[0].Text != "Random thought" {
		t.Error("scratch round-trip failed")
	}
}

func TestLoadPlan_MissingFile(t *testing.T) {
	s := tempStore(t)
	plan, err := s.LoadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil {
		t.Fatal("expected non-nil empty plan")
	}
	if len(plan.WeekFocus) != 0 || len(plan.Today) != 0 {
		t.Error("expected empty plan from missing file")
	}
}

func TestAnnotations_Roundtrip(t *testing.T) {
	s := tempStore(t)
	ann := &model.Annotations{
		Items: []model.Annotation{
			{IssueNum: 42, Notes: []string{"note1", "note2"}, CreatedAt: time.Now().Truncate(time.Second)},
		},
	}
	if err := s.SaveAnnotations(ann); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadAnnotations()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Items) != 1 || len(loaded.Items[0].Notes) != 2 {
		t.Error("annotations round-trip failed")
	}
}

func TestLoadAnnotations_MissingFile(t *testing.T) {
	s := tempStore(t)
	ann, err := s.LoadAnnotations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ann.Items) != 0 {
		t.Error("expected empty annotations")
	}
}

func TestRollover_DoneYesterday_Archived(t *testing.T) {
	plan := &model.Plan{
		Today: []model.TodoItem{
			{Text: "old done", Done: true, CreatedAt: time.Now().Add(-25 * time.Hour)},
		},
	}
	s := tempStore(t)
	s.Rollover(plan)
	if len(plan.Today) != 0 {
		t.Errorf("expected done+yesterday item removed from Today, got %d", len(plan.Today))
	}
	if len(plan.Completed) != 1 {
		t.Errorf("expected 1 completed item, got %d", len(plan.Completed))
	}
}

func TestRollover_DoneToday_Kept(t *testing.T) {
	plan := &model.Plan{
		Today: []model.TodoItem{
			{Text: "done today", Done: true, CreatedAt: time.Now()},
		},
	}
	s := tempStore(t)
	s.Rollover(plan)
	if len(plan.Today) != 1 {
		t.Errorf("expected done+today item kept, got %d items", len(plan.Today))
	}
	if len(plan.Completed) != 0 {
		t.Errorf("expected 0 completed, got %d", len(plan.Completed))
	}
}

func TestRollover_UndoneYesterday_Kept(t *testing.T) {
	plan := &model.Plan{
		Today: []model.TodoItem{
			{Text: "carry over", Done: false, CreatedAt: time.Now().Add(-25 * time.Hour)},
		},
	}
	s := tempStore(t)
	s.Rollover(plan)
	if len(plan.Today) != 1 {
		t.Errorf("expected undone item kept as carry-over, got %d", len(plan.Today))
	}
}

func TestRollover_ZeroCreatedAt_SetToNow(t *testing.T) {
	plan := &model.Plan{
		Today: []model.TodoItem{
			{Text: "no timestamp", Done: false},
		},
	}
	s := tempStore(t)
	s.Rollover(plan)
	if plan.Today[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set for zero-timestamp item")
	}
}

func TestRollover_EmptyPlan(t *testing.T) {
	plan := &model.Plan{}
	s := tempStore(t)
	s.Rollover(plan) // should not panic
	if len(plan.Today) != 0 {
		t.Error("expected empty Today")
	}
}

func TestGetAnnotation_Found(t *testing.T) {
	s := tempStore(t)
	ann := &model.Annotations{
		Items: []model.Annotation{
			{IssueNum: 42, Notes: []string{"found me"}},
			{IssueNum: 99, Notes: []string{"other"}},
		},
	}
	got := s.GetAnnotation(ann, 42)
	if got == nil {
		t.Fatal("expected annotation for #42")
	}
	if got.Notes[0] != "found me" {
		t.Errorf("expected 'found me', got %q", got.Notes[0])
	}
}

func TestGetAnnotation_NotFound(t *testing.T) {
	s := tempStore(t)
	ann := &model.Annotations{Items: []model.Annotation{{IssueNum: 42}}}
	got := s.GetAnnotation(ann, 999)
	if got != nil {
		t.Error("expected nil for missing annotation")
	}
}

func TestLogUsage_LoadUsageStats(t *testing.T) {
	s := tempStore(t)
	s.LogUsage(":mv")
	s.LogUsage(":mv")
	s.LogUsage(":c")

	stats, err := s.LoadUsageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats[":mv"] != 2 {
		t.Errorf("expected :mv count=2, got %d", stats[":mv"])
	}
	if stats[":c"] != 1 {
		t.Errorf("expected :c count=1, got %d", stats[":c"])
	}
}

func TestLoadUsageStats_MissingFile(t *testing.T) {
	s := tempStore(t)
	stats, err := s.LoadUsageStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Error("expected empty stats from missing file")
	}
}
