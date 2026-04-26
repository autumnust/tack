package hibana

import (
	"encoding/json"
	"fmt"
	"time"
)

// Op enumerates the on-disk event types. We deliberately keep this set
// minimal: the user-facing "edit" operation is implemented as delete+add
// rather than a third op, which keeps the fold logic obviously correct.
type Op string

const (
	OpAdd    Op = "add"
	OpDelete Op = "delete"
)

// Event is one line of the append-only log.
//
// Two ids: EventID identifies *this row* and is what the sync layer keys
// "already pushed" tracking off — it must be unique across all rows ever
// written. NoteID identifies the conceptual note the row is about; an
// add+delete pair share a NoteID but have distinct EventIDs.
type Event struct {
	EventID   ID        `json:"eid"`
	NoteID    ID        `json:"nid"`
	Op        Op        `json:"op"`
	TS        time.Time `json:"ts"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// MarshalLine returns the JSON encoding terminated with a newline, ready to
// append to the log.
func (e Event) MarshalLine() ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ParseEvent decodes one log line. A trailing newline is tolerated. Empty
// lines (whitespace only) are reported as errEmptyLine so the reader can
// skip them gracefully.
func ParseEvent(line []byte) (Event, error) {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r' || line[len(line)-1] == ' ' || line[len(line)-1] == '\t') {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		return Event{}, errEmptyLine
	}
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("hibana: parse: %w", err)
	}
	if !ValidID(string(e.EventID)) {
		return Event{}, fmt.Errorf("hibana: invalid event id %q", e.EventID)
	}
	if !ValidID(string(e.NoteID)) {
		return Event{}, fmt.Errorf("hibana: invalid note id %q", e.NoteID)
	}
	if e.Op != OpAdd && e.Op != OpDelete {
		return Event{}, fmt.Errorf("hibana: unknown op %q", e.Op)
	}
	return e, nil
}

// errEmptyLine is a sentinel so callers can distinguish blank lines (skip)
// from actually corrupt content (surface).
var errEmptyLine = fmt.Errorf("hibana: empty line")
