package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/autumnust/tack/internal/model"
)

func makePlanWithScratch(n int) *model.Plan {
	p := &model.Plan{}
	for i := 0; i < n; i++ {
		p.Scratch = append(p.Scratch, model.ScratchNote{Text: "note"})
	}
	return p
}

func makePlanWithToday(n int) *model.Plan {
	p := &model.Plan{}
	for i := 0; i < n; i++ {
		p.Today = append(p.Today, model.TodoItem{Text: "task"})
	}
	return p
}

func TestPlanView_ViewRendersOnlyVisibleItems(t *testing.T) {
	plan := makePlanWithToday(20)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionToday)

	output := m.View(80, 16) // availLines = 16 - 6 = 10

	if m.scrollOffset != 0 {
		t.Errorf("expected scrollOffset=0 initially, got %d", m.scrollOffset)
	}
	if output == "" {
		t.Error("expected non-empty output")
	}

	// Move cursor to item 15, then render
	for i := 0; i < 15; i++ {
		m.CursorDown()
	}
	m.View(80, 16)

	if m.scrollOffset <= 0 {
		t.Errorf("expected scrollOffset > 0 after scrolling down, got %d", m.scrollOffset)
	}
}

func TestPlanView_ScrollUpThroughView(t *testing.T) {
	plan := makePlanWithToday(20)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionToday)

	// Scroll to the bottom
	for i := 0; i < 19; i++ {
		m.CursorDown()
	}
	m.View(80, 16)
	if m.scrollOffset == 0 {
		t.Fatal("expected scrollOffset > 0 after scrolling down")
	}

	// Scroll back to top
	for i := 0; i < 19; i++ {
		m.CursorUp()
	}
	output := m.View(80, 16)

	if m.cursorIdx != 0 {
		t.Fatalf("expected cursorIdx=0, got %d", m.cursorIdx)
	}
	if m.scrollOffset != 0 {
		t.Errorf("expected scrollOffset=0 after scrolling back to top, got %d", m.scrollOffset)
	}
	// First item (labeled "1") should be visible
	if !strings.Contains(output, "1") {
		t.Error("first item not visible after scrolling back to top")
	}
}

func TestPlanView_SectionChangeResetsScroll(t *testing.T) {
	plan := makePlanWithScratch(20)
	plan.Today = make([]model.TodoItem, 5)
	for i := range plan.Today {
		plan.Today[i] = model.TodoItem{Text: "task"}
	}

	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionHibana)

	// Scroll down through View
	for i := 0; i < 10; i++ {
		m.CursorDown()
	}
	m.View(80, 20)

	// Switch section
	m.NextSection()

	if m.scrollOffset != 0 {
		t.Errorf("expected scrollOffset=0 after section change, got %d", m.scrollOffset)
	}
	if m.cursorIdx != 0 {
		t.Errorf("expected cursorIdx=0 after section change, got %d", m.cursorIdx)
	}
}

func TestPlanView_MultiLineScrolling(t *testing.T) {
	// Hibana items with multi-line text should scroll based on rendered lines,
	// not item count.
	plan := &model.Plan{}
	for i := 0; i < 10; i++ {
		plan.Scratch = append(plan.Scratch, model.ScratchNote{
			Text: "line one\nline two\nline three", // 3 rendered lines each
		})
	}
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionHibana)
	m.ToggleHibanaExpanded() // expand to get multi-line rendering

	// Terminal height 16: availLines = 16 - 6 = 10 lines
	// Each item = 3 lines, so ~3 items fit on screen
	// Move cursor to item 5
	for i := 0; i < 5; i++ {
		m.CursorDown()
	}

	output := m.View(80, 16)

	// The cursor item (item 5, labeled "6") must appear in the rendered output
	if !strings.Contains(output, "6") {
		t.Errorf("cursor item not visible in rendered output after scrolling with multi-line items")
	}
	// Item 1 should have scrolled off screen
	if m.scrollOffset == 0 {
		t.Errorf("expected scrollOffset > 0 for multi-line items, got 0")
	}
}

func TestPlanView_MoveDownThroughView(t *testing.T) {
	plan := makePlanWithScratch(10)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionHibana)

	// Move cursor to item 5
	for i := 0; i < 5; i++ {
		m.CursorDown()
	}
	m.View(80, 12) // establish scroll state

	// MoveDown swaps item with the one below, cursor follows
	m.MoveDown()
	output := m.View(80, 12)

	// Cursor item (now at index 6, labeled "7") must be in the rendered output
	if !strings.Contains(output, "►") {
		t.Errorf("cursor marker not visible after MoveDown")
	}
}

func TestPlanView_BubbleTeaLifecycle(t *testing.T) {
	// Simulate Bubble Tea's model lifecycle: View() is called on a value copy
	// so state set there is discarded. viewHeight must be set via SetSize()
	// (called from WindowSizeMsg in Update) for ensureVisible() to work.
	plan := makePlanWithToday(20)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionToday)
	m.SetSize(80, 16) // simulates WindowSizeMsg in Update path

	// Simulate View() on a copy (as Bubble Tea does) — discard the copy
	copy := m
	copy.View(80, 16)
	// m is unaffected by copy's View

	// Navigate past viewport in the Update path
	for i := 0; i < 15; i++ {
		m.CursorDown()
	}

	// Render via View — cursor item must be visible
	output := m.View(80, 16)
	if !strings.Contains(output, "►") {
		t.Errorf("cursor not visible after navigating past viewport in Bubble Tea lifecycle")
	}
	if m.scrollOffset == 0 {
		t.Errorf("expected scrollOffset > 0 in Bubble Tea lifecycle, got 0")
	}
}

func TestWeekdaysBetween(t *testing.T) {
	// 2026-04-10 is a Friday, 2026-04-13 is Monday
	fri := time.Date(2026, 4, 10, 9, 0, 0, 0, time.Local)
	mon := time.Date(2026, 4, 13, 9, 0, 0, 0, time.Local)
	tue := time.Date(2026, 4, 14, 9, 0, 0, 0, time.Local)
	wed := time.Date(2026, 4, 15, 9, 0, 0, 0, time.Local)
	thu := time.Date(2026, 4, 9, 9, 0, 0, 0, time.Local)

	tests := []struct {
		name string
		from time.Time
		to   time.Time
		want int
	}{
		{"same day", fri, fri, 0},
		{"fri to mon (weekend skipped)", fri, mon, 1},
		{"fri to tue", fri, tue, 2},
		{"thu to mon", thu, mon, 2},
		{"thu to wed (full work week)", thu, wed, 4},
		{"mon to tue (consecutive weekdays)", mon, tue, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := weekdaysBetween(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("weekdaysBetween(%s, %s) = %d, want %d",
					tt.from.Format("Mon 2006-01-02"), tt.to.Format("Mon 2006-01-02"), got, tt.want)
			}
		})
	}
}
