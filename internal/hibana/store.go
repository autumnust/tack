package hibana

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store is the public facade hibana callers use. It wires together a Log,
// a pushedSet sidecar, and a Redis Backend.
type Store struct {
	dir     string
	log     *Log
	pushed  *pushedSet
	backend Backend
}

// nowFunc lets tests freeze time. Production code uses time.Now.
var nowFunc = time.Now

// DefaultDir returns the canonical storage directory: ~/.tack/. This is the
// install convention; callers should use it unless overridden by config.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tack"), nil
}

// Open creates a Store rooted at dir. The directory is created on demand.
// A nil backend is treated as "no Redis" — local-only operation.
func Open(dir string, backend Backend) (*Store, error) {
	dir = expandHome(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(dir, "hibana.jsonl")
	log, err := NewLog(logPath)
	if err != nil {
		return nil, err
	}
	pushedPath := filepath.Join(dir, ".sync", "hibana.pushed")
	pushed, err := newPushedSet(pushedPath)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		backend = NopBackend()
	}
	return &Store{
		dir:     dir,
		log:     log,
		pushed:  pushed,
		backend: backend,
	}, nil
}

// Dir returns the storage directory.
func (s *Store) Dir() string { return s.dir }

// LogPath returns the path of the underlying event log.
func (s *Store) LogPath() string { return s.log.Path() }

// Backend returns the configured Redis backend (or NopBackend).
func (s *Store) Backend() Backend { return s.backend }

// Add appends a fresh note. The note id is minted here and returned so
// callers can address it later.
func (s *Store) Add(text string) (Note, error) {
	now := nowFunc()
	noteID := NewID()
	ev := Event{
		EventID:   NewID(),
		NoteID:    noteID,
		Op:        OpAdd,
		TS:        now,
		Text:      text,
		CreatedAt: now,
	}
	if err := s.log.Append(ev); err != nil {
		return Note{}, err
	}
	return Note{ID: noteID, Text: text, CreatedAt: now, UpdatedAt: now}, nil
}

// AddWithTimestamps is the migration-only variant of Add: caller controls
// CreatedAt (e.g., when porting historical notes from plan.yaml).
func (s *Store) AddWithTimestamps(text string, createdAt time.Time, updatedAt time.Time) (Note, error) {
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	noteID := NewID()
	ev := Event{
		EventID:   NewID(),
		NoteID:    noteID,
		Op:        OpAdd,
		TS:        updatedAt,
		Text:      text,
		CreatedAt: createdAt,
	}
	if err := s.log.Append(ev); err != nil {
		return Note{}, err
	}
	return Note{ID: noteID, Text: text, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

// Edit replaces a note's text while retaining its NoteID and CreatedAt.
// A later add event for the same live NoteID is folded as an update. A
// delete remains final, so a stale update cannot restore a deleted note.
func (s *Store) Edit(id ID, newText string) (Note, error) {
	events, err := s.log.Read()
	if err != nil {
		return Note{}, err
	}
	var existing *Note
	for _, n := range Fold(events) {
		if n.ID == id {
			n2 := n
			existing = &n2
			break
		}
	}
	if existing == nil {
		return Note{}, fmt.Errorf("hibana: edit: id %s not found", id)
	}
	now := nowFunc()
	ev := Event{EventID: NewID(), NoteID: id, Op: OpAdd, TS: now, Text: newText, CreatedAt: existing.CreatedAt}
	if err := s.log.Append(ev); err != nil {
		return Note{}, err
	}
	return Note{ID: id, Text: newText, CreatedAt: existing.CreatedAt, UpdatedAt: now}, nil
}

// Delete removes a note. Idempotent: deleting a non-existent id appends a
// tombstone but doesn't fail (caller may have a stale view).
func (s *Store) Delete(id ID) error {
	ev := Event{EventID: NewID(), NoteID: id, Op: OpDelete, TS: nowFunc()}
	return s.log.Append(ev)
}

// List returns the current live notes in their original add order. Callers
// that want recency sort should call SortByRecency on the result.
func (s *Store) List() ([]Note, error) {
	events, err := s.log.Read()
	if err != nil {
		return nil, err
	}
	return Fold(events), nil
}

// Sync runs Push then Pull once with a per-call timeout. Errors are returned
// but are not fatal — the caller should display them and move on.
func (s *Store) Sync(ctx context.Context) SyncReport {
	if !s.backend.Enabled() {
		return SyncReport{}
	}
	return SyncOnce(ctx, s.log, s.pushed, s.backend)
}

// Compact rewrites the log to drop tombstones. Should be called after a
// successful Sync so the pushed-set restriction stays consistent with
// what the remote knows.
func (s *Store) Compact() error {
	if err := s.log.Compact(); err != nil {
		return err
	}
	events, err := s.log.Read()
	if err != nil {
		return err
	}
	keep := make(map[ID]struct{}, len(events))
	for _, ev := range events {
		keep[ev.EventID] = struct{}{}
	}
	return s.pushed.Restrict(keep)
}

// MaybeCompact runs Compact if the log has accumulated enough churn to
// justify it. No-op otherwise. Suitable for background callers that don't
// want to think about thresholds.
func (s *Store) MaybeCompact() error {
	want, err := s.log.ShouldCompact()
	if err != nil || !want {
		return err
	}
	return s.Compact()
}

// expandHome supports the ~/foo convention without dragging in a dep.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// PendingPush reports how many local events haven't yet been confirmed in
// the remote. Intended for status lines.
func (s *Store) PendingPush() (int, error) {
	events, err := s.log.Read()
	if err != nil {
		return 0, err
	}
	pending := 0
	for _, ev := range events {
		if !s.pushed.Has(ev.EventID) {
			pending++
		}
	}
	return pending, nil
}

// ErrNotFound is returned by callers that need to distinguish "no such id"
// from other failures. Currently only Edit produces it.
var ErrNotFound = errors.New("hibana: not found")
