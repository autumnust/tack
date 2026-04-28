package ship

import (
	"errors"
	"strings"
	"testing"

	"github.com/autumnust/tack/internal/model"
)

func newHappyGH() *fakeGH {
	return &fakeGH{resp: map[string]ghResp{
		"issue create":     {out: []byte("https://github.com/kumo-ai/kumo/issues/28151\n")},
		"issue view":       {out: []byte(`{"number":28151,"title":"T","url":"https://github.com/kumo-ai/kumo/issues/28151","id":"I_NEW"}`)},
		"project item-add": {out: []byte("ok\n")},
		"api graphql":      {out: []byte(`{"data":{"node":{"parent":null}}}`)},
	}}
}

func TestOrchestrate_HappyNewIssueNoEpic(t *testing.T) {
	gh := newHappyGH()
	ssh := &fakeSSH{}

	form := ShipForm{
		Title:    "Make redis fast",
		Body:     "We need debounce.",
		Repo:     "kumo-ai/kumo",
		IssueNew: true,
		Host:     "local",
	}
	todo := &model.TodoItem{Text: "redis"}

	res, err := Orchestrate(gh, ssh, form, OrchestrateConfig{
		ProjectURL: "https://github.com/orgs/kumo-ai/projects/9",
		RepoPaths:  map[string]string{"kumo-ai/kumo": "/work/kumo"},
	}, todo)
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if res.Issue.Number != 28151 {
		t.Errorf("Issue.Number = %d", res.Issue.Number)
	}
	if todo.IssueNum != 28151 {
		t.Errorf("todo.IssueNum = %d, want 28151", todo.IssueNum)
	}
	if res.Session.SessionName != "28151" {
		t.Errorf("Session.SessionName = %q", res.Session.SessionName)
	}

	// Verify the ordered call sequence: create, project-add, ts new
	calls := ghJoin(gh)
	wantPrefix := []string{"issue create", "issue view", "project item-add"}
	for _, w := range wantPrefix {
		if !strings.Contains(calls, w) {
			t.Errorf("expected gh call with %q in sequence: %s", w, calls)
		}
	}
	if len(ssh.calls) == 0 {
		t.Error("expected ssh call for ts new")
	}
}

func TestOrchestrate_HappyNewIssueWithEpic_FiresLink(t *testing.T) {
	gh := newHappyGH()
	// Epic parent lookup — note `issue view 100` (epic) needs to resolve
	// to number=100. We override the fake's matcher to dispatch by argv
	// for the epic-specific call.
	gh.resp["issue view 100"] = ghResp{out: []byte(`{"number":100,"title":"Epic","url":"https://github.com/kumo-ai/kumo/issues/100","id":"I_PARENT"}`)}
	gh.resp["api graphql"] = ghResp{out: []byte(`{"data":{"node":{"parent":null}}}`)}
	ssh := &fakeSSH{}

	form := ShipForm{
		Title:    "T",
		Body:     "B",
		Repo:     "kumo-ai/kumo",
		IssueNew: true,
		EpicNum:  100,
		Host:     "local",
	}
	todo := &model.TodoItem{Text: "x"}
	_, err := Orchestrate(gh, ssh, form, OrchestrateConfig{
		ProjectURL: "https://github.com/orgs/kumo-ai/projects/9",
		RepoPaths:  map[string]string{"kumo-ai/kumo": "/work/kumo"},
	}, todo)
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	// Should have at least: parent fetch + addSubIssue mutation
	mutationFound := false
	for _, c := range gh.calls {
		j := strings.Join(c.args, " ")
		if strings.Contains(j, "addSubIssue") {
			mutationFound = true
		}
	}
	if !mutationFound {
		t.Error("expected addSubIssue mutation when Epic is set")
	}
}

func TestOrchestrate_AttachExisting_SkipsCreate(t *testing.T) {
	gh := &fakeGH{resp: map[string]ghResp{
		"issue view":       {out: []byte(`{"number":42,"title":"T","url":"https://github.com/kumo-ai/kumo/issues/42","id":"I_EXIST"}`)},
		"project item-add": {out: []byte("ok\n")},
	}}
	ssh := &fakeSSH{}
	form := ShipForm{
		Title:    "",
		Body:     "",
		Repo:     "kumo-ai/kumo",
		IssueNew: false,
		IssueNum: 42,
		Host:     "local",
	}
	todo := &model.TodoItem{Text: "x"}
	res, err := Orchestrate(gh, ssh, form, OrchestrateConfig{
		ProjectURL: "https://github.com/orgs/kumo-ai/projects/9",
		RepoPaths:  map[string]string{"kumo-ai/kumo": "/work/kumo"},
	}, todo)
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if res.Issue.Number != 42 {
		t.Errorf("Issue.Number = %d", res.Issue.Number)
	}
	if todo.IssueNum != 42 {
		t.Errorf("todo.IssueNum = %d", todo.IssueNum)
	}
	for _, c := range gh.calls {
		if strings.HasPrefix(strings.Join(c.args, " "), "issue create") {
			t.Error("must not call gh issue create when attaching")
		}
	}
}

func TestOrchestrate_PartialFailure_SessionCreate(t *testing.T) {
	// Issue created, project add ok, but ts new fails. The todo should
	// still be marked with IssueNum so a re-run finds the existing issue.
	gh := newHappyGH()
	ssh := &fakeSSH{err: errors.New("aws-bench unreachable")}

	form := ShipForm{
		Title:    "T",
		Body:     "B",
		Repo:     "kumo-ai/kumo",
		IssueNew: true,
		Host:     "aws-bench",
	}
	todo := &model.TodoItem{Text: "x"}
	res, err := Orchestrate(gh, ssh, form, OrchestrateConfig{
		ProjectURL: "https://github.com/orgs/kumo-ai/projects/9",
		RepoPaths:  map[string]string{"kumo-ai/kumo": "/work/kumo"},
	}, todo)
	if err == nil {
		t.Fatal("expected partial-failure error")
	}
	// Issue must still be created
	if res.Issue.Number != 28151 {
		t.Errorf("Issue.Number = %d", res.Issue.Number)
	}
	// Todo should be marked so a re-run sees the existing issue
	if todo.IssueNum != 28151 {
		t.Errorf("todo.IssueNum = %d, want 28151 (so re-run sees it)", todo.IssueNum)
	}
}

func TestOrchestrate_RepoPathsFallback(t *testing.T) {
	// No entry in RepoPaths → fallback to ~/work/<basename>.
	gh := newHappyGH()
	ssh := &fakeSSH{}
	form := ShipForm{
		Title: "T", Body: "B", Repo: "owner/cool-thing", IssueNew: true, Host: "local",
	}
	todo := &model.TodoItem{Text: "x"}
	if _, err := Orchestrate(gh, ssh, form, OrchestrateConfig{
		ProjectURL: "https://github.com/orgs/kumo-ai/projects/9",
		RepoPaths:  nil, // no map
	}, todo); err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if len(ssh.calls) == 0 {
		t.Fatal("expected ssh.Run for ts new")
	}
	// Should have used ~/work/cool-thing as the cwd for ts new.
	if !strings.Contains(ssh.calls[0].cmd, "cool-thing") {
		t.Errorf("expected fallback path containing repo basename, got %q", ssh.calls[0].cmd)
	}
}

func ghJoin(gh *fakeGH) string {
	parts := make([]string, len(gh.calls))
	for i, c := range gh.calls {
		parts[i] = strings.Join(c.args, " ")
	}
	return strings.Join(parts, " | ")
}
