package hibana

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T, backend Backend) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, backend)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestSyncPushesLocalAdds(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	n1, _ := s.Add("first")
	n2, _ := s.Add("second")

	rep := s.Sync(context.Background())
	if rep.Err != nil {
		t.Fatalf("sync err: %v", rep.Err)
	}
	if rep.Pushed != 2 {
		t.Errorf("want 2 pushed, got %d", rep.Pushed)
	}
	if be.hashLen(HashKey) != 2 {
		t.Errorf("hash should have 2 entries, got %d", be.hashLen(HashKey))
	}
	if _, ok := be.hashGet(HashKey, string(n1.ID)); !ok {
		t.Errorf("n1 missing from remote hash")
	}
	if _, ok := be.hashGet(HashKey, string(n2.ID)); !ok {
		t.Errorf("n2 missing from remote hash")
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	_, _ = s.Add("hello")
	first := s.Sync(context.Background())
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	second := s.Sync(context.Background())
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	if second.Pushed != 0 {
		t.Errorf("second sync should push 0, got %d", second.Pushed)
	}
}

func TestSyncDeleteHidesFromRemote(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	n, _ := s.Add("ephemeral")
	if err := s.Delete(n.ID); err != nil {
		t.Fatal(err)
	}
	rep := s.Sync(context.Background())
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if be.hashLen(HashKey) != 0 {
		t.Errorf("hash should be empty after add+delete sync, got %d", be.hashLen(HashKey))
	}
	if !be.graveHas(GravesKey, string(n.ID)) {
		t.Errorf("graves should contain deleted id")
	}
}

func TestSyncEditKeepsIDOnRemote(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	n, _ := s.Add("v1")
	s.Sync(context.Background())
	if be.hashLen(HashKey) != 1 {
		t.Fatalf("setup: want 1 in hash, got %d", be.hashLen(HashKey))
	}

	updated, err := s.Edit(n.ID, "v2")
	if err != nil {
		t.Fatal(err)
	}
	rep := s.Sync(context.Background())
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if be.hashLen(HashKey) != 1 {
		t.Errorf("hash should still have 1 entry after edit; got %d", be.hashLen(HashKey))
	}
	if updated.ID != n.ID {
		t.Fatalf("edit changed id: got %s want %s", updated.ID, n.ID)
	}
	raw, ok := be.hashGet(HashKey, string(n.ID))
	if !ok {
		t.Fatalf("stable id missing from hash")
	}
	var remote Note
	if err := json.Unmarshal([]byte(raw), &remote); err != nil {
		t.Fatal(err)
	}
	if remote.Text != "v2" {
		t.Errorf("remote text = %q, want v2", remote.Text)
	}
	if be.graveHas(GravesKey, string(n.ID)) {
		t.Errorf("edited id must not be marked deleted")
	}
}

func TestSyncPullRecognizesRemoteEditWithKnownID(t *testing.T) {
	be := newFakeBackend()
	a, _ := newTestStore(t, be)
	b, _ := newTestStore(t, be)

	n, _ := a.Add("v1")
	if r := a.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := b.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, err := a.Edit(n.ID, "v2"); err != nil {
		t.Fatal(err)
	}
	if r := a.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}

	rep := b.Sync(context.Background())
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if rep.Pulled != 1 {
		t.Fatalf("pulled = %d, want 1", rep.Pulled)
	}
	notes, _ := b.List()
	if len(notes) != 1 || notes[0].ID != n.ID || notes[0].Text != "v2" {
		t.Fatalf("known note was not updated: %+v", notes)
	}
}

func TestPullRecognizesRemoteEditWithEqualOrOlderTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
	}{
		{name: "equal", offset: 0},
		{name: "older", offset: -time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be := newFakeBackend()
			s, _ := newTestStore(t, be)
			note, _ := s.Add("local base")
			if report := s.Sync(context.Background()); report.Err != nil {
				t.Fatal(report.Err)
			}
			base := note.UpdatedAt
			remote := Note{ID: note.ID, Text: "remote current", CreatedAt: note.CreatedAt, UpdatedAt: base.Add(tc.offset)}
			data, _ := json.Marshal(remote)
			be.putHash(HashKey, string(note.ID), string(data))

			report := s.Pull(context.Background())
			if report.Err != nil || report.Pulled != 1 {
				t.Fatalf("pull report = %+v", report)
			}
			notes, _ := s.List()
			if len(notes) != 1 || notes[0].Text != "remote current" {
				t.Fatalf("notes = %+v", notes)
			}
			if next := s.Sync(context.Background()); next.Err != nil || next.Pushed != 0 || next.Pulled != 0 {
				t.Fatalf("next sync echoed pulled text: %+v", next)
			}
		})
	}
}

func TestSyncRemoteDeleteWinsOverStaleEdit(t *testing.T) {
	be := newFakeBackend()
	a, _ := newTestStore(t, be)
	b, _ := newTestStore(t, be)

	n, _ := a.Add("shared")
	a.Sync(context.Background())
	b.Sync(context.Background())
	if err := a.Delete(n.ID); err != nil {
		t.Fatal(err)
	}
	a.Sync(context.Background())
	if _, err := b.Edit(n.ID, "stale edit"); err != nil {
		t.Fatal(err)
	}

	rep := b.Sync(context.Background())
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if _, ok := be.hashGet(HashKey, string(n.ID)); ok {
		t.Fatal("stale edit restored a remotely deleted note")
	}
	if !be.graveHas(GravesKey, string(n.ID)) {
		t.Fatal("remote deletion marker was removed")
	}
	notes, _ := b.List()
	if len(notes) != 0 {
		t.Fatalf("stale device still has deleted note: %+v", notes)
	}
}

// TestSyncPullRemoteOnlyAdd: another device pushed a note we don't have.
// Pull should append it to our local log and surface it via List.
func TestSyncPullRemoteOnlyAdd(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)

	// Pre-load remote state as if device B pushed.
	id := NewID()
	now := time.Now().UTC()
	noteJSON, _ := json.Marshal(Note{ID: id, Text: "from B", CreatedAt: now, UpdatedAt: now})
	be.putHash(HashKey, string(id), string(noteJSON))

	rep := s.Sync(context.Background())
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if rep.Pulled != 1 {
		t.Errorf("want 1 pulled, got %d", rep.Pulled)
	}
	notes, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Text != "from B" {
		t.Errorf("local list should reflect pulled note, got %+v", notes)
	}
}

// TestSyncPullRemoteDelete: we have a note locally; remote graves it.
// Pull should append a delete event so our local fold drops it.
func TestSyncPullRemoteDelete(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	n, _ := s.Add("doomed")
	s.Sync(context.Background()) // push it up

	// Simulate device B deleting it: remove from hash, add to graves.
	be.HDel(context.Background(), HashKey, string(n.ID))
	be.putGrave(GravesKey, string(n.ID))

	rep := s.Sync(context.Background())
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if rep.Pulled != 1 {
		t.Errorf("want 1 pulled (the synthetic delete), got %d", rep.Pulled)
	}
	notes, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("local list should be empty after pulling delete, got %+v", notes)
	}
}

// TestSyncPullDoesNotEchoBack: after a pull, the next push should NOT
// re-emit the events we just pulled (they came from Redis).
func TestSyncPullDoesNotEchoBack(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)

	id := NewID()
	now := time.Now().UTC()
	nj, _ := json.Marshal(Note{ID: id, Text: "echo test", CreatedAt: now, UpdatedAt: now})
	be.putHash(HashKey, string(id), string(nj))

	s.Sync(context.Background()) // pull
	before := be.hashLen(HashKey)
	be.calls = nil
	s.Sync(context.Background()) // should be a no-op
	after := be.hashLen(HashKey)
	if before != after {
		t.Errorf("remote hash size changed across echo sync: %d → %d", before, after)
	}
	for _, c := range be.calls {
		if c == "HSET" {
			t.Errorf("HSET issued during echo sync (should be skipped)")
		}
	}
}

// TestSyncTwoDeviceConvergence: A and B each add a note while disconnected
// from each other. After both sync against shared Redis, both end up with
// both notes.
func TestSyncTwoDeviceConvergence(t *testing.T) {
	be := newFakeBackend()
	dirA := t.TempDir()
	dirB := t.TempDir()
	a, _ := Open(dirA, be)
	b, _ := Open(dirB, be)

	_, _ = a.Add("from A")
	_, _ = b.Add("from B")

	// First sync round: A pushes, then B pushes+pulls A.
	if r := a.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := b.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}
	// Second round: A pulls B.
	if r := a.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}

	la, _ := a.List()
	lb, _ := b.List()
	if len(la) != 2 || len(lb) != 2 {
		t.Fatalf("convergence broken: A=%d B=%d", len(la), len(lb))
	}
}

// TestSyncDeleteRaceBetweenDevicesIsIdempotent: A deletes note X, B also
// deletes note X (concurrently, both with stale views). After sync, X is
// gone everywhere, exactly once in graves.
func TestSyncDeleteRaceIdempotent(t *testing.T) {
	be := newFakeBackend()
	dirA := t.TempDir()
	dirB := t.TempDir()
	a, _ := Open(dirA, be)
	b, _ := Open(dirB, be)

	// Setup: A adds, A and B both sync so both have it.
	n, _ := a.Add("shared")
	a.Sync(context.Background())
	b.Sync(context.Background())

	// Both delete independently.
	if err := a.Delete(n.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(n.ID); err != nil {
		t.Fatal(err)
	}
	a.Sync(context.Background())
	b.Sync(context.Background())

	if be.hashLen(HashKey) != 0 {
		t.Errorf("hash should be empty, got %d", be.hashLen(HashKey))
	}
	if be.graveLen(GravesKey) != 1 {
		t.Errorf("graves should hold exactly 1 id, got %d", be.graveLen(GravesKey))
	}

	la, _ := a.List()
	lb, _ := b.List()
	if len(la) != 0 || len(lb) != 0 {
		t.Fatalf("both should see empty list; A=%v B=%v", la, lb)
	}
}

// TestSyncPushFailureLeavesPushedSetUnchanged: if HSET fails, the cursor
// should not advance, so a retry re-attempts the same event.
func TestSyncPushFailureRetries(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	_, _ = s.Add("flaky")

	be.failOnce("HSET")
	rep := s.Sync(context.Background())
	if rep.Err == nil {
		t.Fatalf("expected err on first sync")
	}
	if be.hashLen(HashKey) != 0 {
		t.Errorf("hash should remain empty after failure")
	}

	rep2 := s.Sync(context.Background())
	if rep2.Err != nil {
		t.Fatalf("retry should succeed: %v", rep2.Err)
	}
	if be.hashLen(HashKey) != 1 {
		t.Errorf("retry should land the event, got %d", be.hashLen(HashKey))
	}
}

// TestSyncOfflineThenOnlineRecoversOfflineNote: simulates the Corgi Cafe
// scenario. Add note while backend is "offline" (we use NopBackend then
// swap to a real fake), then sync. The note must reach Redis.
func TestSyncOfflineThenOnlineRecoversOfflineNote(t *testing.T) {
	dir := t.TempDir()
	// Phase 1: offline. NopBackend.
	s1, err := Open(dir, NopBackend())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := s1.Add("written from Corgi Cafe")
	// Sync is a no-op when backend disabled.
	s1.Sync(context.Background())

	// Phase 2: online. Same dir, real backend.
	be := newFakeBackend()
	s2, err := Open(dir, be)
	if err != nil {
		t.Fatal(err)
	}
	rep := s2.Sync(context.Background())
	if rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if rep.Pushed != 1 {
		t.Errorf("want 1 pushed, got %d", rep.Pushed)
	}
	if _, ok := be.hashGet(HashKey, string(n.ID)); !ok {
		t.Errorf("offline note never reached Redis")
	}
	pending, _ := s2.PendingPush()
	if pending != 0 {
		t.Errorf("PendingPush should be 0, got %d", pending)
	}
}

// TestPendingPushReportsUnpushed: after Add but before Sync, PendingPush
// should be N.
func TestPendingPushReports(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	for i := 0; i < 3; i++ {
		_, _ = s.Add("x")
	}
	pending, err := s.PendingPush()
	if err != nil {
		t.Fatal(err)
	}
	if pending != 3 {
		t.Errorf("want 3 pending, got %d", pending)
	}
}

// TestSyncWithDisabledBackendIsNoOp: NopBackend.Enabled() is false.
func TestSyncWithDisabledBackendIsNoOp(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	_, _ = s.Add("local-only")
	rep := s.Sync(context.Background())
	if rep.Pushed != 0 || rep.Pulled != 0 || rep.Err != nil {
		t.Errorf("disabled backend should produce empty report, got %+v", rep)
	}
	notes, _ := s.List()
	if len(notes) != 1 {
		t.Errorf("local read should still work")
	}
}

// TestCompactRestrictsPushedSet: after Add → Delete → Sync → Compact, the
// pushed-set file should no longer reference the dropped events.
func TestCompactRestrictsPushedSet(t *testing.T) {
	be := newFakeBackend()
	dir := t.TempDir()
	s, _ := Open(dir, be)

	n, _ := s.Add("doomed")
	s.Delete(n.ID)
	if r := s.Sync(context.Background()); r.Err != nil {
		t.Fatal(r.Err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	// After compact: log is empty (the only events were the doomed ones).
	notes, _ := s.List()
	if len(notes) != 0 {
		t.Errorf("compact should leave 0 live notes, got %d", len(notes))
	}

	// pushed-set file should now be empty (no surviving event ids to track).
	data, _ := os.ReadFile(filepath.Join(dir, ".sync", "hibana.pushed"))
	if len(data) != 0 {
		t.Errorf("pushed-set file should be empty after compact; got %d bytes", len(data))
	}
}
