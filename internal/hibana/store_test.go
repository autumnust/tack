package hibana

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStoreAddListDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, NopBackend())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Add("alpha")
	b, _ := s.Add("beta")

	notes, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("want 2, got %d", len(notes))
	}
	if notes[0].Text != "alpha" || notes[1].Text != "beta" {
		t.Errorf("order: %+v", notes)
	}

	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	notes, _ = s.List()
	if len(notes) != 1 || notes[0].ID != b.ID {
		t.Errorf("delete failed: %+v", notes)
	}
}

func TestStoreEditPreservesIDAndCreatedAt(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	n, _ := s.Add("v1")
	originalCreated := n.CreatedAt
	originalID := n.ID

	// Force time to advance for UpdatedAt.
	prev := nowFunc
	nowFunc = func() time.Time { return originalCreated.Add(2 * time.Hour) }
	defer func() { nowFunc = prev }()

	updated, err := s.Edit(n.ID, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != originalID {
		t.Errorf("edit changed id: got %s want %s", updated.ID, originalID)
	}
	if !updated.CreatedAt.Equal(originalCreated) {
		t.Errorf("CreatedAt must be preserved: got %v want %v", updated.CreatedAt, originalCreated)
	}
	if updated.Text != "v2" {
		t.Errorf("text not updated: %s", updated.Text)
	}

	notes, _ := s.List()
	if len(notes) != 1 {
		t.Fatalf("expected 1 live note after edit, got %d", len(notes))
	}
	if notes[0].ID != updated.ID || notes[0].Text != "v2" {
		t.Errorf("list shows wrong note: %+v", notes[0])
	}
}

func TestStoreEditUnknownIDFails(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	_, err := s.Edit(NewID(), "nope")
	if err == nil {
		t.Fatal("expected error editing unknown id")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found, got: %v", err)
	}
}

// TestStoreDeleteUnknownIDIsTolerant: idempotent — caller may have a stale
// view; we must not error.
func TestStoreDeleteUnknownIDIsTolerant(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	if err := s.Delete(NewID()); err != nil {
		t.Errorf("delete on unknown id should not error: %v", err)
	}
}

// TestStorePersistsAcrossInstances: simulates a restart — close and re-Open.
func TestStorePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	s1, _ := Open(dir, NopBackend())
	_, _ = s1.Add("persistent")

	s2, err := Open(dir, NopBackend())
	if err != nil {
		t.Fatal(err)
	}
	notes, _ := s2.List()
	if len(notes) != 1 || notes[0].Text != "persistent" {
		t.Errorf("post-restart read: %+v", notes)
	}
}

func TestStoreAddWithTimestampsForMigration(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	created := time.Date(2026, 4, 25, 19, 11, 29, 0, time.UTC)
	updated := created.Add(time.Hour)
	n, err := s.AddWithTimestamps("migrated", created, updated)
	if err != nil {
		t.Fatal(err)
	}
	if !n.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt: got %v want %v", n.CreatedAt, created)
	}
	if !n.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt: got %v want %v", n.UpdatedAt, updated)
	}

	// And it shows up in List with those timestamps.
	notes, _ := s.List()
	if len(notes) != 1 || !notes[0].CreatedAt.Equal(created) {
		t.Errorf("List: %+v", notes)
	}
}

// TestStoreSyncIntegratesEndToEnd: add → sync → restart → list still shows it
// without re-pulling (because local log is authoritative).
func TestStoreSyncEndToEnd(t *testing.T) {
	be := newFakeBackend()
	dir := t.TempDir()
	s, _ := Open(dir, be)
	_, _ = s.Add("one")
	_, _ = s.Add("two")
	if r := s.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}

	s2, _ := Open(dir, be)
	notes, _ := s2.List()
	if len(notes) != 2 {
		t.Errorf("post-restart count: %d", len(notes))
	}
	pending, _ := s2.PendingPush()
	if pending != 0 {
		t.Errorf("post-sync PendingPush should be 0, got %d", pending)
	}
}

// TestStoreMaybeCompactNoOpWhenSmall ensures the threshold guard works.
func TestStoreMaybeCompactNoOpWhenSmall(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	for i := 0; i < 10; i++ {
		_, _ = s.Add("x")
	}
	if err := s.MaybeCompact(); err != nil {
		t.Errorf("MaybeCompact small: %v", err)
	}
	notes, _ := s.List()
	if len(notes) != 10 {
		t.Errorf("MaybeCompact should have been a no-op, got %d notes", len(notes))
	}
}
