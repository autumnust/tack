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

// CreateSession must produce a command where the working-directory path
// expands a leading ~ properly in /bin/sh. Single-quoting the path
// (the original impl) silently broke tilde expansion: `cd '~/work/kumo'`
// errors with "No such file or directory" because sh treats ~ literally
// inside single quotes. The fix should produce a command where ~ is
// rewritten to $HOME before quoting (or otherwise made expandable).
func TestCreateSession_TildePathExpands(t *testing.T) {
	ssh := &fakeSSH{}
	ref := IssueRef{Number: 28151, Repo: "kumo-ai/kumo"}
	if _, err := CreateSession(ssh, "local", "28151", "~/work/kumo", ref); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cmd := ssh.calls[0].cmd
	// The unexpanded form is what's broken: literal '~/work/kumo' inside
	// single quotes. Reject it.
	if strings.Contains(cmd, "'~/") {
		t.Errorf("path is single-quoted with literal ~ — sh won't expand it. cmd: %q", cmd)
	}
	// Acceptable forms: $HOME-substituted, or absolute. Either way, no
	// literal tilde left.
	if strings.Contains(cmd, "~") && !strings.Contains(cmd, "$HOME") {
		t.Errorf("expected ~ to be substituted to $HOME (or expanded absolute), got %q", cmd)
	}
}

// An empty repoPath means "don't cd — just run ts new from whatever
// the host's default cwd is." This is the simplest behavior for the
// common case where the user doesn't care about the session's starting
// directory; tilde expansion + repo-path config become unnecessary.
func TestCreateSession_EmptyRepoSkipsCD(t *testing.T) {
	ssh := &fakeSSH{}
	ref := IssueRef{Number: 28400, Repo: "kumo-ai/kumo"}
	if _, err := CreateSession(ssh, "aws", "28400-multi", "", ref); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cmd := ssh.calls[0].cmd
	// `cd` should not appear as the first action. (`tmux has-session`
	// uses session-name targeting, not paths, so any literal `cd ` we
	// see here is the bug.)
	if strings.Contains(cmd, "cd \"") || strings.Contains(cmd, "cd '") {
		t.Errorf("empty repoPath should skip `cd`, got %q", cmd)
	}
	if !strings.Contains(cmd, "ts new") || !strings.Contains(cmd, "28400-multi") {
		t.Errorf("expected `ts new 28400-multi`, got %q", cmd)
	}
}

// The remote shell over plain SSH is non-interactive and may not have
// the user's ~/bin on PATH. Wrapping the command in `bash -lc` forces
// a login shell, which sources .bash_profile / .profile, which puts
// ~/bin on PATH, which is where `ts` lives.
func TestCreateSession_WrapsInLoginShell(t *testing.T) {
	ssh := &fakeSSH{}
	ref := IssueRef{Number: 28400, Repo: "kumo-ai/kumo"}
	if _, err := CreateSession(ssh, "aws", "28400-multi", "", ref); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cmd := ssh.calls[0].cmd
	if !strings.HasPrefix(cmd, "bash -lc ") {
		t.Errorf("command should start with `bash -lc`, got %q", cmd)
	}
}

// `ts new` ends with `tmux attach-session`, which fails under plain
// non-interactive ssh ("open terminal failed: not a terminal"). The
// session itself is created BEFORE the attach attempt, so the right
// success signal is "did the session end up existing?", not "did
// `ts new` exit zero?". Implementation chains `; tmux has-session -t
// <name>` after `ts new` so the final exit code reflects existence.
func TestCreateSession_TolerantOfAttachFailure(t *testing.T) {
	ssh := &fakeSSH{}
	ref := IssueRef{Number: 28400, Repo: "kumo-ai/kumo"}
	if _, err := CreateSession(ssh, "aws", "28400-multi", "", ref); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cmd := ssh.calls[0].cmd
	if !strings.Contains(cmd, "tmux has-session") {
		t.Errorf("expected post-create `tmux has-session` check, got %q", cmd)
	}
	if !strings.Contains(cmd, "28400-multi") {
		t.Errorf("has-session check should target session name, got %q", cmd)
	}
}

var errSSHFake = &sshErr{msg: "ssh down"}

type sshErr struct{ msg string }

func (e *sshErr) Error() string { return e.msg }
