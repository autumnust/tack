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
