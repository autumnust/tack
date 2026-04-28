package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/autumnust/tack/internal/model"
	"github.com/autumnust/tack/internal/ship"
	tea "github.com/charmbracelet/bubbletea"
)

// --- Fakes shared by ship workflow tests ---

type fakeGH struct {
	calls []fakeGHCall
	resp  map[string]fakeGHResp
}

type fakeGHCall struct {
	args  []string
	stdin []byte
}

type fakeGHResp struct {
	out []byte
	err error
}

func (f *fakeGH) Run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, fakeGHCall{args: args})
	return f.match(args)
}

func (f *fakeGH) RunStdin(stdin []byte, args ...string) ([]byte, error) {
	f.calls = append(f.calls, fakeGHCall{args: args, stdin: stdin})
	return f.match(args)
}

func (f *fakeGH) match(args []string) ([]byte, error) {
	joined := strings.Join(args, " ")
	bestLen := -1
	var best fakeGHResp
	matched := false
	for prefix, r := range f.resp {
		if strings.HasPrefix(joined, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			best = r
			matched = true
		}
	}
	if matched {
		return best.out, best.err
	}
	return nil, nil
}

type fakeSSH struct {
	calls []fakeSSHCall
	err   error
}

type fakeSSHCall struct {
	host string
	cmd  string
}

func (f *fakeSSH) Run(host, command string) ([]byte, error) {
	f.calls = append(f.calls, fakeSSHCall{host: host, cmd: command})
	return nil, f.err
}

// --- :promote tests ---

func TestPromote_HibanaToToday_OnSave(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")

	// Add a hibana note
	app.plan.Scratch = []model.ScratchNote{
		{Id: "n1", Text: "raw thought"},
	}
	app.planView.SetData(app.plan, app.project)
	app.planView.SetSection(sectionHibana)

	// Run :promote 1 — should open editor (we won't assert the cmd, just
	// the post-editor behavior via simulated editorFinishedMsg).
	app = sendCommand(t, app, "promote 1")

	// Simulate the user saving "polished thought" in vim
	tmpFile := t.TempDir() + "/p.md"
	if err := os.WriteFile(tmpFile, []byte("polished thought"), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ := app.Update(editorFinishedMsg{
		tmpPath: tmpFile,
		purpose: editorPurposePromote,
		idx:     0,
	})
	app = m.(AppModel)

	if len(app.plan.Today) != 1 {
		t.Fatalf("expected 1 today item, got %d", len(app.plan.Today))
	}
	if app.plan.Today[0].Text != "polished thought" {
		t.Errorf("today text = %q", app.plan.Today[0].Text)
	}
	if app.plan.Today[0].IssueNum != 0 {
		t.Errorf("today IssueNum = %d, want 0 for promote", app.plan.Today[0].IssueNum)
	}
	if len(app.plan.Scratch) != 0 {
		t.Errorf("expected hibana row deleted, still have %d", len(app.plan.Scratch))
	}
	assertStatus(t, app, "Promoted to today")
}

func TestPromote_AbortLeavesNoteInPlace(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app.plan.Scratch = []model.ScratchNote{
		{Id: "n1", Text: "untouched"},
	}
	app.planView.SetData(app.plan, app.project)
	app.planView.SetSection(sectionHibana)

	// Simulate editor failing (user :cq'd or wrote nothing) — our flow
	// uses an empty file to mean "abort".
	tmpFile := t.TempDir() + "/empty.md"
	_ = os.WriteFile(tmpFile, []byte(""), 0644)
	m, _ := app.Update(editorFinishedMsg{
		tmpPath: tmpFile,
		purpose: editorPurposePromote,
		idx:     0,
	})
	app = m.(AppModel)

	if len(app.plan.Today) != 0 {
		t.Errorf("expected no today items, got %d", len(app.plan.Today))
	}
	if len(app.plan.Scratch) != 1 {
		t.Errorf("expected hibana row preserved, got %d", len(app.plan.Scratch))
	}
	assertStatus(t, app, "Promote canceled")
}

// --- :ship tests ---

func TestShip_HappyPath_FromTodayRow(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40

	// Stub config + ship runners
	app.config.Project = "https://github.com/orgs/test-org/projects/1"

	gh := &fakeGH{resp: map[string]fakeGHResp{
		"issue create":     {out: []byte("https://github.com/test-org/repo/issues/28151\n")},
		"issue view":       {out: []byte(`{"number":28151,"title":"T","url":"https://github.com/test-org/repo/issues/28151","id":"I_NEW"}`)},
		"project item-add": {out: []byte("ok")},
	}}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "make redis fast"`)

	// User runs :ship 1 — opens editor with template (we skip the
	// editor-launch assertion and synthesize the final message).
	tmpFile := t.TempDir() + "/ship.md"
	tpl := `# Title
Make redis fast

# Body
We need debounce.

# Repo
test-org/repo

# Issue
new

# Epic


# Host
local
`
	if err := os.WriteFile(tmpFile, []byte(tpl), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ := app.Update(editorFinishedMsg{
		tmpPath: tmpFile,
		purpose: editorPurposeShip,
		idx:     0,
	})
	app = m.(AppModel)

	if len(app.plan.Today) != 1 {
		t.Fatalf("today rows changed unexpectedly: %d", len(app.plan.Today))
	}
	if app.plan.Today[0].IssueNum != 28151 {
		t.Errorf("today IssueNum = %d, want 28151", app.plan.Today[0].IssueNum)
	}
	if !strings.Contains(app.statusMsg, "Shipped #28151") {
		t.Errorf("status = %q, want 'Shipped #28151...'", app.statusMsg)
	}
	if !strings.Contains(app.statusMsg, "local:28151") {
		t.Errorf("status missing local:28151: %q", app.statusMsg)
	}
}

func TestShip_AbortNoOp(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "task"`)

	gh := &fakeGH{}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	tmpFile := t.TempDir() + "/empty.md"
	_ = os.WriteFile(tmpFile, []byte(""), 0644)
	m, _ := app.Update(editorFinishedMsg{
		tmpPath: tmpFile,
		purpose: editorPurposeShip,
		idx:     0,
	})
	app = m.(AppModel)

	if app.plan.Today[0].IssueNum != 0 {
		t.Errorf("IssueNum should remain 0 on abort, got %d", app.plan.Today[0].IssueNum)
	}
	if len(gh.calls) > 0 {
		t.Errorf("must not call gh on abort, got %d calls", len(gh.calls))
	}
	assertStatus(t, app, "Ship canceled")
}

// TestShip_AbortOnUnsavedQuit: simulates `:q!` from vim — the editor
// exits without writing, so the file's content is identical to the
// template tack pre-filled. Nothing user-driven was decided, so nothing
// should happen: no gh, no ssh, no IssueNum stamp.
func TestShip_AbortOnUnsavedQuit(t *testing.T) {
	app := newTestApp()
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "task"`)

	gh := &fakeGH{}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	// The exact template tack would have written. The file ends up with
	// this same content because vim never saved.
	tpl := `# Title


# Body
task

# Repo
kumo-ai/kumo

# Issue
new

# Epic


# Host
local
`
	tmpFile := t.TempDir() + "/ship.md"
	if err := os.WriteFile(tmpFile, []byte(tpl), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ := app.Update(editorFinishedMsg{
		tmpPath:         tmpFile,
		purpose:         editorPurposeShip,
		idx:             0,
		originalContent: tpl,
	})
	app = m.(AppModel)

	if app.plan.Today[0].IssueNum != 0 {
		t.Errorf("IssueNum should remain 0 on :q!, got %d", app.plan.Today[0].IssueNum)
	}
	if len(gh.calls) > 0 {
		t.Errorf(":q! must not call gh, got %d calls", len(gh.calls))
	}
	if len(ssh.calls) > 0 {
		t.Errorf(":q! must not call ssh, got %d calls", len(ssh.calls))
	}
	assertStatus(t, app, "Ship canceled")
}

// TestShip_FromBoard_CreatesSessionWithSlug: in board view, with the
// cursor on a project item, `:ship <slug>` creates a tmux session named
// `<issue-num>-<slug>` for that item. No editor, no template, no
// `gh issue create` (the issue already exists).
func TestShip_FromBoard_CreatesSessionWithSlug(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40

	gh := &fakeGH{}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	// Build a minimal board with one issue under one person.
	app.persons = []model.PersonGroup{
		{
			Login: "alice", DisplayName: "Alice",
			Groups: []model.IssueGroup{
				{
					Issues: []model.ProjectItem{
						{Number: 28151, Title: "Diskgraph", Repo: "kumo-ai/kumo"},
					},
				},
			},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard

	// Move cursor onto the issue (epic header is at idx 0 if present;
	// no epic here, so issue is at 0).
	// Ensure cursor is on the issue, not a header.
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	app = sendCommand(t, app, "ship multicat")

	// Should have made one ssh call: ts new on the issue#-slug name.
	if len(ssh.calls) != 1 {
		t.Fatalf("expected 1 ssh call, got %d: %+v", len(ssh.calls), ssh.calls)
	}
	if !strings.Contains(ssh.calls[0].cmd, "ts new") {
		t.Errorf("ssh cmd should run ts new, got %q", ssh.calls[0].cmd)
	}
	if !strings.Contains(ssh.calls[0].cmd, "28151-multicat") {
		t.Errorf("session name should be 28151-multicat, got cmd %q", ssh.calls[0].cmd)
	}
	if len(gh.calls) > 0 {
		t.Errorf("board ship should not call gh (issue already exists), got %d calls", len(gh.calls))
	}
	if !strings.Contains(app.statusMsg, "Shipped #28151") {
		t.Errorf("status = %q, want 'Shipped #28151...'", app.statusMsg)
	}
	if !strings.Contains(app.statusMsg, "28151-multicat") {
		t.Errorf("status missing 28151-multicat: %q", app.statusMsg)
	}
}

// TestShip_FromBoard_RequiresSlug: `:ship` with no arg in board view
// surfaces a usage hint instead of a silent default.
func TestShip_FromBoard_RequiresSlug(t *testing.T) {
	app := newTestApp()
	app.width = 80
	app.height = 40
	gh := &fakeGH{}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	app.persons = []model.PersonGroup{
		{
			Login: "alice", DisplayName: "Alice",
			Groups: []model.IssueGroup{{Issues: []model.ProjectItem{{Number: 28151, Title: "Diskgraph", Repo: "kumo-ai/kumo"}}}},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	app = sendCommand(t, app, "ship")

	if len(ssh.calls) != 0 {
		t.Errorf("expected no ssh calls without slug, got %d", len(ssh.calls))
	}
	if !strings.Contains(app.statusMsg, "slug") && !strings.Contains(app.statusMsg, "Usage") {
		t.Errorf("status should hint at the slug arg, got %q", app.statusMsg)
	}
}

func TestShip_PartialFailure_StampIssueAnyway(t *testing.T) {
	app := newTestApp()
	app.config.Project = "https://github.com/orgs/test-org/projects/1"
	app = sendCommand(t, app, "plan")
	app = sendCommand(t, app, `today "task"`)

	gh := &fakeGH{resp: map[string]fakeGHResp{
		"issue create":     {out: []byte("https://github.com/test-org/repo/issues/9999\n")},
		"issue view":       {out: []byte(`{"number":9999,"title":"T","url":"https://github.com/test-org/repo/issues/9999","id":"I_X"}`)},
		"project item-add": {out: []byte("ok")},
	}}
	ssh := &fakeSSH{err: errors.New("ssh down")}
	app.shipGH = gh
	app.shipSSH = ssh

	tmpFile := t.TempDir() + "/ship.md"
	tpl := `# Title
T

# Body
B

# Repo
test-org/repo

# Issue
new

# Epic


# Host
aws-bench
`
	_ = os.WriteFile(tmpFile, []byte(tpl), 0644)
	m, _ := app.Update(editorFinishedMsg{
		tmpPath: tmpFile,
		purpose: editorPurposeShip,
		idx:     0,
	})
	app = m.(AppModel)

	// Issue created → todo stamped, even though session create failed
	if app.plan.Today[0].IssueNum != 9999 {
		t.Errorf("IssueNum = %d, want 9999 (so re-run finds it)", app.plan.Today[0].IssueNum)
	}
	if !strings.Contains(strings.ToLower(app.statusMsg), "ship") {
		t.Errorf("status should describe partial state: %q", app.statusMsg)
	}
}

// --- Wiring sanity check: Orchestrate result formatter is the source of
// the status string (so the same code path the integration tests exercise
// is what the user sees).

func TestFormatStatus_IncludesAttachHint(t *testing.T) {
	res := ship.OrchestrateResult{
		Issue: ship.IssueRef{Number: 28151, Repo: "kumo-ai/kumo"},
		Session: ship.SessionResult{
			SessionName:  "28151",
			TicketRef:    "kumo-ai/kumo#28151",
			NotesPathTBD: true,
		},
	}
	got := ship.FormatStatus(res, "aws")
	for _, want := range []string{"Shipped #28151", "aws:28151", "Attach with"} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatStatus missing %q: %q", want, got)
		}
	}
}

// Avoid unused import warnings when tests are stripped down.
var _ = tea.KeyMsg{}
