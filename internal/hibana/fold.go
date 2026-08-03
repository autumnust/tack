package hibana

import (
	"sort"
	"time"
)

// Note is the public, folded view of a still-live note. UpdatedAt is the
// timestamp of the most recent event affecting it.
type Note struct {
	ID        ID        `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Fold reduces an event stream to live notes, in original add order.
//
// Semantics keyed on NoteID:
//   - An `add` event introduces a new note id or updates a live one.
//   - A `delete` event removes the note id from the live set permanently.
//     A later `add` of the same NoteID is ignored.
func Fold(events []Event) []Note {
	type slot struct {
		note  Note
		order int // first time we saw this note id
	}
	live := map[ID]*slot{}
	dead := map[ID]struct{}{}
	order := 0
	for _, e := range events {
		switch e.Op {
		case OpAdd:
			if _, gone := dead[e.NoteID]; gone {
				continue
			}
			s, ok := live[e.NoteID]
			if !ok {
				s = &slot{order: order}
				order++
				live[e.NoteID] = s
			}
			s.note = Note{
				ID:        e.NoteID,
				Text:      e.Text,
				CreatedAt: e.CreatedAt,
				UpdatedAt: e.TS,
			}
		case OpDelete:
			dead[e.NoteID] = struct{}{}
			delete(live, e.NoteID)
		}
	}
	out := make([]Note, 0, len(live))
	for _, s := range live {
		out = append(out, s.note)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return live[out[i].ID].order < live[out[j].ID].order
	})
	return out
}

// SortByRecency reorders notes so the most recently updated come first.
// Equal timestamps fall back to id order for stability.
func SortByRecency(notes []Note) {
	sort.SliceStable(notes, func(i, j int) bool {
		if !notes[i].UpdatedAt.Equal(notes[j].UpdatedAt) {
			return notes[i].UpdatedAt.After(notes[j].UpdatedAt)
		}
		return notes[i].ID < notes[j].ID
	})
}
