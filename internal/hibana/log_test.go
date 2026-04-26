package hibana

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTempLog(t *testing.T) (*Log, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hibana.jsonl")
	log, err := NewLog(path)
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}
	return log, path
}

func TestLogAppendThenRead(t *testing.T) {
	log, _ := newTempLog(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	want := addEv(NewID(), now, "hi")
	if err := log.Append(want); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := log.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].EventID != want.EventID || got[0].Text != want.Text {
		t.Errorf("mismatch: %+v", got[0])
	}
}

func TestLogReadEmptyFileReturnsNil(t *testing.T) {
	log, _ := newTempLog(t)
	got, err := log.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != nil {
		t.Errorf("want nil, got %d events", len(got))
	}
}

// TestLogTruncatedLastLineIsDropped simulates a crash mid-Append: file
// ends in a partial JSON without a trailing newline. Read should silently
// drop it but return everything before.
func TestLogTruncatedLastLineIsDropped(t *testing.T) {
	log, path := newTempLog(t)
	good := addEv(NewID(), time.Now().UTC(), "good")
	if err := log.Append(good); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Manually append a partial line (no closing brace, no newline).
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"eid":"01HXXTRUNC`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := log.Read()
	if err != nil {
		t.Fatalf("Read tolerated bad mid-file or surfaced trailing-line error: %v", err)
	}
	if len(got) != 1 || got[0].Text != "good" {
		t.Fatalf("expected only the good event; got %+v", got)
	}
}

// TestLogCorruptMidFileSurfacesError ensures we DO complain when a
// complete (\n-terminated) but invalid line is in the middle of the
// file — that's real corruption, not a crash artifact.
func TestLogCorruptMidFileSurfacesError(t *testing.T) {
	log, path := newTempLog(t)
	first := addEv(NewID(), time.Now().UTC(), "a")
	second := addEv(NewID(), time.Now().UTC(), "b")
	if err := log.Append(first); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json at all\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := log.Append(second); err != nil {
		t.Fatal(err)
	}

	if _, err := log.Read(); err == nil {
		t.Fatalf("expected Read to surface the mid-file corruption")
	}
}

// TestLogConcurrentAppendsArePreserved: 16 goroutines each append 25
// events. All 400 should land, every line round-trips, no duplicates.
func TestLogConcurrentAppendsArePreserved(t *testing.T) {
	log, _ := newTempLog(t)
	const goroutines = 16
	const perG = 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := log.Append(addEv(NewID(), time.Now().UTC(), "concurrent")); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got, err := log.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != goroutines*perG {
		t.Fatalf("want %d, got %d", goroutines*perG, len(got))
	}
	seen := map[ID]struct{}{}
	for _, ev := range got {
		if _, dup := seen[ev.EventID]; dup {
			t.Errorf("duplicate eid: %s", ev.EventID)
		}
		seen[ev.EventID] = struct{}{}
	}
}

func TestLogAppendBatchAllOrNothing(t *testing.T) {
	log, _ := newTempLog(t)
	a, b := NewID(), NewID()
	now := time.Now().UTC()
	batch := []Event{
		delEv(a, now),
		addEv(b, now, "new"),
	}
	if err := log.AppendBatch(batch); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	got, err := log.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].NoteID != a || got[1].NoteID != b {
		t.Errorf("order not preserved: %+v", got)
	}
}

// TestLogCompactDropsTombstonedEvents: add A, B; delete A; compact. File
// should now contain just one `add B` event.
func TestLogCompactDropsTombstonedEvents(t *testing.T) {
	log, path := newTempLog(t)
	now := time.Now().UTC()
	a, b := NewID(), NewID()
	_ = log.Append(addEv(a, now, "A"))
	_ = log.Append(addEv(b, now, "B"))
	_ = log.Append(delEv(a, now.Add(time.Second)))

	if err := log.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	got, err := log.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 surviving event, got %d", len(got))
	}
	if got[0].Op != OpAdd || got[0].Text != "B" || got[0].NoteID != b {
		t.Errorf("expected only `add B`, got %+v", got[0])
	}
	stat, _ := os.Stat(path)
	if stat == nil || stat.Size() == 0 {
		t.Fatalf("compacted file is missing or empty")
	}
}

// TestLogCompactPreservesEventIDs: the surviving add events keep their
// original EventIDs, so the sync layer's pushed-set tracking still
// matches.
func TestLogCompactPreservesEventIDs(t *testing.T) {
	log, _ := newTempLog(t)
	now := time.Now().UTC()
	noteA := NewID()
	addA := addEv(noteA, now, "A")
	noteB := NewID()
	addB := addEv(noteB, now.Add(time.Second), "B")
	_ = log.Append(addA)
	_ = log.Append(addB)
	_ = log.Append(delEv(noteA, now.Add(2*time.Second)))

	if err := log.Compact(); err != nil {
		t.Fatal(err)
	}
	got, _ := log.Read()
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].EventID != addB.EventID {
		t.Errorf("compact dropped EventID: got %s, want %s", got[0].EventID, addB.EventID)
	}
}

func TestLogCompactPreservesOriginalOnTmpFailure(t *testing.T) {
	log, path := newTempLog(t)
	now := time.Now().UTC()
	if err := log.Append(addEv(NewID(), now, "x")); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)

	tmp := path + ".compact.tmp"
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := log.Compact(); err == nil {
		t.Fatalf("expected Compact to fail with tmp blocked")
	}
	after, _ := os.ReadFile(path)
	if string(original) != string(after) {
		t.Fatalf("original log was mutated despite Compact failure")
	}
}

func TestLogShouldCompactThresholds(t *testing.T) {
	log, _ := newTempLog(t)
	now := time.Now().UTC()
	for i := 0; i < 50; i++ {
		_ = log.Append(addEv(NewID(), now, "x"))
	}
	if want, _ := log.ShouldCompact(); want {
		t.Errorf("under-floor file should not trigger compact")
	}
}

// regression: parsing should tolerate blank trailing line.
func TestLogReadTolerantOfTrailingBlank(t *testing.T) {
	log, path := newTempLog(t)
	_ = log.Append(addEv(NewID(), time.Now().UTC(), "x"))
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("\n")
	f.Close()
	if _, err := log.Read(); err != nil {
		t.Fatalf("trailing blank line should not error: %v", err)
	}
}

func TestLogAppendCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	log, err := NewLog(filepath.Join(deep, "hibana.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(addEv(NewID(), time.Now().UTC(), "x")); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(filepath.Join(deep, "hibana.jsonl"))
	if err != nil || stat.Size() == 0 {
		t.Fatalf("expected non-empty log at %s, err=%v", deep, err)
	}
}
