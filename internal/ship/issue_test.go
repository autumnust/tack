package ship

import (
	"errors"
	"strings"
	"testing"
)

// fakeGH records calls and returns canned responses keyed off subcommand.
type fakeGH struct {
	calls []ghCall
	// resp maps a subcommand prefix (e.g. "issue create") to the
	// stdout/err response to return. Matched by HasPrefix on the
	// space-joined args.
	resp map[string]ghResp
}

type ghCall struct {
	args  []string
	stdin []byte
}

type ghResp struct {
	out []byte
	err error
}

func (f *fakeGH) Run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, ghCall{args: args})
	return f.match(args, nil)
}

func (f *fakeGH) RunStdin(stdin []byte, args ...string) ([]byte, error) {
	f.calls = append(f.calls, ghCall{args: args, stdin: stdin})
	return f.match(args, stdin)
}

func (f *fakeGH) match(args []string, _ []byte) ([]byte, error) {
	joined := strings.Join(args, " ")
	for prefix, r := range f.resp {
		if strings.HasPrefix(joined, prefix) {
			return r.out, r.err
		}
	}
	return nil, nil
}

func (f *fakeGH) lastWithPrefix(prefix string) *ghCall {
	for i := len(f.calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.Join(f.calls[i].args, " "), prefix) {
			return &f.calls[i]
		}
	}
	return nil
}

func TestCreateIssue_ParsesNumberFromURL(t *testing.T) {
	gh := &fakeGH{resp: map[string]ghResp{
		"issue create": {out: []byte("https://github.com/kumo-ai/kumo/issues/28151\n")},
	}}
	ref, err := CreateIssue(gh, "kumo-ai/kumo", "T", "B")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if ref.Number != 28151 {
		t.Errorf("Number = %d, want 28151", ref.Number)
	}
	if ref.Repo != "kumo-ai/kumo" {
		t.Errorf("Repo = %q", ref.Repo)
	}

	c := gh.lastWithPrefix("issue create")
	if c == nil {
		t.Fatal("expected issue create call")
	}
	args := strings.Join(c.args, " ")
	for _, want := range []string{"-R kumo-ai/kumo", "-t T", "-b B"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

func TestCreateIssue_PropagatesError(t *testing.T) {
	gh := &fakeGH{resp: map[string]ghResp{
		"issue create": {err: errors.New("boom")},
	}}
	if _, err := CreateIssue(gh, "kumo-ai/kumo", "T", "B"); err == nil {
		t.Error("expected error")
	}
}

func TestValidateIssue_AcceptsExisting(t *testing.T) {
	gh := &fakeGH{resp: map[string]ghResp{
		"issue view": {out: []byte(`{"number":42,"title":"X","url":"https://github.com/kumo-ai/kumo/issues/42","id":"I_kw1"}`)},
	}}
	ref, err := ValidateIssue(gh, "kumo-ai/kumo", 42)
	if err != nil {
		t.Fatalf("ValidateIssue: %v", err)
	}
	if ref.Number != 42 {
		t.Errorf("Number = %d", ref.Number)
	}
	if ref.NodeID != "I_kw1" {
		t.Errorf("NodeID = %q", ref.NodeID)
	}
}

func TestValidateIssue_RejectsMissing(t *testing.T) {
	gh := &fakeGH{resp: map[string]ghResp{
		"issue view": {err: errors.New("not found")},
	}}
	if _, err := ValidateIssue(gh, "kumo-ai/kumo", 999999); err == nil {
		t.Error("expected error for missing issue")
	}
}

func TestAddToProject_FiresGraphQL(t *testing.T) {
	gh := &fakeGH{resp: map[string]ghResp{
		"project item-add": {out: []byte("added\n")},
	}}
	ref := IssueRef{Number: 1, Repo: "kumo-ai/kumo", NodeID: "I_kw1", URL: "https://github.com/kumo-ai/kumo/issues/1"}
	if err := AddToProject(gh, "https://github.com/orgs/kumo-ai/projects/9", ref); err != nil {
		t.Fatalf("AddToProject: %v", err)
	}
	c := gh.lastWithPrefix("project item-add")
	if c == nil {
		t.Fatal("expected project item-add call")
	}
	args := strings.Join(c.args, " ")
	if !strings.Contains(args, ref.URL) {
		t.Errorf("missing issue URL in args: %q", args)
	}
	if !strings.Contains(args, "9") {
		t.Errorf("missing project number in args: %q", args)
	}
}

func TestLinkSubIssue_Idempotent_SameParent(t *testing.T) {
	// First call: query returns no parent → mutation fires.
	// Second call: query returns same parent → mutation should NOT fire (no-op).
	gh := &fakeGH{resp: map[string]ghResp{
		"api graphql": {out: []byte(`{"data":{"node":{"parent":null}}}`)},
	}}
	parent := IssueRef{Number: 100, Repo: "kumo-ai/kumo", NodeID: "I_PARENT"}
	child := IssueRef{Number: 200, Repo: "kumo-ai/kumo", NodeID: "I_CHILD"}

	if err := LinkSubIssue(gh, parent, child); err != nil {
		t.Fatalf("first LinkSubIssue: %v", err)
	}
	firstCalls := len(gh.calls)
	mutationCount := 0
	for _, c := range gh.calls {
		j := strings.Join(c.args, " ")
		// addSubIssue mutation goes through gh api graphql with stdin or -f
		if strings.Contains(string(c.stdin), "addSubIssue") || strings.Contains(j, "addSubIssue") {
			mutationCount++
		}
	}
	if mutationCount != 1 {
		t.Errorf("first run: expected 1 addSubIssue call, got %d (calls=%d)", mutationCount, firstCalls)
	}

	// Second invocation: parent now matches child's existing parent.
	gh.resp["api graphql"] = ghResp{out: []byte(`{"data":{"node":{"parent":{"id":"I_PARENT","number":100}}}}`)}
	gh.calls = nil

	if err := LinkSubIssue(gh, parent, child); err != nil {
		t.Fatalf("second LinkSubIssue: %v", err)
	}
	mutationCount = 0
	for _, c := range gh.calls {
		j := strings.Join(c.args, " ")
		if strings.Contains(string(c.stdin), "addSubIssue") || strings.Contains(j, "addSubIssue") {
			mutationCount++
		}
	}
	if mutationCount != 0 {
		t.Errorf("idempotent re-run should not fire mutation, got %d", mutationCount)
	}
}

func TestLinkSubIssue_DifferentParent_Errors(t *testing.T) {
	// Child already has a different parent — surface as error, no re-parent.
	gh := &fakeGH{resp: map[string]ghResp{
		"api graphql": {out: []byte(`{"data":{"node":{"parent":{"id":"I_OTHER","number":999}}}}`)},
	}}
	parent := IssueRef{Number: 100, Repo: "kumo-ai/kumo", NodeID: "I_PARENT"}
	child := IssueRef{Number: 200, Repo: "kumo-ai/kumo", NodeID: "I_CHILD"}

	err := LinkSubIssue(gh, parent, child)
	if err == nil {
		t.Fatal("expected error when child has a different parent")
	}
	if !strings.Contains(err.Error(), "different parent") && !strings.Contains(err.Error(), "already") {
		t.Errorf("error should mention different/already parent: %v", err)
	}
	for _, c := range gh.calls {
		if strings.Contains(string(c.stdin), "addSubIssue") {
			t.Errorf("must not fire addSubIssue when child has different parent")
		}
	}
}
