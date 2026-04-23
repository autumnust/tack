package planning

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/autumnust/tack/internal/model"
)

// Helper: make the fake backend fail every call, simulating offline. Resets
// after the test via t.Cleanup.
func goOffline(fb *fakeBackend) func() {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	prev := fb.failNext
	fb.failNext = errors.New("offline")
	// Wrap so every call re-arms the error until the returned restore runs.
	fb.sticky = true
	return func() {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		fb.sticky = false
		fb.failNext = prev
	}
}

func TestReconcile_AppendOnlyUsageReplaysAfterReconnect(t *testing.T) {
	s, fb := storeWithFake(t)

	// Go offline and log usage twice.
	restore := goOffline(fb)
	s.LogUsage("plan.add")
	s.LogUsage("plan.done")
	restore()

	if s.OutboxPending() != 2 {
		t.Fatalf("expected 2 buffered ops, got %d", s.OutboxPending())
	}

	rep, err := s.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Replayed != 2 {
		t.Errorf("expected 2 replayed, got %d", rep.Replayed)
	}
	if got := len(fb.lists[keyUsage]); got != 2 {
		t.Errorf("expected 2 usage entries in Redis, got %d", got)
	}
	if s.OutboxPending() != 0 {
		t.Errorf("expected empty outbox, got %d", s.OutboxPending())
	}
}

func TestReconcile_BlobNoConflictPushes(t *testing.T) {
	s, fb := storeWithFake(t)

	// Go offline, save plan.
	restore := goOffline(fb)
	if err := s.SavePlan(&model.Plan{Today: []model.TodoItem{{Text: "offline-edit"}}}); err != nil {
		t.Fatal(err)
	}
	restore()

	// Reconcile should push plan and bump rev.
	rep, err := s.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", rep.Conflicts)
	}
	if len(rep.Pushed) == 0 || rep.Pushed[0] != keyPlan {
		t.Errorf("expected push of tack:plan, got %+v", rep.Pushed)
	}
	if got := s.syncState.Rev(keyPlan); got != 1 {
		t.Errorf("expected cached rev 1, got %d", got)
	}
	if fb.counters[keyPlanRev] != 1 {
		t.Errorf("expected remote rev 1, got %d", fb.counters[keyPlanRev])
	}
	if !strings.Contains(fb.kv[keyPlan], "offline-edit") {
		t.Errorf("remote plan missing local edit: %s", fb.kv[keyPlan])
	}
}

func TestReconcile_BlobConflictPersists(t *testing.T) {
	s, fb := storeWithFake(t)

	// Simulate: we last synced at rev 1; remote is now at rev 2 with a
	// different blob (written by another device); we have a pending local
	// write at rev 1.
	_ = s.syncState.SetRev(keyPlan, 1)
	fb.counters[keyPlanRev] = 2
	fb.kv[keyPlanRev] = "2"
	remote, _ := json.Marshal(&model.Plan{Today: []model.TodoItem{{Text: "device-B"}}})
	fb.kv[keyPlan] = string(remote)

	local, _ := json.Marshal(&model.Plan{Today: []model.TodoItem{{Text: "device-A-offline"}}})
	_ = s.outbox.Append(OutboxOp{TS: time.Now(), Op: "set", Key: keyPlan, Payload: string(local)})

	rep, err := s.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", rep.Conflicts)
	}
	c := rep.Conflicts[0]
	if c.Key != keyPlan || c.CachedRev != 1 || c.RemoteRev != 2 {
		t.Errorf("unexpected conflict: %+v", c)
	}
	if !strings.Contains(c.LocalPayload, "device-A") || !strings.Contains(c.RemotePayload, "device-B") {
		t.Errorf("conflict payloads wrong: %+v", c)
	}
	// Outbox should still carry the op for later resolution.
	if s.OutboxPending() != 1 {
		t.Errorf("expected outbox to retain op, got %d", s.OutboxPending())
	}
	// Conflict persisted to disk.
	cs, err := s.LoadConflicts()
	if err != nil || len(cs) != 1 {
		t.Errorf("expected 1 persisted conflict, got %v err=%v", cs, err)
	}
}

func TestReconcile_PullsRemoteRevWhenNoPending(t *testing.T) {
	s, fb := storeWithFake(t)
	_ = s.syncState.SetRev(keyPlan, 1)
	fb.counters[keyPlanRev] = 5
	fb.kv[keyPlanRev] = "5"

	rep, err := s.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Pulled) != 1 || rep.Pulled[0] != keyPlan {
		t.Errorf("expected pull of plan, got %+v", rep.Pulled)
	}
	if s.syncState.Rev(keyPlan) != 5 {
		t.Errorf("expected cached rev 5, got %d", s.syncState.Rev(keyPlan))
	}
}

func TestReconcile_RecapConflict(t *testing.T) {
	s, fb := storeWithFake(t)
	// Remote has different content for the same week.
	fb.kv[recapKey("2026-W16")] = "# remote body"

	_ = s.outbox.Append(OutboxOp{TS: time.Now(), Op: "set", Key: recapKey("2026-W16"), Payload: "# local body"})

	rep, err := s.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", rep.Conflicts)
	}
	if rep.Conflicts[0].Key != recapKey("2026-W16") {
		t.Errorf("unexpected key: %s", rep.Conflicts[0].Key)
	}
}

func TestLogUsage_ReadsBackFromRedis(t *testing.T) {
	s, _ := storeWithFake(t)
	s.LogUsage("plan.add")
	s.LogUsage("plan.add")
	s.LogUsage("plan.done")

	stats, err := s.LoadUsageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["plan.add"] != 2 || stats["plan.done"] != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

func TestSaveRecap_WritesRedisAndIndex(t *testing.T) {
	s, fb := storeWithFake(t)
	if err := s.SaveRecap("2026-W16", "# hi"); err != nil {
		t.Fatal(err)
	}
	if fb.kv[recapKey("2026-W16")] != "# hi" {
		t.Errorf("recap not in redis: %v", fb.kv)
	}
	if _, ok := fb.sets[keyRecapsIndex]["2026-W16"]; !ok {
		t.Errorf("recap name not in index: %+v", fb.sets[keyRecapsIndex])
	}

	names, err := s.ListRecaps()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "2026-W16" {
		t.Errorf("ListRecaps wrong: %+v", names)
	}

	body, err := s.LoadRecap("2026-W16")
	if err != nil {
		t.Fatal(err)
	}
	if body != "# hi" {
		t.Errorf("LoadRecap wrong: %q", body)
	}
}

// Ensure the "offline" takeFail sticky helper really re-arms.
var _ context.Context = context.Background()

// Deletes made this session must survive the merge, even though remote
// still has them.
func TestSavePlan_LocalDeletesSurviveMerge(t *testing.T) {
	s, fb := storeWithFake(t)

	t0 := time.Now().Truncate(time.Second)
	a := model.ScratchNote{Text: "keep", CreatedAt: t0}
	b := model.ScratchNote{Text: "delete-me", CreatedAt: t0.Add(time.Second)}
	c := model.ScratchNote{Text: "remote-added", CreatedAt: t0.Add(5 * time.Second)}

	// Seed remote with a, b, and c (c was appended by another device).
	for _, n := range []model.ScratchNote{a, b, c} {
		blob, _ := json.Marshal(n)
		fb.lists[keyHibana] = append(fb.lists[keyHibana], string(blob))
	}

	// Simulate the load path: LoadPlan would have captured {a, b} as the
	// snapshot. (c was added after.) We capture {a, b} manually to model
	// that state.
	s.captureHibanaSnapshot([]model.ScratchNote{a, b})

	// User deletes b during the session — local now has only {a}.
	if err := s.SavePlan(&model.Plan{Scratch: []model.ScratchNote{a}}); err != nil {
		t.Fatal(err)
	}

	// Expected: a (kept) + c (remote-added) — b is gone.
	var texts []string
	for _, raw := range fb.lists[keyHibana] {
		var n model.ScratchNote
		_ = json.Unmarshal([]byte(raw), &n)
		texts = append(texts, n.Text)
	}
	if len(texts) != 2 {
		t.Fatalf("expected 2 entries after merge, got %v", texts)
	}
	if texts[0] != "keep" || texts[1] != "remote-added" {
		t.Errorf("merge result wrong: %v (want keep, remote-added)", texts)
	}
}

// SavePlan must not wipe hibana notes another device added since we loaded.
func TestSavePlan_PreservesRemoteHibanaAppends(t *testing.T) {
	s, fb := storeWithFake(t)

	t0 := time.Now().Truncate(time.Second)
	local := []model.ScratchNote{
		{Text: "mine-1", CreatedAt: t0},
		{Text: "mine-2", CreatedAt: t0.Add(time.Second)},
	}
	// Seed Redis as if another device appended two notes after our load.
	extras := []model.ScratchNote{
		{Text: "theirs-1", CreatedAt: t0.Add(10 * time.Second)},
		{Text: "theirs-2", CreatedAt: t0.Add(11 * time.Second)},
	}
	all := append([]model.ScratchNote{}, local...)
	all = append(all, extras...)
	for _, n := range all {
		b, _ := json.Marshal(n)
		fb.lists[keyHibana] = append(fb.lists[keyHibana], string(b))
	}

	if err := s.SavePlan(&model.Plan{Scratch: local}); err != nil {
		t.Fatal(err)
	}

	// After save, remote list must still have all 4 entries, local order first.
	if got := len(fb.lists[keyHibana]); got != 4 {
		t.Fatalf("expected 4 merged hibana entries, got %d", got)
	}
	var texts []string
	for _, raw := range fb.lists[keyHibana] {
		var n model.ScratchNote
		_ = json.Unmarshal([]byte(raw), &n)
		texts = append(texts, n.Text)
	}
	want := []string{"mine-1", "mine-2", "theirs-1", "theirs-2"}
	for i, w := range want {
		if texts[i] != w {
			t.Errorf("entry %d: got %q want %q (full: %v)", i, texts[i], w, texts)
		}
	}
}
