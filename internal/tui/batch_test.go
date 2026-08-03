package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/autumnust/tack/internal/hibana"
	"github.com/autumnust/tack/internal/model"
	tea "github.com/charmbracelet/bubbletea"
)

func newBatchTestApp(t *testing.T) (AppModel, *hibana.Store, *[]string) {
	t.Helper()
	app := newTestApp()
	store, err := hibana.Open(t.TempDir(), hibana.NopBackend())
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.Add("A")
	if err != nil {
		t.Fatal(err)
	}
	app.hibanaStore = store
	app.plan.Scratch = notesToScratch([]hibana.Note{note})
	app.planView.SetData(app.plan, app.project)
	app.view = viewPlan
	app.planView.SetSection(sectionHibana)
	opened := &[]string{}
	app.batchManager = hibana.NewBatchManager(store, func(path string) error {
		*opened = append(*opened, path)
		return nil
	})
	app.batchCommitFn = app.batchManager.Commit
	return app, store, opened
}

func runBatchCommitCommand(t *testing.T, app AppModel) AppModel {
	t.Helper()
	modelAfterCommand, cmd := app.executeCommand(&CommandResult{Action: "commit"})
	app = modelAfterCommand.(AppModel)
	if cmd == nil {
		t.Fatal("commit did not schedule background work")
	}
	msg := cmd()
	if _, ok := msg.(batchCommitDoneMsg); !ok {
		t.Fatalf("commit message type = %T", msg)
	}
	modelAfterDone, _ := app.Update(msg)
	return modelAfterDone.(AppModel)
}

func TestBatchCommandRequiresHibanaPlanningSection(t *testing.T) {
	app, _, opened := newBatchTestApp(t)
	app.view = viewBoard
	app = sendCommand(t, app, "batch")
	assertStatus(t, app, "Hibana")
	if len(*opened) != 0 {
		t.Fatal("batch opened outside planning view")
	}

	app.view = viewPlan
	app.planView.SetSection(sectionToday)
	app = sendCommand(t, app, "batch")
	assertStatus(t, app, "Hibana")
	if len(*opened) != 0 {
		t.Fatal("batch opened outside Hibana section")
	}
}

func TestBatchCommandsOpenDiffCommitAndRefresh(t *testing.T) {
	app, store, opened := newBatchTestApp(t)
	app = sendCommand(t, app, "batch")
	assertStatus(t, app, "created")
	if len(*opened) != 1 {
		t.Fatalf("opener calls = %d", len(*opened))
	}
	workspace := (*opened)[0]
	entries, err := os.ReadDir(filepath.Join(workspace, "notes"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "notes", entries[0].Name())
	if err := os.WriteFile(path, []byte("A edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	app = sendCommand(t, app, "diff")
	assertStatus(t, app, "1 edit")
	notes, _ := store.List()
	if notes[0].Text != "A" {
		t.Fatal("diff changed the store")
	}
	app = sendKeys(t, app, "esc")

	app = runBatchCommitCommand(t, app)
	assertStatus(t, app, "1 edit")
	if len(app.plan.Scratch) != 1 || app.plan.Scratch[0].Text != "A edited" {
		t.Fatalf("plan scratch was not refreshed: %+v", app.plan.Scratch)
	}
	if _, err := app.batchManager.Reopen(); err == nil {
		t.Fatal("committed session remained active")
	}
}

func TestBatchWorkspaceMatchesHibanaVisibleOrderAndNumbers(t *testing.T) {
	app := newTestApp()
	store, err := hibana.Open(t.TempDir(), hibana.NopBackend())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	oldRegular, _ := store.AddWithTimestamps("Old regular", base, base)
	research, _ := store.AddWithTimestamps("[research] GPU study", base, base.Add(3*time.Hour))
	newRegular, _ := store.AddWithTimestamps("Newest regular", base, base.Add(2*time.Hour))
	notes, _ := store.List()
	app.hibanaStore = store
	app.plan.Scratch = notesToScratch(notes)
	app.planView.SetData(app.plan, app.project)
	app.view = viewPlan
	app.planView.SetSection(sectionHibana)
	opened := []string{}
	app.batchManager = hibana.NewBatchManager(store, func(path string) error {
		opened = append(opened, path)
		return nil
	})
	app.batchCommitFn = app.batchManager.Commit

	app = sendCommand(t, app, "batch")
	if len(opened) != 1 {
		t.Fatalf("opener calls = %d", len(opened))
	}
	entries, err := os.ReadDir(filepath.Join(opened[0], "notes"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"001 - [3] Newest regular -- " + string(newRegular.ID) + ".md",
		"002 - [1] Old regular -- " + string(oldRegular.ID) + ".md",
		"003 - [2] [research] GPU study -- " + string(research.ID) + ".md",
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

func TestBatchCommandsReopenAndAbort(t *testing.T) {
	app, store, opened := newBatchTestApp(t)
	app = sendCommand(t, app, "batch")
	workspace := (*opened)[0]
	entries, _ := os.ReadDir(filepath.Join(workspace, "notes"))
	_ = os.WriteFile(filepath.Join(workspace, "notes", entries[0].Name()), []byte("discard me"), 0o644)

	app = sendCommand(t, app, "reopen")
	assertStatus(t, app, "reopened")
	if len(*opened) != 2 {
		t.Fatalf("opener calls = %d", len(*opened))
	}
	app = sendCommand(t, app, "abort")
	assertStatus(t, app, "aborted")
	notes, _ := store.List()
	if len(notes) != 1 || notes[0].Text != "A" {
		t.Fatalf("abort changed notes: %+v", notes)
	}
}

func TestBatchDiffAndCommitReportConflict(t *testing.T) {
	app, store, opened := newBatchTestApp(t)
	app = sendCommand(t, app, "batch")
	entries, _ := os.ReadDir(filepath.Join((*opened)[0], "notes"))
	path := filepath.Join((*opened)[0], "notes", entries[0].Name())
	_ = os.WriteFile(path, []byte("A local"), 0o644)
	id := hibana.ID(app.plan.Scratch[0].Id)
	if _, err := store.Edit(id, "A current"); err != nil {
		t.Fatal(err)
	}

	app = sendCommand(t, app, "diff")
	assertStatus(t, app, "1 conflict")
	app = sendKeys(t, app, "esc")
	app = runBatchCommitCommand(t, app)
	if !strings.Contains(strings.ToLower(app.statusMsg), "refused") || !strings.Contains(app.statusMsg, "1 conflict") {
		t.Fatalf("commit status = %q", app.statusMsg)
	}
	notes, _ := store.List()
	if len(notes) != 1 || notes[0].Text != "A current" {
		t.Fatalf("conflicted commit changed notes: %+v", notes)
	}
}

func TestBatchDiffShowsDetailedConsolidationAndDeletions(t *testing.T) {
	app, store, opened := newBatchTestApp(t)
	b, _ := store.Add("Beta note to remove")
	c, _ := store.Add("Gamma note to remove")
	notes, _ := store.List()
	app.plan.Scratch = notesToScratch(notes)
	app.planView.SetData(app.plan, app.project)
	app.width = 100
	app.height = 30
	app = sendCommand(t, app, "batch")
	workspace := (*opened)[0]
	entries, _ := os.ReadDir(filepath.Join(workspace, "notes"))
	var bFile, cFile string
	for _, entry := range entries {
		path := filepath.Join(workspace, "notes", entry.Name())
		switch {
		case strings.Contains(entry.Name(), app.plan.Scratch[0].Id):
			_ = os.WriteFile(path, []byte("Alpha plus Beta plus Gamma"), 0o644)
		case strings.Contains(entry.Name(), string(b.ID)):
			bFile = entry.Name()
			_ = os.Remove(path)
		case strings.Contains(entry.Name(), string(c.ID)):
			cFile = entry.Name()
			_ = os.Remove(path)
		}
	}

	app = sendCommand(t, app, "diff")
	assertView(t, app, viewDetail)
	content := app.detail.viewport.View()
	for _, want := range []string{
		"EDIT", app.plan.Scratch[0].Id, "Alpha plus Beta plus Gamma",
		"DELETE", string(b.ID), "Beta note to remove", bFile,
		string(c.ID), "Gamma note to remove", cFile,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("detailed diff missing %q:\n%s", want, content)
		}
	}
	app = sendKeys(t, app, "esc")
	assertView(t, app, viewPlan)
	if app.planView.section != sectionHibana {
		t.Fatal("diff did not return to Hibana section")
	}
}

func TestBatchCommitSchedulesWorkAndBlocksDuplicate(t *testing.T) {
	app, store, opened := newBatchTestApp(t)
	app = sendCommand(t, app, "batch")
	entries, _ := os.ReadDir(filepath.Join((*opened)[0], "notes"))
	_ = os.WriteFile(filepath.Join((*opened)[0], "notes", entries[0].Name()), []byte("A edited"), 0o644)

	modelAfterCommand, cmd := app.executeCommand(&CommandResult{Action: "commit"})
	app = modelAfterCommand.(AppModel)
	if cmd == nil || !app.batchCommitBusy || !strings.Contains(app.statusMsg, "in progress") {
		t.Fatalf("busy=%v status=%q cmd=%v", app.batchCommitBusy, app.statusMsg, cmd != nil)
	}
	notes, _ := store.List()
	if notes[0].Text != "A" {
		t.Fatal("commit ran inside command handling")
	}
	duplicateModel, duplicateCmd := app.executeCommand(&CommandResult{Action: "commit"})
	app = duplicateModel.(AppModel)
	if duplicateCmd != nil || !strings.Contains(app.statusMsg, "already") {
		t.Fatalf("duplicate status=%q cmd=%v", app.statusMsg, duplicateCmd != nil)
	}
	completed, _ := app.Update(cmd())
	app = completed.(AppModel)
	if app.batchCommitBusy || app.plan.Scratch[0].Text != "A edited" {
		t.Fatalf("busy=%v scratch=%+v", app.batchCommitBusy, app.plan.Scratch)
	}
}

func TestBatchCommitCompletionReportsLocalCleanupAndPendingErrors(t *testing.T) {
	app, store, _ := newBatchTestApp(t)
	note := app.plan.Scratch[0]
	_, _ = store.Edit(hibana.ID(note.Id), "committed text")
	app.batchCommitFn = func(context.Context) (hibana.BatchCommitReport, error) {
		return hibana.BatchCommitReport{
			Edited:         1,
			LocalCommitted: true,
			RemoteEnabled:  true,
			PendingErr:     errors.New("pending count failed"),
		}, errors.New("cleanup failed")
	}
	app = runBatchCommitCommand(t, app)
	if app.batchCommitBusy || app.plan.Scratch[0].Text != "committed text" {
		t.Fatalf("busy=%v scratch=%+v", app.batchCommitBusy, app.plan.Scratch)
	}
	for _, want := range []string{"committed locally", "cleanup failed", "pending count failed"} {
		if !strings.Contains(strings.ToLower(app.statusMsg), want) {
			t.Fatalf("status %q missing %q", app.statusMsg, want)
		}
	}
}

func TestNewAppConstructsBatchManager(t *testing.T) {
	cfg := testConfig()
	cfg.Planning.Dir = t.TempDir()
	cfg.Planning.Obsidian = model.ObsidianConfig{}
	app := NewApp(cfg, "", nil, true)
	if app.hibanaStore == nil || app.batchManager == nil || app.batchCommitFn == nil {
		t.Fatalf("store=%v manager=%v commit=%v", app.hibanaStore != nil, app.batchManager != nil, app.batchCommitFn != nil)
	}
}

func TestBatchCommitBusyBlocksQuitAndAbortUntilCompletion(t *testing.T) {
	app, _, opened := newBatchTestApp(t)
	app = sendCommand(t, app, "batch")
	workspace := (*opened)[0]
	started := make(chan struct{})
	release := make(chan struct{})
	app.batchCommitFn = func(context.Context) (hibana.BatchCommitReport, error) {
		close(started)
		<-release
		return hibana.BatchCommitReport{}, errors.New("delayed failure")
	}
	modelAfterCommand, commitCmd := app.executeCommand(&CommandResult{Action: "commit"})
	app = modelAfterCommand.(AppModel)
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- commitCmd() }()
	<-started

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
	} {
		modelAfterKey, cmd := app.Update(key)
		app = modelAfterKey.(AppModel)
		if cmd != nil || !strings.Contains(app.statusMsg, "commit is active") {
			t.Fatalf("key=%q status=%q cmd=%v", key.String(), app.statusMsg, cmd != nil)
		}
	}
	for _, action := range []string{"q", "quit"} {
		quitModel, quitCmd := app.executeCommand(&CommandResult{Action: action})
		app = quitModel.(AppModel)
		if quitCmd != nil || !strings.Contains(app.statusMsg, "commit is active") {
			t.Fatalf("action=%q status=%q cmd=%v", action, app.statusMsg, quitCmd != nil)
		}
	}
	directModel, directCmd := app.initiateQuit()
	app = directModel.(AppModel)
	if directCmd != nil || !strings.Contains(app.statusMsg, "commit is active") {
		t.Fatalf("direct quit status=%q cmd=%v", app.statusMsg, directCmd != nil)
	}
	app.view = viewReview
	reviewModel, reviewCmd := app.updateReview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	app = reviewModel.(AppModel)
	if reviewCmd != nil || !strings.Contains(app.statusMsg, "commit is active") {
		t.Fatalf("review quit status=%q cmd=%v", app.statusMsg, reviewCmd != nil)
	}
	app.view = viewPlan
	app.planView.SetSection(sectionHibana)
	abortModel, abortCmd := app.executeCommand(&CommandResult{Action: "abort"})
	app = abortModel.(AppModel)
	if abortCmd != nil || !strings.Contains(app.statusMsg, "commit is active") {
		t.Fatalf("abort status=%q cmd=%v", app.statusMsg, abortCmd != nil)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("abort removed active workspace: %v", err)
	}
	duplicateModel, duplicateCmd := app.executeCommand(&CommandResult{Action: "commit"})
	app = duplicateModel.(AppModel)
	if duplicateCmd != nil || !strings.Contains(app.statusMsg, "already") {
		t.Fatalf("duplicate status=%q cmd=%v", app.statusMsg, duplicateCmd != nil)
	}

	close(release)
	completed, _ := app.Update(<-msgCh)
	app = completed.(AppModel)
	if app.batchCommitBusy {
		t.Fatal("busy state remained after completion")
	}
	abortModel, abortCmd = app.executeCommand(&CommandResult{Action: "abort"})
	app = abortModel.(AppModel)
	if abortCmd != nil || !strings.Contains(app.statusMsg, "aborted") {
		t.Fatalf("post-completion abort status=%q cmd=%v", app.statusMsg, abortCmd != nil)
	}
	quitModel, quitCmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	app = quitModel.(AppModel)
	if quitCmd != nil || !app.confirmQuit {
		t.Fatalf("normal quit did not reach confirmation: confirm=%v cmd=%v", app.confirmQuit, quitCmd != nil)
	}
}

func TestBatchDiffExcerptTruncatesUnicodeSafely(t *testing.T) {
	text := strings.Repeat("界", 120)
	content := renderBatchDiff(hibana.BatchPlan{Changes: []hibana.BatchChange{{Kind: hibana.BatchAdd, Path: "new.md", Text: text}}})
	if !utf8.ValidString(content) {
		t.Fatalf("rendered diff is invalid UTF-8: %q", content)
	}
	if !strings.Contains(content, strings.Repeat("界", 97)+"...") {
		t.Fatalf("Unicode excerpt did not end cleanly:\n%s", content)
	}
}
