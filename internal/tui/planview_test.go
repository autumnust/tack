package tui

import (
	"strings"
	"testing"

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

func TestPlanView_ScrollFollowsCursor(t *testing.T) {
	plan := makePlanWithScratch(20)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionHibana)
	m.viewHeight = 5

	// Move cursor past the viewport
	for i := 0; i < 10; i++ {
		m.CursorDown()
	}

	if m.cursorIdx != 10 {
		t.Fatalf("expected cursorIdx=10, got %d", m.cursorIdx)
	}
	if m.cursorIdx < m.scrollOffset || m.cursorIdx >= m.scrollOffset+m.viewHeight {
		t.Errorf("cursor %d not visible in scroll window [%d, %d)",
			m.cursorIdx, m.scrollOffset, m.scrollOffset+m.viewHeight)
	}
}

func TestPlanView_ScrollUpAdjusts(t *testing.T) {
	plan := makePlanWithScratch(20)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionHibana)
	m.viewHeight = 5

	// Move to bottom
	for i := 0; i < 19; i++ {
		m.CursorDown()
	}
	// Move back up past the visible window
	for i := 0; i < 19; i++ {
		m.CursorUp()
	}

	if m.cursorIdx != 0 {
		t.Fatalf("expected cursorIdx=0, got %d", m.cursorIdx)
	}
	if m.scrollOffset != 0 {
		t.Errorf("expected scrollOffset=0 after scrolling back to top, got %d", m.scrollOffset)
	}
}

func TestPlanView_ViewRendersOnlyVisibleItems(t *testing.T) {
	plan := makePlanWithToday(20)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionToday)

	output := m.View(80, 16) // viewHeight = 16 - 6 = 10

	// Should not contain item 15 (0-indexed) which would show as line "16"
	// but should contain item 0 which shows as line "1"
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

func TestPlanView_SectionChangeResetsScroll(t *testing.T) {
	plan := makePlanWithScratch(20)
	plan.Today = make([]model.TodoItem, 5)
	for i := range plan.Today {
		plan.Today[i] = model.TodoItem{Text: "task"}
	}

	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionHibana)
	m.viewHeight = 5

	// Scroll down
	for i := 0; i < 10; i++ {
		m.CursorDown()
	}

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

func TestPlanView_MoveDownKeepsCursorVisible(t *testing.T) {
	plan := makePlanWithScratch(10)
	m := NewPlanViewModel(plan, nil)
	m.SetSection(sectionHibana)
	m.viewHeight = 3

	// Move cursor to item 5
	for i := 0; i < 5; i++ {
		m.CursorDown()
	}

	// MoveDown swaps item with the one below, cursor follows
	m.MoveDown()

	if m.cursorIdx < m.scrollOffset || m.cursorIdx >= m.scrollOffset+m.viewHeight {
		t.Errorf("cursor %d not visible after MoveDown in window [%d, %d)",
			m.cursorIdx, m.scrollOffset, m.scrollOffset+m.viewHeight)
	}
}
