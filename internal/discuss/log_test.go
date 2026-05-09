package discuss

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLog_AppendAndRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	did := NewID()
	log, err := NewLog(filepath.Join(dir, "discussions", string(did)+".jsonl"))
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	events := []Event{
		{
			EventID:      NewID(),
			DiscussionID: did,
			Op:           OpCreated,
			TS:           now,
			SystemPrompt: "be terse",
			Model:        "claude-sonnet-4-6",
			SeedNotes: []Seed{
				{NoteID: "01HX0000000000000000000000", Text: "one", CreatedAt: now},
			},
		},
		{
			EventID:      NewID(),
			DiscussionID: did,
			Op:           OpUserMsg,
			TS:           now.Add(time.Second),
			Text:         "hello",
		},
		{
			EventID:      NewID(),
			DiscussionID: did,
			Op:           OpAssistant,
			TS:           now.Add(2 * time.Second),
			Text:         "hi back",
		},
		{
			EventID:      NewID(),
			DiscussionID: did,
			Op:           OpClosed,
			TS:           now.Add(3 * time.Second),
		},
	}
	for _, ev := range events {
		if err := log.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := log.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("event count: got %d, want %d", len(got), len(events))
	}
	for i, ev := range events {
		if got[i].EventID != ev.EventID {
			t.Errorf("event %d EventID: got %s, want %s", i, got[i].EventID, ev.EventID)
		}
		if got[i].Op != ev.Op {
			t.Errorf("event %d Op: got %s, want %s", i, got[i].Op, ev.Op)
		}
		if got[i].Text != ev.Text {
			t.Errorf("event %d Text: got %q, want %q", i, got[i].Text, ev.Text)
		}
	}
	if got[0].SystemPrompt != "be terse" {
		t.Errorf("created event lost SystemPrompt: %q", got[0].SystemPrompt)
	}
	if len(got[0].SeedNotes) != 1 || got[0].SeedNotes[0].Text != "one" {
		t.Errorf("created event lost SeedNotes: %+v", got[0].SeedNotes)
	}
}

func TestLog_ReadMissingFile(t *testing.T) {
	dir := t.TempDir()
	log, err := NewLog(filepath.Join(dir, "missing", "x.jsonl"))
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}
	got, err := log.Read()
	if err != nil {
		t.Fatalf("Read on missing file should be nil err, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil events on missing file, got %v", got)
	}
}

func TestLog_TruncatedTrailingLineDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	did := NewID()
	good := Event{EventID: NewID(), DiscussionID: did, Op: OpUserMsg, TS: time.Now(), Text: "ok"}
	line, _ := good.MarshalLine()
	// Append one good line + a partial second line (no newline, no closing brace).
	partial := []byte(`{"eid":"01HX","did":"`)
	if err := os.WriteFile(path, append(line, partial...), 0o644); err != nil {
		t.Fatal(err)
	}
	log, _ := NewLog(path)
	got, err := log.Read()
	if err != nil {
		t.Fatalf("Read should tolerate truncated trailing line, got err %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 event (partial dropped), got %d", len(got))
	}
}

func TestNewID_Uniqueness(t *testing.T) {
	seen := map[ID]bool{}
	for i := 0; i < 10000; i++ {
		id := NewID()
		if !ValidID(string(id)) {
			t.Fatalf("invalid id: %s", id)
		}
		if seen[id] {
			t.Fatalf("collision at %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestResolveSystemPrompt_DefaultsAndOverride(t *testing.T) {
	if got := ResolveSystemPrompt(""); got != DefaultSystemPrompt {
		t.Errorf("empty config should resolve to DefaultSystemPrompt")
	}
	if got := ResolveSystemPrompt("custom"); got != "custom" {
		t.Errorf("override should win: got %q", got)
	}
}

func TestResolveModel_DefaultsAndOverride(t *testing.T) {
	if got := ResolveModel(""); got != DefaultModel {
		t.Errorf("empty config should resolve to DefaultModel")
	}
	if got := ResolveModel("claude-opus-4-7"); got != "claude-opus-4-7" {
		t.Errorf("override should win: got %q", got)
	}
}

func TestLogPath_AndListDiscussions(t *testing.T) {
	dir := t.TempDir()
	id1 := NewID()
	id2 := NewID()
	for _, id := range []ID{id1, id2} {
		log, err := NewLog(LogPath(dir, id))
		if err != nil {
			t.Fatal(err)
		}
		_ = log.Append(Event{EventID: NewID(), DiscussionID: id, Op: OpCreated, TS: time.Now()})
	}
	ids, err := ListDiscussions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 discussions, got %d", len(ids))
	}
}
