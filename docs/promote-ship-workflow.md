# Promote → Ship workflow plan

Two new commands in the tack TUI that take a thought from raw capture in
hibana all the way to an executing tmux session attached to a tracked
ticket. Two stages on purpose: the friction along this path comes in
different forms, and the user's "I'm thinking" mindset is different from
their "I'm ready to start" mindset. One command would force the second
mindset on every promotion.

## Stage 1 — `:promote N`

**Mental model:** a hibana row is a half-formed thought. The user decides
"this is real work, I want to do it." That deserves a moment of editing
to sharpen the language and add any context, then the row graduates to
the today list. The hibana original disappears because keeping a copy is
visual debt — the thought has graduated.

**Behavior:**
- User issues `:promote N` (1-indexed into the hibana panel) or invokes
  it via a keybinding while the cursor is on a hibana row.
- The TUI opens vim with a tmpfile prefilled with the note's current
  text. No template, no sections — just the raw text.
- On clean exit, the saved text becomes a new today todo (`model.TodoItem`
  with `Text=<saved text>`, `CreatedAt=now`, `IssueNum=0`).
- The hibana row is deleted via `hibana.Store.Delete(noteID)`.
- TUI status: `"Promoted to today."`

**On editor abort (no save / non-zero exit):** no-op. Hibana row stays.
No today todo created. Status: `"Promote canceled."`

**No external tools:** stage 1 doesn't touch GitHub, doesn't touch tss,
doesn't even touch Redis directly (it goes through the existing hibana
delete + plan save paths, which already handle sync).

## Stage 2 — `:ship N`

**Mental model:** a today todo is "I'm doing it." Ship is the moment
execution starts. The user needs to anchor it to a GitHub issue (new or
existing) and pick a place to run. Both choices benefit from a structured
template so the user can review before committing.

**Behavior:**
- User issues `:ship N` (1-indexed into the today panel) on a row.
- The TUI opens vim with a structured template:

  ```
  # Title
  <prefilled blank — user fills>

  # Body
  <prefilled with the today todo's text>

  # Repo
  kumo-ai/kumo

  # Issue
  new

  # Epic
  <empty>

  # Host
  local
  ```

**Field semantics:**

| Field | Required | Format | Behavior |
|---|---|---|---|
| `Title` | only when `Issue: new` | free text, single line | Used as `gh issue create -t`. Ignored when attaching. |
| `Body` | yes | markdown, multi-line | Issue body when `Issue: new`. Pre-filled from today text but user-editable. |
| `Repo` | yes | `<owner>/<repo>` | Target repo for `gh` operations and the working tree for the session. |
| `Issue` | yes | `new` or `#<N>` | `new` creates; `#<N>` attaches to existing (validated via `gh issue view`). |
| `Epic` | no | empty, `#<N>`, or `<owner>/<repo>#<N>` | If non-empty, link the resulting issue as a sub-issue under `<N>`. Bare `#<N>` resolves against `Repo`. Cross-repo linkage works because the underlying GraphQL `addSubIssue` mutation takes node IDs. |
| `Host` | yes | host name from ssh config | Where the tmux session runs. `local` skips ssh. |

**Edge case — attach + epic:** when `Issue: #<N>` (attach existing) AND `Epic` is non-empty, idempotently add the sub-issue link if missing, no-op if already linked. Surface a warning if the existing issue already has a *different* parent — don't silently re-parent.

- On save:
  1. Parse the template. Required fields: Title (only when
     `Issue: new`), Body, Repo, Issue, Host.
  2. **Issue resolution:**
     - If `Issue: new`: `gh issue create -R <repo> -t "<title>" -b "<body>"`,
       capture the returned issue number, then add to the configured
       project via `gh api graphql` (search the codebase for the
       existing project-add pattern).
     - If `Issue: #<N>`: validate via `gh issue view <N> -R <repo>`. If
       missing or in wrong repo, surface error and leave today row
       untouched.
     - If `Epic` is non-empty: after issue resolution, call
       `LinkSubIssue(parent, child)` (in `internal/ship/issue.go`) which
       fires the GraphQL `addSubIssue` mutation. Idempotent — safe to
       re-run.
  3. **Session creation:**
     - Session name = the issue number (e.g., `28151`).
     - Working directory = the local clone of `<repo>` on `<host>`
       (resolved via the repo-paths config — see Open Questions § 1).
     - **Preferred path** (once the tss `ts new --issue` change lands —
       see `tss/docs/tack-ship-integration.md` § 3):
       ```
       ssh <host> 'ts new <session-name> --issue <repo>#<issue>'
       ```
       `ts` writes the canonical `ticket:` notes header itself; tack
       doesn't need to know the notes file path.
     - **Fallback path** (until the tss change ships):
       ```
       ssh <host> 'ts new <session-name>'
       ssh <host> 'echo "ticket: <repo>#<issue>" > <notes-path>'
       ```
       The `<notes-path>` is documented by tss when the schema is
       finalized; until then the second SSH is a no-op and `tss ls`
       won't show the ticket column for tack-created sessions. Issue
       link still works via `IssueNum` on the today row.
     - For `host == local`, both paths skip the `ssh` wrapper.
  4. **Today row update:**
     - Set `IssueNum = <N>` on the `model.TodoItem`. The row's text
       stays untouched. The TUI render reads `IssueNum` and prefixes a
       visual marker (e.g., ` #28151`) at draw time.
  5. TUI status: `"Shipped #28151 → aws:28151. Attach with: tss aws:28151"`

**On editor abort:** no-op. Nothing on GitHub, nothing on the host. Today
row unchanged.

**On partial failure** (issue created successfully, session creation
fails — host unreachable, `ts` not installed, etc.): tack surfaces the
partial state — issue number, what step failed, what's left to do — and
exits without auto-recovery. The user either re-runs `:ship` (which sees
the populated `IssueNum`, skips issue creation, retries session
creation) or attaches manually with `tss <host>:<session>`. Tack does
*not* delete the just-created GH issue; that's destructive and the
issue is real work. Tack does *not* silently retry session creation,
because doing so would race a manually-created session and produce
duplicates.

## Data model touch points

- `model.TodoItem` already has `IssueNum int`. Stage 2 just populates it.
- `model.ScratchNote` has `Id` since the hibana redesign — stage 1 uses
  `hibana.Store.Delete` to remove the note cleanly.
- No schema changes required for either stage.

## Code surface (paths to write or edit)

| File | What |
|---|---|
| `internal/tui/app.go` | `:promote` and `:ship` command handlers. Both invoke `openEditor(...)` with appropriate prefill. |
| `internal/tui/command.go` (or wherever commands route) | Register `:promote` and `:ship`. |
| `internal/tui/planview.go` (modify) | When rendering a today row with `IssueNum > 0`, prefix a visual marker (` #<N>`). |
| `internal/ship/template.go` (new) | Parse/render the ship template. Functions: `RenderTemplate(todo) string`, `Parse(text) (ShipForm, error)`. Pure; unit-testable. |
| `internal/ship/issue.go` (new) | `CreateIssue(repo, title, body) (IssueRef, error)` — wraps `gh issue create`. `ValidateIssue(repo, num) (IssueRef, error)` — wraps `gh issue view`. `AddToProject(projectURL, ref) error`. `LinkSubIssue(parent, child IssueRef) error` — wraps GraphQL `addSubIssue`, idempotent. |
| `internal/ship/session.go` (new) | `CreateSession(host, name, repoPath, ticketRef) error` — wraps `ssh <host> ts new <name> --issue <ref>` (preferred) or the fallback two-step pattern. Handles `host == local` by skipping ssh. |
| `internal/ship/orchestrate.go` (new) | `:ship` orchestration: parse → resolve issue → link epic if requested → add to project → create session → update todo. Composable from the unit-tested pieces. |
| `internal/ship/runner.go` (new) | Interfaces (`GHRunner`, `SSHRunner`) so tests can inject fakes. Default impls just exec the binary. |

## Tests (per `AGENTS.md` testing workflow rule)

Add tests in this order — tests precede implementation per the project
convention:

1. `internal/ship/template_test.go` — table-driven: render a template
   for known todo shapes; parse a template back; round-trip; reject
   malformed templates; correctly parse Epic in all three valid forms
   (empty, `#<N>`, `<owner>/<repo>#<N>`) and reject malformed.
2. `internal/ship/issue_test.go` — fake `GHRunner`. Test:
   - `CreateIssue` returns the right number from `gh issue create` output.
   - `ValidateIssue` accepts existing, rejects missing, rejects wrong-repo.
   - `AddToProject` issues the expected GraphQL mutation.
   - `LinkSubIssue` is idempotent: re-running with same parent/child is
     a no-op; running with a *different* parent on an issue that already
     has one surfaces a warning rather than silently re-parenting.
3. `internal/ship/session_test.go` — fake `SSHRunner`. Test:
   - `host == local` skips the ssh wrapper.
   - Preferred path constructs `ts new <name> --issue <ref>` correctly.
   - Fallback path issues the two-step `ts new` + notes-write pattern.
4. `internal/ship/orchestrate_test.go` — wire the fakes together. Test
   the full flow:
   - Happy path (new issue, no epic): issue created, project add, session
     created, today row updated.
   - Happy path with epic: link mutation fires after issue creation.
   - Attach-existing path: skips create, validates, optionally links epic.
   - Partial failure (session create fails after issue create): issue
     stays in place, status surfaces, today row's `IssueNum` is set so
     a re-run finds the existing issue.
5. `internal/tui/workflow_test.go` — integration: simulate `:promote`
   and `:ship` keypresses end-to-end with `editorFinishedMsg`. Confirm:
   - `:promote` creates a today todo and removes the hibana row.
   - `:promote` cancel (no save) is a no-op.
   - `:ship` (with fakes) populates `IssueNum` and produces the right
     status string.
6. `internal/tui/planview_test.go` — render: a today row with
   `IssueNum > 0` shows the ` #<N>` marker; a row with `IssueNum == 0`
   shows no marker.

## Dependencies / blockers

| Dependency | What's needed | Status |
|---|---|---|
| tss `hosts` listing (machine-readable) | so the ship template's Host field can validate user input and tab-complete (future) | Requested via `tss/docs/tack-ship-integration.md`. Until landed, tack hardcodes the ssh config host names (acceptable for stage 1 of the rollout). |
| tss `ls` showing ticket from notes header | so the session is visibly linked to the ticket from the cross-host viewer | Same doc. Until landed, the link is one-way — tack populates IssueNum, but `tss ls` won't surface it. |
| `gh` CLI authenticated locally | for issue creation / lookup | Already true on the user's machine; just a precondition. |
| Local clone path per repo | `:ship` needs to know where `kumo-ai/kumo` lives on disk to pass as `cwd` to `ts new` | Needs a new config field, e.g., `repos.kumo-ai/kumo: /Users/lei/work/kumo`. See "Open questions." |

## Open questions

These two need a call before implementation starts:

1. **Repo paths config.** Where on disk does each repo live on each host?
   - **Flat map in config**: `repos: { "kumo-ai/kumo": "~/work/kumo" }`.
   - **Convention-based** (no config): always `~/work/<basename>`.
   - **Per-host override map**: nested under host name.

   *Recommendation:* flat with convention fallback. If the `repos` map
   has an entry, use it; otherwise use `~/work/<basename>`. Per-host
   overrides only when a host actually diverges.

2. **Host smart default once tss exposes the host listing.** Default
   to `local` always, or pick a "smart" host based on repo (e.g., `aws`
   for kumo work)?

   *Recommendation:* always `local`. Explicit beats implicit for
   something as consequential as "where is my work running."

## Sequence diagram

Preferred path (assumes `ts new --issue` from tss-side ask § 3 has shipped):

```
USER          TACK TUI                   GH        SSH→HOST       TS/TMUX
  |              |                       |          |             |
  | :promote 3   |                       |          |             |
  |------------->|                       |          |             |
  |              |--openEditor(text)----> vim       |             |
  |              |<--saved--------------  |         |             |
  |              | hibana.Delete(id)      |         |             |
  |              | plan.Today.append(...)           |             |
  |              | SavePlan               |         |             |
  |              |---"Promoted"           |         |             |
  | :ship 2      |                        |         |             |
  |------------->|                        |         |             |
  |              |--openEditor(template)-> vim      |             |
  |              |<--saved--------------  |         |             |
  |              | parse                  |         |             |
  |              | gh issue create -----> |         |             |
  |              |<-- #28151 --------------         |             |
  |              | (if Epic) addSubIssue->|         |             |
  |              | gh project add ------> |         |             |
  |              | ssh aws 'ts new 28151 --issue ...'------------>| session + notes set
  |              | TodoItem.IssueNum = 28151                      |
  |              |---"Shipped #28151 → aws:28151"                 |
```

Fallback path (until tss ships the `--issue` flag): replace the single
`ts new ... --issue` call with two SSH round-trips — one for `ts new`
and one to write the notes header. Same end state on the host.

## What's NOT in scope

- Cross-host migration of an in-flight session.
- Auto-detection of an issue from text (NLP-style).
- A "ship" undo command.
- Anything board-side (issue display already handles `IssueNum`).
- Replacing the `ts new` invocation with a tack-native tmux control-mode
  client. Captured for future investigation as
  [issue #3](https://github.com/autumnust/tack/issues/3) — interesting
  if/when we need live tmux event streams or want to deprecate `ts`.

These can be follow-ups; none of them block the first version.
