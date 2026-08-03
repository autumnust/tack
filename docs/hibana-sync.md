# Hibana synchronization and batch editing

Hibana notes use stable 26-character IDs. Text edits retain the same ID, while deletion records prevent a stale device from restoring a deleted note.

## Coordinated upgrade requirement

Every device using the same Hibana remote must run a Tack version that supports stable note IDs before any device edits notes. An older client represents an edit as deleting one ID and creating another. Mixing that behavior with stable-ID clients can bypass the conflict checks expected by batch editing.

## Batch workspace decisions

- One Markdown file in `notes/` represents one note. Existing filenames retain their ID; new files have no existing ID.
- Foreign files, links, and nested directories inside `notes/` fail validation.
- Removing an existing ID while fully rewriting the file is interpreted as deleting the old note and adding a new note because Tack cannot infer identity from unrelated text.
- A failed remote pull does not discard accepted local work. Commit may complete durably offline and reports the remote error or pending event count so synchronization can be retried.
- `:diff` is read-only. `:commit` checks the exported base, workspace, remote-aware local state, and the exact local state again while applying the event batch.

Use `:batch` from the Hibana planning section, review with `:diff`, and then choose `:commit` or `:abort`. Closing Cursor has no commit behavior.

For one note, place the Hibana cursor on that note and run `:ide`. Tack opens the note in Cursor and waits. Save the file and close that Cursor tab to return to Tack; Tack then applies the edit through the normal single-note save and synchronization path. The existing `e` key continues to use `VISUAL`, `EDITOR`, or Vim. Set `TACK_IDE` to another command with `--wait` support to replace Cursor for `:ide`.
