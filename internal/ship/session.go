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

	// Build the inner shell script. We chain `; tmux has-session -t
	// <name>` after `ts new` so that the final exit code reflects
	// whether the session ended up existing — not whether `ts new`
	// returned zero. This matters because `ts new` ends with
	// `tmux attach-session`, which fails on a non-interactive ssh
	// ("open terminal failed: not a terminal") even though the session
	// was already created in a prior step. From the user's POV the
	// session is up; tack should treat it that way.
	//
	// shellQuotePath handles the leading-`~` case for the repo path.
	// shellQuote handles arbitrary content (the session name).
	var inner string
	if repoPath == "" {
		inner = fmt.Sprintf("ts new %s; tmux has-session -t %s", shellQuote(name), shellQuote(name))
	} else {
		inner = fmt.Sprintf("cd %s && ts new %s; tmux has-session -t %s",
			shellQuotePath(repoPath), shellQuote(name), shellQuote(name))
	}
	// Wrap in `bash -lc` so PATH includes the user's ~/bin (where `ts`
	// usually lives). Plain non-interactive ssh doesn't source
	// .bash_profile / .profile, so $HOME/bin would not be on PATH
	// without the login-shell wrapper.
	createCmd := fmt.Sprintf("bash -lc %s", shellQuote(inner))
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
//
// Use this for arbitrary user-controlled strings (session names, free
// text). Don't use it for filesystem paths that may start with `~` —
// see shellQuotePath, which preserves shell expansion semantics.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuotePath quotes a filesystem path for /bin/sh while preserving
// the shell's tilde-and-$HOME expansion behavior. A leading `~/` is
// rewritten to `$HOME/`, and the result is wrapped in double quotes so
// `$HOME` still expands. Embedded `"` and `\` get backslash-escaped.
//
// Why not single quotes: sh does not expand `~` or `$VAR` inside single
// quotes, and we can't expand `~` ourselves Go-side because in remote
// SSH cases the home dir we'd substitute is the local Mac's, not the
// remote host's. Substituting to `$HOME` works in both cases — the
// final shell that runs the command (local or remote) does the right
// thing.
func shellQuotePath(p string) string {
	switch {
	case p == "~":
		p = "$HOME"
	case strings.HasPrefix(p, "~/"):
		p = "$HOME/" + p[2:]
	}
	// Inside double quotes, `\` and `"` need escaping; `$` is left alone
	// so $HOME (and any caller-set vars in the path) expand naturally.
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `"`, `\"`)
	return `"` + p + `"`
}
