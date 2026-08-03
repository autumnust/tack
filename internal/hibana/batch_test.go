package hibana

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func batchNote(id ID, text string) Note {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	return Note{ID: id, Text: text, CreatedAt: now, UpdatedAt: now}
}

func batchDoc(path string, note Note) BatchDocument {
	return BatchDocument{Path: path, ID: note.ID, Text: note.Text}
}

func countBatchChanges(plan BatchPlan) (adds, edits, deletes int) {
	for _, change := range plan.Changes {
		switch change.Kind {
		case BatchAdd:
			adds++
		case BatchEdit:
			edits++
		case BatchDelete:
			deletes++
		}
	}
	return
}

func TestPlanBatchThreeWayCases(t *testing.T) {
	idA, idB, idC, idD := NewID(), NewID(), NewID(), NewID()
	a, b, c := batchNote(idA, "A"), batchNote(idB, "B"), batchNote(idC, "C")
	remoteA := a
	remoteA.Text = "A remote"
	remoteA.UpdatedAt = remoteA.UpdatedAt.Add(time.Hour)

	tests := []struct {
		name          string
		base          []Note
		docs          []BatchDocument
		current       []Note
		wantAdds      int
		wantEdits     int
		wantDeletes   int
		wantConflicts int
		wantErr       string
	}{
		{name: "unchanged", base: []Note{a}, docs: []BatchDocument{batchDoc("001-"+string(idA)+".md", a)}, current: []Note{a}},
		{name: "edit", base: []Note{a}, docs: []BatchDocument{{Path: "a.md", ID: idA, Text: "A local"}}, current: []Note{a}, wantEdits: 1},
		{name: "delete", base: []Note{a}, current: []Note{a}, wantDeletes: 1},
		{name: "add", docs: []BatchDocument{{Path: "new.md", Text: "new"}}, current: nil, wantAdds: 1},
		{name: "rename", base: []Note{a}, docs: []BatchDocument{batchDoc("renamed-"+string(idA)+".md", a)}, current: []Note{a}},
		{name: "remote only add", base: []Note{a}, docs: []BatchDocument{batchDoc("a.md", a)}, current: []Note{a, batchNote(idD, "D")}},
		{name: "remote edit local unchanged", base: []Note{a}, docs: []BatchDocument{batchDoc("a.md", a)}, current: []Note{remoteA}},
		{name: "local edit remote unchanged", base: []Note{a}, docs: []BatchDocument{{Path: "a.md", ID: idA, Text: "A local"}}, current: []Note{a}, wantEdits: 1},
		{name: "both edit same", base: []Note{a}, docs: []BatchDocument{{Path: "a.md", ID: idA, Text: "same"}}, current: []Note{{ID: idA, Text: "same", CreatedAt: a.CreatedAt, UpdatedAt: remoteA.UpdatedAt}}},
		{name: "both edit differently", base: []Note{a}, docs: []BatchDocument{{Path: "a.md", ID: idA, Text: "A local"}}, current: []Note{remoteA}, wantConflicts: 1},
		{name: "local delete remote edit", base: []Note{a}, current: []Note{remoteA}, wantConflicts: 1},
		{name: "local edit remote delete", base: []Note{a}, docs: []BatchDocument{{Path: "a.md", ID: idA, Text: "A local"}}, wantConflicts: 1},
		{name: "both delete", base: []Note{a}},
		{name: "empty file", docs: []BatchDocument{{Path: "empty.md", Text: " \n"}}, wantErr: "empty"},
		{name: "duplicate id", base: []Note{a}, docs: []BatchDocument{batchDoc("one.md", a), batchDoc("two.md", a)}, current: []Note{a}, wantErr: "duplicate"},
		{name: "lost id", base: []Note{a}, docs: []BatchDocument{{Path: "renamed.md", Text: "A"}}, current: []Note{a}, wantErr: "lost"},
		{name: "unknown id", docs: []BatchDocument{{Path: "unknown.md", ID: idB, Text: "B"}}, wantErr: "unknown"},
		{name: "consolidate three into one", base: []Note{a, b, c}, docs: []BatchDocument{{Path: "combined.md", ID: idA, Text: "A+B+C"}}, current: []Note{a, b, c}, wantEdits: 1, wantDeletes: 2},
		{name: "split one into three", base: []Note{a}, docs: []BatchDocument{{Path: "a.md", ID: idA, Text: "part one"}, {Path: "part-two.md", Text: "part two"}, {Path: "part-three.md", Text: "part three"}}, current: []Note{a}, wantEdits: 1, wantAdds: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanBatch(tt.base, tt.docs, tt.current)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.wantErr) {
					t.Fatalf("error = %v, want text %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			adds, edits, deletes := countBatchChanges(plan)
			if adds != tt.wantAdds || edits != tt.wantEdits || deletes != tt.wantDeletes || len(plan.Conflicts) != tt.wantConflicts {
				t.Fatalf("plan = adds:%d edits:%d deletes:%d conflicts:%d", adds, edits, deletes, len(plan.Conflicts))
			}
		})
	}
}

func TestStoreApplyBatchValidatesBeforeAppend(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	a, _ := s.Add("A")
	before, err := os.ReadFile(s.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ApplyBatch([]BatchChange{
		{Kind: BatchEdit, ID: a.ID, Text: "A edited"},
		{Kind: BatchDelete, ID: NewID()},
	})
	if err == nil {
		t.Fatal("invalid batch should fail")
	}
	after, err := os.ReadFile(s.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid batch appended some events")
	}
}

func TestBatchWorkspacePersistsAndReopens(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	n, _ := s.Add("persistent note")
	var opened []string
	opener := func(path string) error { opened = append(opened, path); return nil }
	m1 := NewBatchManager(s, opener)
	session, reopened, err := m1.Start()
	if err != nil {
		t.Fatal(err)
	}
	if reopened {
		t.Fatal("first start reported reopened")
	}
	if len(opened) != 1 || opened[0] != session.Workspace {
		t.Fatalf("opener calls = %v", opened)
	}
	if filepath.Dir(session.Manifest) == session.Workspace || strings.HasPrefix(session.Manifest, session.Workspace+string(os.PathSeparator)) {
		t.Fatal("manifest must be outside the opened workspace")
	}
	if _, err := os.Stat(filepath.Join(session.Workspace, "README.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(session.Workspace, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	if err != nil || len(files) != 1 || !strings.Contains(files[0].Name(), string(n.ID)) {
		t.Fatalf("exported notes = %v, err = %v", files, err)
	}

	s2, err := Open(dir, NopBackend())
	if err != nil {
		t.Fatal(err)
	}
	m2 := NewBatchManager(s2, opener)
	recovered, reopened, err := m2.Start()
	if err != nil || !reopened || recovered.Workspace != session.Workspace {
		t.Fatalf("recovery = %+v, reopened=%v err=%v", recovered, reopened, err)
	}
	if _, err := m2.Reopen(); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 3 {
		t.Fatalf("opener called %d times, want 3", len(opened))
	}
}

func TestBatchWorkspaceFilenamesUseDisplayedTitleAndIdentity(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, NopBackend())
	first := batchNote(NewID(), "Second visible title\nbody")
	second := batchNote(NewID(), "First visible title")
	m := NewBatchManager(s, func(string) error { return nil })
	workspace := filepath.Join(dir, "named-workspace")
	if err := m.exportWorkspace(workspace, []Note{first, second}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(workspace, "notes"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"001 - [1] Second visible title -- " + string(first.ID) + ".md",
		"002 - [2] First visible title -- " + string(second.ID) + ".md",
	}
	if len(entries) != len(want) {
		t.Fatalf("workspace files = %d, want %d", len(entries), len(want))
	}
	for i, entry := range entries {
		if entry.Name() != want[i] {
			t.Errorf("file %d = %q, want %q", i, entry.Name(), want[i])
		}
	}
}

func TestBatchWorkspaceValidationRejectsUnsafeFiles(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	_, _ = s.Add("A")
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	notesDir := filepath.Join(session.Workspace, "notes")
	if err := os.Symlink(filepath.Join(notesDir, "missing.md"), filepath.Join(notesDir, "unsafe.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Diff(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unsafe") {
		t.Fatalf("unsafe entry error = %v", err)
	}
}

func TestBatchWorkspaceValidationRejectsInvalidUTF8(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session.Workspace, "notes", "bad.md"), []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Diff(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "utf-8") {
		t.Fatalf("UTF-8 error = %v", err)
	}
}

func TestBatchConsolidationCommitEndToEnd(t *testing.T) {
	be := newFakeBackend()
	dir := t.TempDir()
	s, _ := Open(dir, be)
	a, _ := s.Add("A")
	b, _ := s.Add("B")
	c, _ := s.Add("C")
	if rep := s.Sync(context.Background()); rep.Err != nil {
		t.Fatal(rep.Err)
	}
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	notesDir := filepath.Join(session.Workspace, "notes")
	entries, _ := os.ReadDir(notesDir)
	for _, entry := range entries {
		path := filepath.Join(notesDir, entry.Name())
		switch {
		case strings.Contains(entry.Name(), string(a.ID)):
			if err := os.WriteFile(path, []byte("A+B+C"), 0o644); err != nil {
				t.Fatal(err)
			}
		case strings.Contains(entry.Name(), string(b.ID)), strings.Contains(entry.Name(), string(c.ID)):
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}
	report, err := m.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 0 || report.Edited != 1 || report.Deleted != 2 || !report.LocalCommitted || report.Pending != 0 {
		t.Fatalf("commit report = %+v", report)
	}

	restarted, _ := Open(dir, be)
	notes, _ := restarted.List()
	if len(notes) != 1 || notes[0].ID != a.ID || notes[0].Text != "A+B+C" {
		t.Fatalf("restart notes = %+v", notes)
	}
	raw, ok := be.hashGet(HashKey, string(a.ID))
	if !ok {
		t.Fatal("remote is missing consolidated note")
	}
	var remote Note
	if err := json.Unmarshal([]byte(raw), &remote); err != nil {
		t.Fatal(err)
	}
	if remote.ID != a.ID || remote.Text != "A+B+C" {
		t.Fatalf("remote consolidated note = %+v", remote)
	}
	if _, ok := be.hashGet(HashKey, string(b.ID)); ok {
		t.Fatal("remote retained B")
	}
	if _, ok := be.hashGet(HashKey, string(c.ID)); ok {
		t.Fatal("remote retained C")
	}
	if _, err := m.Commit(context.Background()); err == nil {
		t.Fatal("duplicate commit should be rejected")
	}
}

func TestBatchCommitRemoteFailureKeepsLocalChangesPending(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	a, _ := s.Add("A")
	s.Sync(context.Background())
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, _ := m.Start()
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	path := filepath.Join(session.Workspace, "notes", entries[0].Name())
	if err := os.WriteFile(path, []byte("A offline edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	be.failOnce("HSET")
	report, err := m.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.LocalCommitted || report.SyncErr == nil || report.Pending != 1 {
		t.Fatalf("offline report = %+v", report)
	}
	notes, _ := s.List()
	if len(notes) != 1 || notes[0].ID != a.ID || notes[0].Text != "A offline edit" {
		t.Fatalf("local changes missing: %+v", notes)
	}
	if retry := s.Sync(context.Background()); retry.Err != nil {
		t.Fatal(retry.Err)
	}
	pending, _ := s.PendingPush()
	if pending != 0 {
		t.Fatalf("pending after retry = %d", pending)
	}
}

func TestBatchCommitConflictBlocksEntireLocalBatch(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	a, _ := s.Add("A")
	b, _ := s.Add("B")
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, _ := m.Start()
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	for _, entry := range entries {
		path := filepath.Join(session.Workspace, "notes", entry.Name())
		if strings.Contains(entry.Name(), string(a.ID)) {
			_ = os.WriteFile(path, []byte("A local"), 0o644)
		}
		if strings.Contains(entry.Name(), string(b.ID)) {
			_ = os.WriteFile(path, []byte("B local"), 0o644)
		}
	}
	if _, err := s.Edit(a.ID, "A remote"); err != nil {
		t.Fatal(err)
	}
	report, err := m.Commit(context.Background())
	if err == nil || len(report.Conflicts) != 1 || report.LocalCommitted {
		t.Fatalf("conflict report = %+v err=%v", report, err)
	}
	notes, _ := s.List()
	for _, note := range notes {
		if note.ID == b.ID && note.Text != "B" {
			t.Fatalf("non-conflicting edit was partially applied: %+v", note)
		}
	}
}

func TestBatchCommitPullsRemoteStateBeforePlanning(t *testing.T) {
	be := newFakeBackend()
	dirA, dirB := t.TempDir(), t.TempDir()
	a, _ := Open(dirA, be)
	b, _ := Open(dirB, be)
	note, _ := a.Add("base")
	a.Sync(context.Background())
	b.Sync(context.Background())

	m := NewBatchManager(b, func(string) error { return nil })
	session, _, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	path := filepath.Join(session.Workspace, "notes", entries[0].Name())
	if err := os.WriteFile(path, []byte("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Edit(note.ID, "remote edit"); err != nil {
		t.Fatal(err)
	}
	if rep := a.Sync(context.Background()); rep.Err != nil {
		t.Fatal(rep.Err)
	}

	report, err := m.Commit(context.Background())
	if err == nil || len(report.Conflicts) != 1 || report.LocalCommitted {
		t.Fatalf("commit report = %+v err=%v", report, err)
	}
	notes, _ := b.List()
	if len(notes) != 1 || notes[0].Text != "remote edit" {
		t.Fatalf("current remote state was not pulled: %+v", notes)
	}
}

func TestBatchCommitConflictsWithClockSkewedRemoteEdit(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	note, _ := s.Add("base")
	if rep := s.Sync(context.Background()); rep.Err != nil {
		t.Fatal(rep.Err)
	}
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, _ := m.Start()
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	path := filepath.Join(session.Workspace, "notes", entries[0].Name())
	_ = os.WriteFile(path, []byte("local edit"), 0o644)
	remote := Note{ID: note.ID, Text: "remote edit", CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt.Add(-time.Hour)}
	data, _ := json.Marshal(remote)
	be.putHash(HashKey, string(note.ID), string(data))

	report, err := m.Commit(context.Background())
	if err == nil || len(report.Conflicts) != 1 || report.LocalCommitted {
		t.Fatalf("report = %+v err=%v", report, err)
	}
}

func TestBatchRecoversInterruptedSessionHalfStates(t *testing.T) {
	t.Run("workspace without manifest", func(t *testing.T) {
		s, _ := newTestStore(t, NopBackend())
		_, _ = s.Add("A")
		opened := 0
		m := NewBatchManager(s, func(string) error { opened++; return nil })
		if err := m.exportWorkspace(m.workspacePath(), nil); err != nil {
			t.Fatal(err)
		}
		session, reopened, err := m.Start()
		if err != nil || reopened || opened != 1 {
			t.Fatalf("session=%+v reopened=%v opened=%d err=%v", session, reopened, opened, err)
		}
		docs, err := readBatchDocuments(session.Workspace, []Note{batchNoteFromStore(t, s)})
		if err != nil || len(docs) != 1 {
			t.Fatalf("docs=%+v err=%v", docs, err)
		}
	})

	t.Run("manifest without workspace", func(t *testing.T) {
		s, _ := newTestStore(t, NopBackend())
		_, _ = s.Add("A")
		opened := 0
		m := NewBatchManager(s, func(string) error { opened++; return nil })
		session, _, _ := m.Start()
		if err := os.RemoveAll(session.Workspace); err != nil {
			t.Fatal(err)
		}
		recovered, err := m.Reopen()
		if err != nil || opened != 2 {
			t.Fatalf("recovered=%+v opened=%d err=%v", recovered, opened, err)
		}
		if err := os.Remove(m.manifestPath()); err != nil {
			t.Fatal(err)
		}
		if err := m.Abort(); err != nil {
			t.Fatalf("abort orphan workspace: %v", err)
		}
	})
}

func batchNoteFromStore(t *testing.T, store *Store) Note {
	t.Helper()
	notes, err := store.List()
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes=%+v err=%v", notes, err)
	}
	return notes[0]
}

func TestBatchCommitReportsCleanupFailureAfterLocalSuccess(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	_, _ = s.Add("A")
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, _ := m.Start()
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	_ = os.WriteFile(filepath.Join(session.Workspace, "notes", entries[0].Name()), []byte("A edited"), 0o644)
	m.removeSessionFn = func(batchManifest) error { return errors.New("cleanup failed") }
	report, err := m.Commit(context.Background())
	if err == nil || !report.LocalCommitted || report.Edited != 1 || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	notes, _ := s.List()
	if len(notes) != 1 || notes[0].Text != "A edited" {
		t.Fatalf("notes=%+v", notes)
	}
}

func TestBatchFilenameIDMatchingUsesKnownBaseIDs(t *testing.T) {
	a, b := NewID(), NewID()
	for _, tc := range []struct {
		name string
		file string
		want ID
	}{
		{name: "preceding allowed character", file: "X" + string(a) + ".md", want: a},
		{name: "following allowed character", file: string(a) + "Y.md", want: a},
		{name: "both adjacent", file: "X" + string(a) + "Y-title.md", want: a},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := batchIDFromFilename(tc.file, []ID{a, b})
			if err != nil || got != tc.want {
				t.Fatalf("id=%s err=%v", got, err)
			}
		})
	}
	if _, err := batchIDFromFilename(string(a)+"-"+string(b)+".md", []ID{a, b}); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("multiple ID error=%v", err)
	}
}

func TestBatchRenamedFileRetainsKnownIDWithAdjacentText(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	note, _ := s.Add("A")
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, _ := m.Start()
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	oldPath := filepath.Join(session.Workspace, "notes", entries[0].Name())
	newPath := filepath.Join(session.Workspace, "notes", "X"+string(note.ID)+"Y-renamed.md")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	plan, err := m.Diff()
	if err != nil || len(plan.Changes) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestBatchCommitCarriesPendingCountError(t *testing.T) {
	be := newFakeBackend()
	s, _ := newTestStore(t, be)
	_, _ = s.Add("A")
	s.Sync(context.Background())
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, _ := m.Start()
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	_ = os.WriteFile(filepath.Join(session.Workspace, "notes", entries[0].Name()), []byte("A edited"), 0o644)
	m.pendingPushFn = func() (int, error) { return 0, errors.New("pending count failed") }
	report, err := m.Commit(context.Background())
	if err != nil || !report.LocalCommitted || report.PendingErr == nil || !strings.Contains(report.PendingErr.Error(), "pending count failed") {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestBatchCommitRefusesCrossProcessStateChangesBetweenPlanAndApply(t *testing.T) {
	tests := []struct {
		name      string
		editFiles func(t *testing.T, workspace string, notes []Note)
		mutate    func(t *testing.T, other *Store, notes []Note)
		assert    func(t *testing.T, got []Note)
	}{
		{
			name: "concurrent edit",
			editFiles: func(t *testing.T, workspace string, notes []Note) {
				writeBatchNoteForID(t, workspace, notes[0].ID, "workspace edit")
			},
			mutate: func(t *testing.T, other *Store, notes []Note) {
				if _, err := other.Edit(notes[0].ID, "other process edit"); err != nil {
					t.Fatal(err)
				}
			},
			assert: func(t *testing.T, got []Note) {
				if len(got) != 2 || got[0].Text != "other process edit" || got[1].Text != "B" {
					t.Fatalf("notes=%+v", got)
				}
			},
		},
		{
			name: "concurrent delete",
			editFiles: func(t *testing.T, workspace string, notes []Note) {
				writeBatchNoteForID(t, workspace, notes[0].ID, "workspace edit")
			},
			mutate: func(t *testing.T, other *Store, notes []Note) {
				if err := other.Delete(notes[0].ID); err != nil {
					t.Fatal(err)
				}
			},
			assert: func(t *testing.T, got []Note) {
				if len(got) != 1 || got[0].Text != "B" {
					t.Fatalf("notes=%+v", got)
				}
			},
		},
		{
			name: "concurrent add during consolidation",
			editFiles: func(t *testing.T, workspace string, notes []Note) {
				writeBatchNoteForID(t, workspace, notes[0].ID, "A+B")
				removeBatchNoteForID(t, workspace, notes[1].ID)
			},
			mutate: func(t *testing.T, other *Store, _ []Note) {
				if _, err := other.Add("other process add"); err != nil {
					t.Fatal(err)
				}
			},
			assert: func(t *testing.T, got []Note) {
				if len(got) != 3 || got[0].Text != "A" || got[1].Text != "B" || got[2].Text != "other process add" {
					t.Fatalf("notes=%+v", got)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			primary, _ := Open(dir, NopBackend())
			_, _ = primary.Add("A")
			_, _ = primary.Add("B")
			other, _ := Open(dir, NopBackend())
			notes, _ := primary.List()
			manager := NewBatchManager(primary, func(string) error { return nil })
			session, _, _ := manager.Start()
			tc.editFiles(t, session.Workspace, notes)
			manager.beforeApply = func() { tc.mutate(t, other, notes) }

			report, err := manager.Commit(context.Background())
			if err == nil || report.LocalCommitted || len(report.Conflicts) == 0 {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			got, _ := primary.List()
			tc.assert(t, got)
		})
	}
}

func writeBatchNoteForID(t *testing.T, workspace string, id ID, text string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(workspace, "notes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), string(id)) {
			if err := os.WriteFile(filepath.Join(workspace, "notes", entry.Name()), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("note file for %s not found", id)
}

func removeBatchNoteForID(t *testing.T, workspace string, id ID) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(workspace, "notes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), string(id)) {
			if err := os.Remove(filepath.Join(workspace, "notes", entry.Name())); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("note file for %s not found", id)
}

func TestBatchWorkspaceGuidanceDocumentsRetainedDecisions(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	manager := NewBatchManager(s, func(string) error { return nil })
	session, _, err := manager.Start()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(session.Workspace, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	guidance := strings.ToLower(string(data))
	for _, want := range []string{"every device", "stable id", "offline", "foreign", "delete plus add"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("workspace guidance missing %q:\n%s", want, data)
		}
	}
}

func TestBatchManifestRejectionBranches(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	m := NewBatchManager(s, func(string) error { return nil })
	validID := NewID()
	valid := batchManifest{Version: batchManifestVersion, Workspace: m.workspacePath(), Base: []Note{batchNote(validID, "A")}}
	cases := []struct {
		name     string
		manifest batchManifest
		setup    func(t *testing.T)
		want     string
	}{
		{name: "unsupported version", manifest: batchManifest{Version: 99, Workspace: m.workspacePath()}, setup: func(t *testing.T) { _ = os.MkdirAll(m.workspacePath(), 0o755) }, want: "version"},
		{name: "unsafe workspace path", manifest: batchManifest{Version: batchManifestVersion, Workspace: t.TempDir()}, setup: func(t *testing.T) {}, want: "unsafe workspace"},
		{name: "invalid base id", manifest: batchManifest{Version: batchManifestVersion, Workspace: m.workspacePath(), Base: []Note{{ID: "!!!!!!!!!!!!!!!!!!!!!!!!!!", Text: "A"}}}, setup: func(t *testing.T) { _ = os.MkdirAll(m.workspacePath(), 0o755) }, want: "invalid note"},
		{name: "duplicate base id", manifest: batchManifest{Version: batchManifestVersion, Workspace: m.workspacePath(), Base: []Note{batchNote(validID, "A"), batchNote(validID, "B")}}, setup: func(t *testing.T) { _ = os.MkdirAll(m.workspacePath(), 0o755) }, want: "duplicate"},
		{name: "unsafe workspace type", manifest: valid, setup: func(t *testing.T) { _ = os.WriteFile(m.workspacePath(), []byte("not a directory"), 0o644) }, want: "unsafe batch workspace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.RemoveAll(m.workspacePath())
			_ = os.RemoveAll(filepath.Dir(m.manifestPath()))
			tc.setup(t)
			if err := m.writeManifest(tc.manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := m.loadManifest(); err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
		})
	}
}

func TestBatchCommitPreservesRemoteOnlyAdd(t *testing.T) {
	be := newFakeBackend()
	dirA, dirB := t.TempDir(), t.TempDir()
	a, _ := Open(dirA, be)
	b, _ := Open(dirB, be)
	first, _ := a.Add("first")
	a.Sync(context.Background())
	b.Sync(context.Background())
	m := NewBatchManager(b, func(string) error { return nil })
	session, _, _ := m.Start()

	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	path := filepath.Join(session.Workspace, "notes", entries[0].Name())
	_ = os.WriteFile(path, []byte("first edited"), 0o644)
	remoteOnly, _ := a.Add("remote only")
	a.Sync(context.Background())

	report, err := m.Commit(context.Background())
	if err != nil || !report.LocalCommitted {
		t.Fatalf("report = %+v err=%v", report, err)
	}
	notes, _ := b.List()
	byID := map[ID]Note{}
	for _, note := range notes {
		byID[note.ID] = note
	}
	if len(notes) != 2 || byID[first.ID].Text != "first edited" || byID[remoteOnly.ID].Text != "remote only" {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestBatchAbortLeavesNotesUnchanged(t *testing.T) {
	s, _ := newTestStore(t, NopBackend())
	a, _ := s.Add("A")
	m := NewBatchManager(s, func(string) error { return nil })
	session, _, _ := m.Start()
	entries, _ := os.ReadDir(filepath.Join(session.Workspace, "notes"))
	_ = os.WriteFile(filepath.Join(session.Workspace, "notes", entries[0].Name()), []byte("changed"), 0o644)
	if err := m.Abort(); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.List()
	if len(notes) != 1 || notes[0].ID != a.ID || notes[0].Text != "A" {
		t.Fatalf("abort changed notes: %+v", notes)
	}
	if _, err := m.Reopen(); !errors.Is(err, ErrNoBatchSession) {
		t.Fatalf("reopen after abort error = %v", err)
	}
}
