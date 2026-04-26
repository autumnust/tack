package hibana

import "time"

// addEv builds an add event for use in tests. Mints a fresh EventID;
// callers pass the NoteID they want to track in the fold/sync layer.
func addEv(noteID ID, ts time.Time, text string) Event {
	return Event{
		EventID:   NewID(),
		NoteID:    noteID,
		Op:        OpAdd,
		TS:        ts,
		Text:      text,
		CreatedAt: ts,
	}
}

// addEvAt is addEv but lets the caller separate created_at from event ts
// (i.e., simulate notes that were last touched some time after creation).
func addEvAt(noteID ID, createdAt, updatedAt time.Time, text string) Event {
	return Event{
		EventID:   NewID(),
		NoteID:    noteID,
		Op:        OpAdd,
		TS:        updatedAt,
		Text:      text,
		CreatedAt: createdAt,
	}
}

// delEv builds a delete event.
func delEv(noteID ID, ts time.Time) Event {
	return Event{
		EventID: NewID(),
		NoteID:  noteID,
		Op:      OpDelete,
		TS:      ts,
	}
}
