package planning

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOutbox_AppendLoadRewrite(t *testing.T) {
	dir := t.TempDir()
	ob := newOutbox(dir)

	if n := ob.Len(); n != 0 {
		t.Fatalf("empty outbox should be 0, got %d", n)
	}

	now := time.Now().Truncate(time.Second)
	ops := []OutboxOp{
		{TS: now, Op: "set", Key: "tack:plan", Payload: `{"a":1}`},
		{TS: now.Add(time.Second), Op: "rpush", Key: "tack:usage", Payload: "hello"},
		{TS: now.Add(2 * time.Second), Op: "set", Key: "tack:recap:2026-W16", Payload: "# hi"},
	}
	for _, op := range ops {
		if err := ob.Append(op); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := ob.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3, got %d", len(loaded))
	}
	for i, op := range loaded {
		if op.Key != ops[i].Key || op.Op != ops[i].Op || op.Payload != ops[i].Payload {
			t.Errorf("op %d mismatch: got %+v want %+v", i, op, ops[i])
		}
	}

	// Partial drain: keep only the last op.
	if err := ob.Rewrite(loaded[2:]); err != nil {
		t.Fatal(err)
	}
	remaining, err := ob.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Key != "tack:recap:2026-W16" {
		t.Errorf("partial rewrite wrong: %+v", remaining)
	}

	// Full drain removes the file.
	if err := ob.Rewrite(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".outbox.jsonl")); !os.IsNotExist(err) {
		t.Errorf("expected outbox file to be removed, err=%v", err)
	}
}

func TestOutbox_SkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".outbox.jsonl")
	_ = os.WriteFile(path, []byte("not json\n{\"op\":\"set\",\"key\":\"k\"}\n"), 0644)

	ob := newOutbox(dir)
	ops, err := ob.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Key != "k" {
		t.Errorf("expected 1 valid op, got %+v", ops)
	}
}

func TestSyncState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	ss := newSyncState(dir)
	if ss.Rev("tack:plan") != 0 {
		t.Errorf("fresh state should return 0")
	}
	if err := ss.SetRev("tack:plan", 7); err != nil {
		t.Fatal(err)
	}
	if err := ss.SetRev("tack:annotations", 2); err != nil {
		t.Fatal(err)
	}

	// Reload from disk — must persist.
	ss2 := newSyncState(dir)
	if ss2.Rev("tack:plan") != 7 {
		t.Errorf("expected rev 7, got %d", ss2.Rev("tack:plan"))
	}
	if ss2.Rev("tack:annotations") != 2 {
		t.Errorf("expected rev 2, got %d", ss2.Rev("tack:annotations"))
	}
}
