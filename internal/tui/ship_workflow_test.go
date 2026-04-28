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
