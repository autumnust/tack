package ship

import (
	"fmt"
	"strings"
)

// SessionResult describes what CreateSession actually did. It surfaces
// the known v1 limitation (notes-path TBD until tss formalizes the
// schema) so the orchestrator can include it in the user-facing status.
type SessionResult struct {
	Host        string
	SessionName string
	TicketRef   string // <repo>#<num>, what we *want* in the notes header

	// NotesPathTBD is true while tack uses the v1 fallback path. In v1,
	// tack runs `ts new <name>` but cannot write the canonical
	// `ticket: <ref>` notes header because the path isn't documented yet
	// on the tss side. tss ask #2 closes this. Surface to the user.
	NotesPathTBD bool
}

// CreateSession creates a tmux session via `ts new` on the given host
// and (best-effort, in v1) records the ticket reference in the session's
// notes header.
//
// v1 path: always two-step.
//
//	ssh <host> 'ts new <name>'
//	(notes-path write — currently a no-op; surfaced via NotesPathTBD).
//
// host == "local" runs without an ssh wrapper.
func CreateSession(ssh SSHRunner, host, name, repoPath string, ticket IssueRef) (SessionResult, error) {
	res := SessionResult{
		Host:         host,
		SessionName:  name,
		TicketRef:    fmt.Sprintf("%s#%d", ticket.Repo, ticket.Number),
		NotesPathTBD: true, // v1 always; v2 flips this once `ts new --issue` lands
	}

	// `ts new <name>` is run from the repo working tree so the new tmux
	// session inherits that as its cwd. We `cd` first instead of using
	// any flag because we don't know the ts CLI shape on every host yet.
	createCmd := fmt.Sprintf("cd %s && ts new %s", shellQuote(repoPath), shellQuote(name))
	if _, err := ssh.Run(host, createCmd); err != nil {
		return res, fmt.Errorf("ts new %s on %s: %w", name, host, err)
	}

	// Notes write — placeholder. The path tss writes to isn't documented
	// in the tack-ship-integration spec yet (ask #2). Once tss documents
	// it, replace this with a real write; until then we leave it as a
	// no-op and let NotesPathTBD bubble up to the user.
	//
	// We DON'T fail the session creation if the notes write fails —
	// session creation is the load-bearing step.

	return res, nil
}

// shellQuote returns a single-quoted version of s safe to splice into a
// /bin/sh command. Embedded single quotes get the standard '\'' dance.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
