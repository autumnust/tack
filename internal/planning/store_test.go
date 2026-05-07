package planning

import (
	"os"
	"path/filepath"
	"strings"
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
		// Scratch is owned by the hibana package now; SavePlan deliberately
		// strips it from the persisted plan.
		Scratch: []model.ScratchNote{
			{Text: "should not survive", CreatedAt: time.Now().Truncate(time.Second)},
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
	if len(loaded.Scratch) != 0 {
		t.Errorf("scratch should be stripped on save/load, got %d entries", len(loaded.Scratch))
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

func TestPersonNote_RoundTrip(t *testing.T) {
	s := tempStore(t)

	if got, err := s.LoadPersonNote("alice"); err != nil {
		t.Fatal(err)
	} else if got != "" {
		t.Fatalf("expected empty missing note, got %q", got)
	}

	body := "# Alice\n\n## 2026-04-23\n- discussed priorities\n"
	if err := s.SavePersonNote("alice", body); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPersonNote("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got != body {
		t.Fatalf("expected %q, got %q", body, got)
	}
	if !s.HasPersonNote("alice") {
		t.Fatal("expected note existence after save")
	}
}

func TestPersonNote_HasUnresolved_EmptyAndMissing(t *testing.T) {
	s := tempStore(t)

	if s.HasUnresolvedPersonNote("alice") {
		t.Fatal("missing note should be considered resolved (no icon)")
	}
	if err := s.SavePersonNote("alice", "   \n\n"); err != nil {
		t.Fatal(err)
	}
	if s.HasUnresolvedPersonNote("alice") {
		t.Fatal("whitespace-only note should be treated as nothing to resolve")
	}
}

func TestPersonNote_HasUnresolved_TracksHeadingMarker(t *testing.T) {
	s := tempStore(t)
	body := "# Alice\n\n## 2026-04-23\n- discussed priorities\n\n## 2026-05-01\n- next steps\n"
	if err := s.SavePersonNote("alice", body); err != nil {
		t.Fatal(err)
	}
	if !s.HasUnresolvedPersonNote("alice") {
		t.Fatal("two unmarked sections should be unresolved")
	}

	// Resolve all → icon hides.
	n, err := s.ResolvePersonNote("alice")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 sections resolved, got %d", n)
	}
	if s.HasUnresolvedPersonNote("alice") {
		t.Fatal("after :resolve all sections should be done, icon should hide")
	}

	// Idempotent: resolving again is a no-op.
	if n, err := s.ResolvePersonNote("alice"); err != nil || n != 0 {
		t.Fatalf("re-resolve expected (0,nil), got (%d,%v)", n, err)
	}

	// Reopen one section by hand → icon must come back.
	got, err := s.LoadPersonNote("alice")
	if err != nil {
		t.Fatal(err)
	}
	reopened := strings.Replace(got, "## 2026-05-01 ✓", "## 2026-05-01", 1)
	if reopened == got {
		t.Fatal("expected resolved marker on second heading after :resolve")
	}
	if err := s.SavePersonNote("alice", reopened); err != nil {
		t.Fatal(err)
	}
	if !s.HasUnresolvedPersonNote("alice") {
		t.Fatal("a single unresolved section should bring the icon back")
	}
}

func TestPersonNote_HasUnresolved_HeaderlessTreatedUnresolved(t *testing.T) {
	s := tempStore(t)
	// Freeform body without `## ` headings — user typed prose only.
	if err := s.SavePersonNote("alice", "raw thoughts, no headings\n"); err != nil {
		t.Fatal(err)
	}
	if !s.HasUnresolvedPersonNote("alice") {
		t.Fatal("non-empty headerless note should still surface the icon")
	}
}

func TestPersonNote_Unresolve_Reopens(t *testing.T) {
	s := tempStore(t)
	body := "# Alice\n\n## 2026-04-23\n- a\n\n## 2026-05-01\n- b\n"
	if err := s.SavePersonNote("alice", body); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePersonNote("alice"); err != nil {
		t.Fatal(err)
	}
	if s.HasUnresolvedPersonNote("alice") {
		t.Fatal("expected resolved after :resolve")
	}
	n, err := s.UnresolvePersonNote("alice")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 sections reopened, got %d", n)
	}
	if !s.HasUnresolvedPersonNote("alice") {
		t.Fatal("after :unresolve sections should be unresolved again")
	}
}

func TestPersonNote_SaveEmptyRemovesFile(t *testing.T) {
	s := tempStore(t)
	if err := s.SavePersonNote("alice", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePersonNote("alice", ""); err != nil {
		t.Fatal(err)
	}
	if s.HasPersonNote("alice") {
		t.Fatal("expected empty save to clear note file")
	}
	got, err := s.LoadPersonNote("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected cleared note body, got %q", got)
	}
}
