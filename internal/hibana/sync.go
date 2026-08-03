package hibana

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// pushedSet is a file-backed set of *event* ids that have been successfully
// reflected in Redis. Keying by EventID (not NoteID) lets us track add and
// delete events on the same note as separate, independently-pushable rows.
//
// Layout: one EventID per line, no header. Resilient to torn writes (last
// line without a newline is ignored on read).
type pushedSet struct {
	path string
	mu   sync.Mutex
	mem  map[ID]struct{}
}

func newPushedSet(path string) (*pushedSet, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	ps := &pushedSet{path: path, mem: map[ID]struct{}{}}
	if err := ps.load(); err != nil {
		return nil, err
	}
	return ps, nil
}

func (p *pushedSet) load() error {
	f, err := os.Open(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	r := bufio.NewScanner(f)
	r.Buffer(make([]byte, 0, 64), 1024*1024)
	for r.Scan() {
		s := r.Text()
		if !ValidID(s) {
			continue
		}
		p.mem[ID(s)] = struct{}{}
	}
	return r.Err()
}

func (p *pushedSet) Has(id ID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.mem[id]
	return ok
}

func (p *pushedSet) Add(id ID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.mem[id]; ok {
		return nil
	}
	f, err := os.OpenFile(p.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, string(id)); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	p.mem[id] = struct{}{}
	return nil
}

// Restrict rewrites the file to keep only ids in `keep`. Used by Compact
// so the sidecar doesn't grow forever.
func (p *pushedSet) Restrict(keep map[ID]struct{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	tmp := p.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	newMem := make(map[ID]struct{}, len(p.mem))
	for id := range p.mem {
		if _, ok := keep[id]; !ok {
			continue
		}
		if _, err := fmt.Fprintln(f, string(id)); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		newMem[id] = struct{}{}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p.path); err != nil {
		return err
	}
	p.mem = newMem
	return nil
}

// SyncReport summarizes what happened during a Sync call.
type SyncReport struct {
	Pushed int // events sent to Redis (idempotent retries don't count)
	Pulled int // events appended locally from Redis state we hadn't seen
	Err    error
}

// SyncOnce runs Push then Pull once. Both halves are best-effort: if the
// network or Redis is unreachable, we return what we managed plus the
// error.
func SyncOnce(ctx context.Context, log *Log, pushed *pushedSet, backend Backend) SyncReport {
	rep := SyncReport{}
	if !backend.Enabled() {
		return rep
	}
	if err := pushOnce(ctx, log, pushed, backend, &rep); err != nil {
		rep.Err = err
		return rep
	}
	if err := pullOnce(ctx, log, pushed, backend, &rep); err != nil {
		rep.Err = err
		return rep
	}
	return rep
}

// pushOnce walks the local log and sends every event whose EventID is not
// yet in the pushed set. Each event maps to either an HSET (for adds) or
// an HDEL + SADD pair (for deletes). On any transport error we abort the
// loop so we don't burn through retries while the network is down — next
// call resumes from where we left off.
func pushOnce(ctx context.Context, log *Log, pushed *pushedSet, backend Backend, rep *SyncReport) error {
	events, err := log.Read()
	if err != nil {
		return err
	}
	for _, ev := range events {
		if pushed.Has(ev.EventID) {
			continue
		}
		if err := pushEvent(ctx, backend, ev); err != nil {
			return fmt.Errorf("push %s: %w", ev.EventID, err)
		}
		if err := pushed.Add(ev.EventID); err != nil {
			return fmt.Errorf("mark pushed %s: %w", ev.EventID, err)
		}
		rep.Pushed++
	}
	return nil
}

func pushEvent(ctx context.Context, backend Backend, ev Event) error {
	switch ev.Op {
	case OpAdd:
		graves, err := backend.SMembers(ctx, GravesKey)
		if err != nil {
			return err
		}
		for _, id := range graves {
			if id == string(ev.NoteID) {
				return nil
			}
		}
		val, err := json.Marshal(noteFromEvent(ev))
		if err != nil {
			return err
		}
		return backend.HSet(ctx, HashKey, string(ev.NoteID), string(val))
	case OpDelete:
		if err := backend.HDel(ctx, HashKey, string(ev.NoteID)); err != nil {
			return err
		}
		return backend.SAdd(ctx, GravesKey, string(ev.NoteID))
	}
	return fmt.Errorf("unknown op %q", ev.Op)
}

// pullOnce inspects remote state and appends synthetic events to the local
// log for anything we haven't seen yet.
//
// Three cases:
//  1. A remote live NoteID is absent locally, so append an add.
//  2. A remote live NoteID is newer than our known version, so append an update.
//  3. A remote grave contains a locally live NoteID, so append a delete.
//
// In both cases we mark the synthetic event's EventID as already-pushed so
// we don't echo it back to Redis next push.
func pullOnce(ctx context.Context, log *Log, pushed *pushedSet, backend Backend, rep *SyncReport) error {
	graves, err := backend.SMembers(ctx, GravesKey)
	if err != nil {
		return err
	}
	graveSet := map[string]struct{}{}
	for _, g := range graves {
		graveSet[g] = struct{}{}
	}
	hashKeys, err := backend.HKeys(ctx, HashKey)
	if err != nil {
		return err
	}
	events, err := log.Read()
	if err != nil {
		return err
	}
	localLive := map[ID]Note{}
	for _, n := range Fold(events) {
		localLive[n.ID] = n
	}

	// Cases 1 and 2: remote-only adds and updates to known IDs.
	var candidates []string
	for _, k := range hashKeys {
		if !ValidID(k) {
			continue
		}
		if _, gone := graveSet[k]; gone {
			continue
		}
		candidates = append(candidates, k)
	}
	if len(candidates) > 0 {
		vals, err := backend.HMGet(ctx, HashKey, candidates...)
		if err != nil {
			return err
		}
		for _, k := range candidates {
			raw, ok := vals[k]
			if !ok {
				continue
			}
			var n Note
			if err := json.Unmarshal([]byte(raw), &n); err != nil {
				continue
			}
			if local, have := localLive[ID(k)]; have {
				if local.Text == n.Text {
					continue
				}
			}
			ev := Event{
				EventID:   NewID(),
				NoteID:    ID(k),
				Op:        OpAdd,
				TS:        n.UpdatedAt,
				Text:      n.Text,
				CreatedAt: n.CreatedAt,
			}
			if err := log.Append(ev); err != nil {
				return err
			}
			if err := pushed.Add(ev.EventID); err != nil {
				return err
			}
			rep.Pulled++
		}
	}

	// Case 3: remote graves we haven't applied locally.
	for g := range graveSet {
		if !ValidID(g) {
			continue
		}
		if _, alive := localLive[ID(g)]; !alive {
			continue
		}
		ev := Event{
			EventID: NewID(),
			NoteID:  ID(g),
			Op:      OpDelete,
			TS:      nowFunc(),
		}
		if err := log.Append(ev); err != nil {
			return err
		}
		if err := pushed.Add(ev.EventID); err != nil {
			return err
		}
		rep.Pulled++
	}
	return nil
}

// noteFromEvent extracts a Note from an add event for storing in the
// remote hash. Mirrors the local Fold representation so HMGet round-trips
// cleanly.
func noteFromEvent(ev Event) Note {
	return Note{
		ID:        ev.NoteID,
		Text:      ev.Text,
		CreatedAt: ev.CreatedAt,
		UpdatedAt: ev.TS,
	}
}
