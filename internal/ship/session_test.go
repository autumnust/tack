package ship

import (
	"strings"
	"testing"
)

type fakeSSH struct {
	calls []sshCall
	err   error
}

type sshCall struct {
	host string
	cmd  string
}

func (f *fakeSSH) Run(host, command string) ([]byte, error) {
	f.calls = append(f.calls, sshCall{host: host, cmd: command})
	return nil, f.err
}

func TestCreateSession_LocalSkipsSSHWrapper(t *testing.T) {
	ssh := &fakeSSH{}
	ref := IssueRef{Number: 28151, Repo: "kumo-ai/kumo"}
	res, err := CreateSession(ssh, "local", "28151", "/Users/lei/work/kumo", ref)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(ssh.calls) == 0 {
		t.Fatal("expected at least one ssh.Run call")
	}
	for _, c := range ssh.calls {
		if c.host != "local" {
			t.Errorf("expected host=local, got %q", c.host)
		}
	}
	// First call must invoke `ts new <name>` (not the --issue variant in v1).
	first := ssh.calls[0].cmd
	if !strings.Contains(first, "ts new") || !strings.Contains(first, "28151") {
		t.Errorf("expected ts new 28151 in first call, got %q", first)
	}
	// Working directory should be passed somehow.
	if !strings.Contains(first, "/Users/lei/work/kumo") {
		t.Errorf("expected working dir in command, got %q", first)
	}
	// Result should describe what happened — including the known
	// limitation about notes-path being TBD until tss formalizes it.
	if res.SessionName != "28151" {
		t.Errorf("SessionName = %q", res.SessionName)
	}
}

func TestCreateSession_FallbackTwoStep(t *testing.T) {
	// v1 always uses the fallback path: `ts new <name>`, then write notes.
	ssh := &fakeSSH{}
	ref := IssueRef{Number: 28151, Repo: "kumo-ai/kumo"}
	res, err := CreateSession(ssh, "aws", "28151", "/home/lei/work/kumo", ref)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(ssh.calls) < 1 {
		t.Fatalf("expected ssh calls, got %d", len(ssh.calls))
	}
	// First: ts new on remote host
	if ssh.calls[0].host != "aws" {
		t.Errorf("first call host = %q, want aws", ssh.calls[0].host)
	}
	if !strings.Contains(ssh.calls[0].cmd, "ts new") || !strings.Contains(ssh.calls[0].cmd, "28151") {
		t.Errorf("first call cmd = %q", ssh.calls[0].cmd)
	}
	// Either a second call writes the ticket header, OR the result
	// surfaces the limitation that the notes-path is TBD. Since the
	// notes-path is documented by tss as <session>-notes.md placeholder,
	// the implementation may attempt the write best-effort. Either way,
	// the ticket ref should be threaded through the result.
	if res.TicketRef != "kumo-ai/kumo#28151" {
		t.Errorf("TicketRef = %q, want kumo-ai/kumo#28151", res.TicketRef)
	}
	// Known limitation should be communicated in v1.
	if !res.NotesPathTBD {
		t.Error("expected NotesPathTBD=true in v1 (until tss formalizes notes-path)")
	}
}

func TestCreateSession_PropagatesTsError(t *testing.T) {
	ssh := &fakeSSH{err: errSSHFake}
	ref := IssueRef{Number: 1, Repo: "kumo-ai/kumo"}
	if _, err := CreateSession(ssh, "local", "1", "/work", ref); err == nil {
		t.Error("expected error when ssh runner fails on ts new")
	}
}

var errSSHFake = &sshErr{msg: "ssh down"}

type sshErr struct{ msg string }

func (e *sshErr) Error() string { return e.msg }
