package discuss

import (
	"encoding/json"
	"fmt"
	"time"
)

// Op enumerates the on-disk event types for one discussion's log.
type Op string

const (
	OpCreated   Op = "created"
	OpUserMsg   Op = "user_msg"
	OpAssistant Op = "assistant_msg"
	OpClosed    Op = "closed"
)

// Event is one line of the per-discussion append-only log.
//
// "created" rows freeze the system prompt and seed-note ids onto the
// discussion at start time so changing config later doesn't retro-edit
// past discussions.
type Event struct {
	EventID      ID        `json:"eid"`
	DiscussionID ID        `json:"did"`
	Op           Op        `json:"op"`
	TS           time.Time `json:"ts"`

	// op="created":
	SeedNoteIDs  []string `json:"seed_note_ids,omitempty"`
	SeedNotes    []Seed   `json:"seed_notes,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
	Model        string   `json:"model,omitempty"`

	// op="user_msg" / "assistant_msg":
	Text string `json:"text,omitempty"`
}

// Seed is a single hibana note captured into a discussion at creation
// time. We snapshot the text rather than reference it by id so a later
// edit of the source note doesn't rewrite the conversation context.
type Seed struct {
	NoteID    string    `json:"note_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func (e Event) MarshalLine() ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func ParseEvent(line []byte) (Event, error) {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r' || line[len(line)-1] == ' ' || line[len(line)-1] == '\t') {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		return Event{}, errEmptyLine
	}
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("discuss: parse: %w", err)
	}
	if !ValidID(string(e.EventID)) {
		return Event{}, fmt.Errorf("discuss: invalid event id %q", e.EventID)
	}
	if !ValidID(string(e.DiscussionID)) {
		return Event{}, fmt.Errorf("discuss: invalid discussion id %q", e.DiscussionID)
	}
	switch e.Op {
	case OpCreated, OpUserMsg, OpAssistant, OpClosed:
	default:
		return Event{}, fmt.Errorf("discuss: unknown op %q", e.Op)
	}
	return e, nil
}

var errEmptyLine = fmt.Errorf("discuss: empty line")
