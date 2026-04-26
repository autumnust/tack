package hibana

import (
	"reflect"
	"testing"
	"time"
)

func TestFoldEmpty(t *testing.T) {
	if got := Fold(nil); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestFoldAddsAreReturned(t *testing.T) {
	now := time.Now().UTC()
	a, b := NewID(), NewID()
	events := []Event{
		addEv(a, now, "A"),
		addEv(b, now.Add(time.Second), "B"),
	}
	got := Fold(events)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].ID != a || got[1].ID != b {
		t.Errorf("order: want [%s %s], got [%s %s]", a, b, got[0].ID, got[1].ID)
	}
}

func TestFoldDeleteRemovesNote(t *testing.T) {
	now := time.Now().UTC()
	a := NewID()
	events := []Event{
		addEv(a, now, "A"),
		delEv(a, now.Add(time.Second)),
	}
	got := Fold(events)
	if len(got) != 0 {
		t.Fatalf("want empty after delete, got %v", got)
	}
}

// TestFoldDeleteThenAddSameIDIsNoOp: once an id is tombstoned, a future
// add of the same NoteID is ignored. (Won't happen with our API but the
// fold logic must be defensive.)
func TestFoldDeleteThenAddSameIDIsNoOp(t *testing.T) {
	now := time.Now().UTC()
	a := NewID()
	events := []Event{
		addEv(a, now, "first"),
		delEv(a, now.Add(time.Second)),
		addEv(a, now.Add(2*time.Second), "second"),
	}
	got := Fold(events)
	if len(got) != 0 {
		t.Fatalf("re-add after delete should be ignored, got %v", got)
	}
}

// TestFoldEditViaDeleteAddPreservesCreatedAt: simulate the Edit op (delete
// old + add new with original CreatedAt). Folded view should have the new
// id, new text, original CreatedAt, edit-time UpdatedAt.
func TestFoldEditViaDeleteAddPreservesCreatedAt(t *testing.T) {
	created := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	editTime := created.Add(48 * time.Hour)
	oldID, newID := NewID(), NewID()
	events := []Event{
		addEvAt(oldID, created, created, "original"),
		delEv(oldID, editTime),
		addEvAt(newID, created, editTime, "edited"),
	}
	got := Fold(events)
	if len(got) != 1 {
		t.Fatalf("want 1, got %v", got)
	}
	n := got[0]
	if n.ID != newID || n.Text != "edited" {
		t.Errorf("identity wrong: %+v", n)
	}
	if !n.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt should be preserved: got %v want %v", n.CreatedAt, created)
	}
	if !n.UpdatedAt.Equal(editTime) {
		t.Errorf("UpdatedAt should be edit time: got %v want %v", n.UpdatedAt, editTime)
	}
}

// TestFoldDeleteUnknownIDIsNoOp: we should not panic or invent state.
func TestFoldDeleteUnknownIDIsNoOp(t *testing.T) {
	events := []Event{delEv(NewID(), time.Now().UTC())}
	if got := Fold(events); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

// TestSortByRecency: most-recently-updated comes first; tie broken by ID.
func TestSortByRecency(t *testing.T) {
	old := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	notes := []Note{
		{ID: "a-id-aaaaaaaaaaaaaaaaaaaaaa", UpdatedAt: old},
		{ID: "b-id-aaaaaaaaaaaaaaaaaaaaaa", UpdatedAt: now},
		{ID: "c-id-aaaaaaaaaaaaaaaaaaaaaa", UpdatedAt: mid},
	}
	SortByRecency(notes)
	want := []string{
		"b-id-aaaaaaaaaaaaaaaaaaaaaa",
		"c-id-aaaaaaaaaaaaaaaaaaaaaa",
		"a-id-aaaaaaaaaaaaaaaaaaaaaa",
	}
	got := []string{string(notes[0].ID), string(notes[1].ID), string(notes[2].ID)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order: got %v want %v", got, want)
	}
}
