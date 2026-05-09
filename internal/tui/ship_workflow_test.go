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

// --- :ship tests (hibana → board) ---

func TestShip_HibanaToBoard_OnSave(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"
	app.width = 80
	app.height = 40
	app = sendCommand(t, app, "plan")

	app.plan.Scratch = []model.ScratchNote{
		{Id: "n1", Text: "raw thought"},
	}
	app.planView.SetData(app.plan, app.project)
	app.planView.SetSection(sectionHibana)

	app = sendCommand(t, app, "ship 1")

	tmpFile := t.TempDir() + "/p.md"
	if err := os.WriteFile(tmpFile, []byte("polished thought"), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ := app.Update(editorFinishedMsg{
		tmpPath: tmpFile,
		purpose: editorPurposeShip,
		idx:     0,
	})
	app = m.(AppModel)

	if len(app.plan.UpstashTasks) != 1 {
		t.Fatalf("expected 1 upstash task, got %d", len(app.plan.UpstashTasks))
	}
	if app.plan.UpstashTasks[0].Text != "polished thought" {
		t.Errorf("upstash text = %q", app.plan.UpstashTasks[0].Text)
	}
	if app.plan.UpstashTasks[0].Id != "n1" {
		t.Errorf("upstash id = %q, want preserved hibana id", app.plan.UpstashTasks[0].Id)
	}
	if len(app.plan.Scratch) != 0 {
		t.Errorf("expected hibana row deleted, still have %d", len(app.plan.Scratch))
	}
	assertStatus(t, app, "Shipped to board")
	if app.view != viewBoard {
		t.Errorf("expected to land on board view, got %v", app.view)
	}
}

func TestShip_RequiresMeConfig(t *testing.T) {
	app := newTestApp()
	// no Me set
	app = sendCommand(t, app, "plan")
	app.plan.Scratch = []model.ScratchNote{{Id: "n1", Text: "x"}}
	app.planView.SetData(app.plan, app.project)
	app.planView.SetSection(sectionHibana)

	app = sendCommand(t, app, "ship 1")
	if len(app.plan.UpstashTasks) != 0 {
		t.Errorf(":ship without Me should be a no-op, got %d tasks", len(app.plan.UpstashTasks))
	}
	if !strings.Contains(app.statusMsg, "me:") && !strings.Contains(app.statusMsg, "Me") {
		t.Errorf("status should hint at config: %q", app.statusMsg)
	}
}

func TestShip_AbortLeavesNoteInPlace(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"
	app = sendCommand(t, app, "plan")
	app.plan.Scratch = []model.ScratchNote{
		{Id: "n1", Text: "untouched"},
	}
	app.planView.SetData(app.plan, app.project)
	app.planView.SetSection(sectionHibana)

	tmpFile := t.TempDir() + "/empty.md"
	_ = os.WriteFile(tmpFile, []byte(""), 0644)
	m, _ := app.Update(editorFinishedMsg{
		tmpPath: tmpFile,
		purpose: editorPurposeShip,
		idx:     0,
	})
	app = m.(AppModel)

	if len(app.plan.UpstashTasks) != 0 {
		t.Errorf("expected no upstash tasks, got %d", len(app.plan.UpstashTasks))
	}
	if len(app.plan.Scratch) != 1 {
		t.Errorf("expected hibana row preserved, got %d", len(app.plan.Scratch))
	}
	assertStatus(t, app, "Ship canceled")
}

// --- :github tests (upstash board row → real GH issue) ---

func TestGithub_HappyPath_FromUpstashRow(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"
	app.config.Project = "https://github.com/orgs/test-org/projects/1"
	app.width = 80
	app.height = 40

	gh := &fakeGH{resp: map[string]fakeGHResp{
		"issue create":     {out: []byte("https://github.com/test-org/repo/issues/28151\n")},
		"issue view":       {out: []byte(`{"number":28151,"title":"T","url":"https://github.com/test-org/repo/issues/28151","id":"I_NEW"}`)},
		"project item-add": {out: []byte("ok")},
	}}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	// Seed an upstash task and rebuild board with it under alice.
	app.plan.UpstashTasks = []model.UpstashTask{
		{Id: "ups1", Text: "raw thought"},
	}
	app.persons = []model.PersonGroup{
		{
			Login: "alice", DisplayName: "Alice",
			Groups: []model.IssueGroup{
				{Issues: app.upstashItems()},
			},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	tmpFile := t.TempDir() + "/gh.md"
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
		tmpPath:   tmpFile,
		purpose:   editorPurposeGithub,
		upstashID: "ups1",
	})
	app = m.(AppModel)

	if len(app.plan.UpstashTasks) != 0 {
		t.Errorf("upstash task should be removed after :github, got %d", len(app.plan.UpstashTasks))
	}
	if !strings.Contains(app.statusMsg, "Shipped #28151") {
		t.Errorf("status = %q, want 'Shipped #28151...'", app.statusMsg)
	}
	if !strings.Contains(app.statusMsg, "local:28151") {
		t.Errorf("status missing local:28151: %q", app.statusMsg)
	}
}

func TestGithub_AbortNoOp(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"

	gh := &fakeGH{}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	app.plan.UpstashTasks = []model.UpstashTask{{Id: "ups1", Text: "x"}}

	tmpFile := t.TempDir() + "/empty.md"
	_ = os.WriteFile(tmpFile, []byte(""), 0644)
	m, _ := app.Update(editorFinishedMsg{
		tmpPath:   tmpFile,
		purpose:   editorPurposeGithub,
		upstashID: "ups1",
	})
	app = m.(AppModel)

	if len(app.plan.UpstashTasks) != 1 {
		t.Errorf("upstash task should remain on abort, got %d", len(app.plan.UpstashTasks))
	}
	if len(gh.calls) > 0 {
		t.Errorf("must not call gh on abort, got %d calls", len(gh.calls))
	}
	assertStatus(t, app, "Github canceled")
}

func TestGithub_AbortOnUnsavedQuit(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"

	gh := &fakeGH{}
	ssh := &fakeSSH{}
	app.shipGH = gh
	app.shipSSH = ssh

	app.plan.UpstashTasks = []model.UpstashTask{{Id: "ups1", Text: "x"}}

	tpl := `# Title


# Body
x

# Repo
kumo-ai/kumo

# Issue
new

# Epic


# Host
local
`
	tmpFile := t.TempDir() + "/gh.md"
	if err := os.WriteFile(tmpFile, []byte(tpl), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ := app.Update(editorFinishedMsg{
		tmpPath:         tmpFile,
		purpose:         editorPurposeGithub,
		upstashID:       "ups1",
		originalContent: tpl,
	})
	app = m.(AppModel)

	if len(app.plan.UpstashTasks) != 1 {
		t.Errorf("upstash task should remain on :q!, got %d", len(app.plan.UpstashTasks))
	}
	if len(gh.calls) > 0 {
		t.Errorf(":q! must not call gh, got %d calls", len(gh.calls))
	}
	if len(ssh.calls) > 0 {
		t.Errorf(":q! must not call ssh, got %d calls", len(ssh.calls))
	}
	assertStatus(t, app, "Github canceled")
}

// --- :edit + :del tests (upstash board rows) ---

func TestEdit_UpstashRow_UpdatesText(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"
	app.width = 80
	app.height = 40

	app.plan.UpstashTasks = []model.UpstashTask{{Id: "ups1", Text: "raw"}}
	app.persons = []model.PersonGroup{
		{
			Login: "alice", DisplayName: "Alice",
			Groups: []model.IssueGroup{{Issues: app.upstashItems()}},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	tmpFile := t.TempDir() + "/edit.md"
	if err := os.WriteFile(tmpFile, []byte("polished"), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ := app.Update(editorFinishedMsg{
		tmpPath:         tmpFile,
		purpose:         editorPurposeEditUpstash,
		upstashID:       "ups1",
		originalContent: "raw",
	})
	app = m.(AppModel)

	if len(app.plan.UpstashTasks) != 1 {
		t.Fatalf("expected 1 upstash task, got %d", len(app.plan.UpstashTasks))
	}
	if app.plan.UpstashTasks[0].Text != "polished" {
		t.Errorf("upstash text not updated: %q", app.plan.UpstashTasks[0].Text)
	}
	assertStatus(t, app, "Note updated")
}

func TestEdit_UpstashRow_AbortLeavesUnchanged(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"

	app.plan.UpstashTasks = []model.UpstashTask{{Id: "ups1", Text: "original"}}

	tmpFile := t.TempDir() + "/edit.md"
	_ = os.WriteFile(tmpFile, []byte(""), 0644)
	m, _ := app.Update(editorFinishedMsg{
		tmpPath:         tmpFile,
		purpose:         editorPurposeEditUpstash,
		upstashID:       "ups1",
		originalContent: "original",
	})
	app = m.(AppModel)

	if app.plan.UpstashTasks[0].Text != "original" {
		t.Errorf("text changed on abort: %q", app.plan.UpstashTasks[0].Text)
	}
	assertStatus(t, app, "Edit canceled")
}

func TestEdit_GHRow_RejectedWithHint(t *testing.T) {
	app := newTestApp()
	app.persons = []model.PersonGroup{
		{
			Login: "alice", DisplayName: "Alice",
			Groups: []model.IssueGroup{{Issues: []model.ProjectItem{
				{Number: 100, Title: "real issue", Repo: "test/repo"},
			}}},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	app = sendCommand(t, app, "edit")
	if !strings.Contains(app.statusMsg, "GitHub") && !strings.Contains(app.statusMsg, "browser") {
		t.Errorf("status should hint at GH/browser flow: %q", app.statusMsg)
	}
}

func TestDel_UpstashRow_Removes(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"

	app.plan.UpstashTasks = []model.UpstashTask{
		{Id: "ups1", Text: "one"},
		{Id: "ups2", Text: "two"},
	}
	app.persons = []model.PersonGroup{
		{
			Login: "alice", DisplayName: "Alice",
			Groups: []model.IssueGroup{{Issues: app.upstashItems()}},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	app = sendCommand(t, app, "del")

	if len(app.plan.UpstashTasks) != 1 {
		t.Fatalf("expected 1 task remaining, got %d", len(app.plan.UpstashTasks))
	}
	if app.plan.UpstashTasks[0].Id != "ups2" {
		t.Errorf("wrong task survived: %+v", app.plan.UpstashTasks)
	}
	if !strings.Contains(app.statusMsg, "Removed") {
		t.Errorf("status = %q", app.statusMsg)
	}
}

func TestDel_GHRow_RejectedWithHint(t *testing.T) {
	app := newTestApp()
	app.persons = []model.PersonGroup{
		{
			Login: "alice", DisplayName: "Alice",
			Groups: []model.IssueGroup{{Issues: []model.ProjectItem{
				{Number: 100, Title: "real issue", Repo: "test/repo"},
			}}},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	app = sendCommand(t, app, "del")
	if !strings.Contains(app.statusMsg, "GitHub") {
		t.Errorf("status should hint that GH issues aren't deletable from tack: %q", app.statusMsg)
	}
}

// --- :start tests (board → tmux session) ---

func TestStart_FromBoard_CreatesSessionWithSlug(t *testing.T) {
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

	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	app = sendCommand(t, app, "start multicat")

	if len(ssh.calls) != 1 {
		t.Fatalf("expected 1 ssh call, got %d: %+v", len(ssh.calls), ssh.calls)
	}
	if ssh.calls[0].host != "aws" {
		t.Errorf("default host should be aws, got %q", ssh.calls[0].host)
	}
	if !strings.Contains(ssh.calls[0].cmd, "ts new") {
		t.Errorf("ssh cmd should run ts new, got %q", ssh.calls[0].cmd)
	}
	if !strings.Contains(ssh.calls[0].cmd, "28151-multicat") {
		t.Errorf("session name should be 28151-multicat, got cmd %q", ssh.calls[0].cmd)
	}
	if len(gh.calls) > 0 {
		t.Errorf(":start should not call gh, got %d calls", len(gh.calls))
	}
	if !strings.Contains(app.statusMsg, "Shipped #28151") {
		t.Errorf("status = %q, want 'Shipped #28151...'", app.statusMsg)
	}
	if !strings.Contains(app.statusMsg, "aws:28151-multicat") {
		t.Errorf("status missing aws:28151-multicat: %q", app.statusMsg)
	}
}

func TestStart_FromBoard_RequiresSlug(t *testing.T) {
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

	app = sendCommand(t, app, "start")

	if len(ssh.calls) != 0 {
		t.Errorf("expected no ssh calls without slug, got %d", len(ssh.calls))
	}
	if !strings.Contains(app.statusMsg, "slug") && !strings.Contains(app.statusMsg, "Usage") {
		t.Errorf("status should hint at the slug arg, got %q", app.statusMsg)
	}
}

// :start on an upstash row creates a session named just <slug> (no
// issue prefix), with no ticket linkage and no repo path — the row
// isn't anchored to a GitHub issue or repo.
func TestStart_OnUpstashRow_UsesSlugOnly(t *testing.T) {
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
			Groups: []model.IssueGroup{{Issues: []model.ProjectItem{
				{ID: "ups1", Title: "raw thought", Source: model.SourceUpstash},
			}}},
		},
	}
	app.board = NewBoardModel(app.persons)
	app.view = viewBoard
	for app.board.SelectedIssue() == nil {
		app.board.CursorDown()
	}

	app = sendCommand(t, app, "start sketch")

	if len(ssh.calls) != 1 {
		t.Fatalf("expected 1 ssh call, got %d", len(ssh.calls))
	}
	if !strings.Contains(ssh.calls[0].cmd, "ts new") || !strings.Contains(ssh.calls[0].cmd, "sketch") {
		t.Errorf("session command should run ts new with slug, got cmd %q", ssh.calls[0].cmd)
	}
	// Make sure the session name isn't '0-sketch' (i.e. no issue-number prefix).
	if strings.Contains(ssh.calls[0].cmd, "0-sketch") {
		t.Errorf("upstash session must not get a #0 issue-number prefix, got %q", ssh.calls[0].cmd)
	}
	if strings.Contains(ssh.calls[0].cmd, "cd ") {
		t.Errorf("upstash session should not cd into a repo, got %q", ssh.calls[0].cmd)
	}
	if len(gh.calls) > 0 {
		t.Errorf(":start should not call gh, got %d calls", len(gh.calls))
	}
	if !strings.Contains(app.statusMsg, "aws:sketch") {
		t.Errorf("status should mention aws:sketch, got %q", app.statusMsg)
	}
}

func TestGithub_PartialFailure(t *testing.T) {
	app := newTestApp()
	app.config.Me = "alice"
	app.config.Project = "https://github.com/orgs/test-org/projects/1"
	app.plan.UpstashTasks = []model.UpstashTask{{Id: "ups1", Text: "x"}}
	app.view = viewBoard

	gh := &fakeGH{resp: map[string]fakeGHResp{
		"issue create":     {out: []byte("https://github.com/test-org/repo/issues/9999\n")},
		"issue view":       {out: []byte(`{"number":9999,"title":"T","url":"https://github.com/test-org/repo/issues/9999","id":"I_X"}`)},
		"project item-add": {out: []byte("ok")},
	}}
	ssh := &fakeSSH{err: errors.New("ssh down")}
	app.shipGH = gh
	app.shipSSH = ssh

	tmpFile := t.TempDir() + "/gh.md"
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
		tmpPath:   tmpFile,
		purpose:   editorPurposeGithub,
		upstashID: "ups1",
	})
	app = m.(AppModel)

	if len(app.plan.UpstashTasks) != 0 {
		t.Errorf("upstash task should be removed even on partial failure, got %d", len(app.plan.UpstashTasks))
	}
	if !strings.Contains(strings.ToLower(app.statusMsg), "github") {
		t.Errorf("status should describe partial state: %q", app.statusMsg)
	}
}

// --- Wiring sanity check: Orchestrate result formatter is the source of
// the status string.

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

var _ = tea.KeyMsg{}
