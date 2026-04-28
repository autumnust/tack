package ship

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/autumnust/tack/internal/model"
)

// OrchestrateConfig holds the host-side knobs the orchestrator needs:
// where the project lives (for `project item-add`) and where each repo
// lives on disk on each host (for the session's working tree).
type OrchestrateConfig struct {
	// ProjectURL is the GitHub Project to add new issues to. Empty
	// means skip project add.
	ProjectURL string

	// RepoPaths maps "<owner>/<repo>" → absolute path on disk. If a repo
	// has no entry, fallback to "~/work/<basename>" — see the design
	// doc's open-questions §1.
	RepoPaths map[string]string
}

// OrchestrateResult is what the orchestrator returns when (or before) it
// finishes. The Issue is populated as soon as it's resolved; Session is
// populated only after `ts new` succeeds. On partial failure the caller
// can read these to surface state to the user.
type OrchestrateResult struct {
	Issue   IssueRef
	Session SessionResult
}

// Orchestrate is the full :ship pipeline: resolve issue (create or
// validate-existing), optionally link an Epic, add to the configured
// project, then create the tmux session and stamp the todo with the
// issue number.
//
// The function is composable from the unit-tested pieces; the result is
// always populated as far as the pipeline got, even when an error is
// returned, so the caller can render a partial-state status message.
func Orchestrate(gh GHRunner, ssh SSHRunner, form ShipForm, cfg OrchestrateConfig, todo *model.TodoItem) (OrchestrateResult, error) {
	var res OrchestrateResult

	// 1. Resolve issue.
	var issue IssueRef
	var err error
	switch {
	case form.IssueNew:
		issue, err = CreateIssue(gh, form.Repo, form.Title, form.Body)
		if err != nil {
			return res, fmt.Errorf("create issue: %w", err)
		}
	default:
		issue, err = ValidateIssue(gh, form.Repo, form.IssueNum)
		if err != nil {
			return res, fmt.Errorf("validate issue: %w", err)
		}
	}
	res.Issue = issue

	// 2. Stamp todo immediately. This is the key invariant for partial
	//    failure: even if later steps fail, a re-run sees IssueNum and
	//    skips create.
	if todo != nil {
		todo.IssueNum = issue.Number
	}

	// 3. Add to project (best-effort: project misconfig shouldn't block
	//    the rest of the flow, but we still surface the error).
	if cfg.ProjectURL != "" {
		if err := AddToProject(gh, cfg.ProjectURL, issue); err != nil {
			return res, fmt.Errorf("add to project: %w", err)
		}
	}

	// 4. Link epic if requested. Resolve the parent IssueRef (with
	//    NodeID) by validating it via gh issue view.
	if form.EpicNum > 0 {
		epicRepo := form.EpicRepo
		if epicRepo == "" {
			epicRepo = form.Repo
		}
		parentRef, verr := ValidateIssue(gh, epicRepo, form.EpicNum)
		if verr != nil {
			return res, fmt.Errorf("epic #%d: %w", form.EpicNum, verr)
		}
		if err := LinkSubIssue(gh, parentRef, issue); err != nil {
			return res, fmt.Errorf("link epic: %w", err)
		}
	}

	// 5. Create session.
	repoPath := resolveRepoPath(cfg.RepoPaths, form.Repo)
	sessionName := strconv.Itoa(issue.Number)
	sess, err := CreateSession(ssh, form.Host, sessionName, repoPath, issue)
	if err != nil {
		return res, fmt.Errorf("create session: %w", err)
	}
	res.Session = sess

	return res, nil
}

// ResolveRepoPath returns the on-disk path for repo. If the config map
// has an entry, use it. Otherwise fall back to ~/work/<basename> per the
// finalized open-question recommendation. Exported so callers outside
// the orchestrator (e.g., the board-mode :ship flow) can reuse the same
// resolution logic without duplicating the fallback.
func ResolveRepoPath(paths map[string]string, repo string) string {
	if p, ok := paths[repo]; ok && p != "" {
		return p
	}
	// fallback: ~/work/<basename of owner/repo>
	return "~/work/" + path.Base(repo)
}

// resolveRepoPath kept as a thin wrapper for in-package callers; remove
// after the next round of refactoring if we standardize on the exported
// name.
func resolveRepoPath(paths map[string]string, repo string) string {
	return ResolveRepoPath(paths, repo)
}

// FormatStatus produces the user-visible "Shipped #N → host:N" string,
// appending a hint about the v1 limitation when relevant.
func FormatStatus(res OrchestrateResult, host string) string {
	parts := []string{
		fmt.Sprintf("Shipped #%d → %s:%s", res.Issue.Number, host, res.Session.SessionName),
	}
	parts = append(parts, fmt.Sprintf("Attach with: tss %s:%s", host, res.Session.SessionName))
	if res.Session.NotesPathTBD {
		parts = append(parts, "(note: ticket header not yet written; tss notes-path TBD)")
	}
	return strings.Join(parts, ". ")
}
