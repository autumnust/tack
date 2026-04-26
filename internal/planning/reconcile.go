package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autumnust/tack/internal/model"
)

// Conflict represents an unresolved divergence between a local offline write
// and a remote version that was written by a different device. Conflicts are
// persisted under <dir>/.conflicts/<sanitized-key>.json until the user
// resolves them via `tack --resolve-conflicts`.
type Conflict struct {
	Key            string    `json:"key"`
	DetectedAt     time.Time `json:"detected_at"`
	LocalPayload   string    `json:"local_payload"`
	RemotePayload  string    `json:"remote_payload"`
	CachedRev      int64     `json:"cached_rev"`
	RemoteRev      int64     `json:"remote_rev"`
}

// ReconcileReport summarizes what Reconcile did on one call.
type ReconcileReport struct {
	Pulled    []string
	Pushed    []string
	Replayed  int
	Conflicts []Conflict
}

// Reconcile drains the outbox and synchronizes blob keys.
//
// For append-only keys (tack:usage) outbox ops replay in order with no
// conflict check. tack:hibana is append-only for AddHibana, but SavePlan may
// buffer an authoritative full-list replacement to preserve delete/reorder
// semantics after a partial rewrite failure.
//
// For blob keys (tack:plan, tack:annotations) and per-file recap keys we
// compare cached rev vs remote rev:
//
//	remote_rev == cached_rev:  safe to push pending, INCR rev
//	remote_rev >  cached_rev:  conflict — persist and return
//	no pending, remote_rev > cached_rev:  pull remote, update cache
func (s *Store) Reconcile() (ReconcileReport, error) {
	var rep ReconcileReport
	if !s.RedisEnabled() {
		return rep, nil
	}
	ctx, cancel := s.reconcileCtx()
	defer cancel()

	ops, err := s.outbox.Load()
	if err != nil {
		return rep, err
	}

	// Bucket ops by key so we can decide per-key push vs conflict.
	byKey := map[string][]OutboxOp{}
	var order []string
	for _, op := range ops {
		if _, seen := byKey[op.Key]; !seen {
			order = append(order, op.Key)
		}
		byKey[op.Key] = append(byKey[op.Key], op)
	}

	var remaining []OutboxOp

	for _, key := range order {
		keyOps := byKey[key]
		switch {
		case key == keyUsage:
			if err := s.replayAppendOnly(ctx, keyOps); err != nil {
				remaining = append(remaining, keyOps...)
				continue
			}
			rep.Pushed = append(rep.Pushed, key)
			rep.Replayed += len(keyOps)

		case key == keyHibana:
			if err := s.replayHibana(ctx, keyOps); err != nil {
				remaining = append(remaining, keyOps...)
				continue
			}
			rep.Pushed = append(rep.Pushed, key)
			rep.Replayed += len(keyOps)

		case key == keyPlan || key == keyAnnotations:
			conflict, pushed, err := s.replayBlobKey(ctx, key, keyOps)
			if err != nil {
				remaining = append(remaining, keyOps...)
				continue
			}
			if conflict != nil {
				if err := s.saveConflict(*conflict); err != nil {
					return rep, err
				}
				rep.Conflicts = append(rep.Conflicts, *conflict)
				// Keep ops in outbox so a subsequent --resolve can consume them,
				// unless the user picks "remote" which discards them explicitly.
				remaining = append(remaining, keyOps...)
				continue
			}
			if pushed {
				rep.Pushed = append(rep.Pushed, key)
				rep.Replayed += len(keyOps)
			}

		case strings.HasPrefix(key, "tack:recap:"):
			// Recaps: per-name key. Replay only the latest op for the name
			// (earlier ones are superseded).
			last := keyOps[len(keyOps)-1]
			conflict, err := s.replayRecap(ctx, last)
			if err != nil {
				remaining = append(remaining, keyOps...)
				continue
			}
			if conflict != nil {
				if err := s.saveConflict(*conflict); err != nil {
					return rep, err
				}
				rep.Conflicts = append(rep.Conflicts, *conflict)
				remaining = append(remaining, keyOps...)
				continue
			}
			rep.Pushed = append(rep.Pushed, key)
			rep.Replayed += len(keyOps)

		default:
			// Unknown key — keep buffered.
			remaining = append(remaining, keyOps...)
		}
	}

	// Rewrite the outbox without the drained ops.
	if err := s.outbox.Rewrite(remaining); err != nil {
		return rep, err
	}

	// Pass 2: pull remote state for blob keys we don't have pending ops for,
	// updating the local cache so the next conflict check is accurate.
	for _, key := range []string{keyPlan, keyAnnotations} {
		if _, pending := byKey[key]; pending {
			continue
		}
		rev, ok, err := s.redis.Get(ctx, revKeyFor(key))
		if err != nil || !ok {
			continue
		}
		remoteRev := parseRev(rev)
		if remoteRev > s.syncState.Rev(key) {
			_ = s.syncState.SetRev(key, remoteRev)
			rep.Pulled = append(rep.Pulled, key)
		}
	}

	return rep, nil
}

func (s *Store) replayAppendOnly(ctx context.Context, ops []OutboxOp) error {
	// Coalesce into a single RPUSH per key for efficiency.
	if len(ops) == 0 {
		return nil
	}
	key := ops[0].Key
	vals := make([]string, 0, len(ops))
	for _, op := range ops {
		vals = append(vals, op.Payload)
	}
	return s.redis.RPush(ctx, key, vals...)
}

// replayHibana is a no-op: the hibana feature has moved to its own package
// with its own sync layer. Any leftover outbox entries from the old design
// are dropped on replay so they don't keep failing forever.
func (s *Store) replayHibana(_ context.Context, _ []OutboxOp) error {
	return nil
}

func decodeScratchNotes(payload string) ([]model.ScratchNote, error) {
	if payload == "" {
		return nil, nil
	}
	var notes []model.ScratchNote
	if err := json.Unmarshal([]byte(payload), &notes); err != nil {
		return nil, err
	}
	return notes, nil
}

func (s *Store) replayBlobKey(ctx context.Context, key string, ops []OutboxOp) (*Conflict, bool, error) {
	if len(ops) == 0 {
		return nil, false, nil
	}
	// The last pending op is the authoritative local version.
	last := ops[len(ops)-1]

	cachedRev := s.syncState.Rev(key)
	rev, ok, err := s.redis.Get(ctx, revKeyFor(key))
	if err != nil {
		return nil, false, err
	}
	var remoteRev int64
	if ok {
		remoteRev = parseRev(rev)
	}

	if remoteRev > cachedRev {
		// Conflict: someone else advanced the key while we were offline.
		remoteVal, _, _ := s.redis.Get(ctx, key)
		return &Conflict{
			Key:           key,
			DetectedAt:    time.Now(),
			LocalPayload:  last.Payload,
			RemotePayload: remoteVal,
			CachedRev:     cachedRev,
			RemoteRev:     remoteRev,
		}, false, nil
	}

	// Safe to push: write the local value, bump rev, update cache.
	if err := s.redis.Set(ctx, key, last.Payload); err != nil {
		return nil, false, err
	}
	newRev, err := s.redis.Incr(ctx, revKeyFor(key))
	if err != nil {
		return nil, false, err
	}
	if err := s.syncState.SetRev(key, newRev); err != nil {
		return nil, false, err
	}
	return nil, true, nil
}

func (s *Store) replayRecap(ctx context.Context, op OutboxOp) (*Conflict, error) {
	// Recap conflict heuristic: remote key exists with a different body.
	existing, ok, err := s.redis.Get(ctx, op.Key)
	if err != nil {
		return nil, err
	}
	if ok && existing != op.Payload {
		return &Conflict{
			Key:           op.Key,
			DetectedAt:    time.Now(),
			LocalPayload:  op.Payload,
			RemotePayload: existing,
		}, nil
	}
	if err := s.redis.Set(ctx, op.Key, op.Payload); err != nil {
		return nil, err
	}
	// Register the recap name in the index.
	name := strings.TrimPrefix(op.Key, "tack:recap:")
	if err := s.redis.SAdd(ctx, keyRecapsIndex, name); err != nil {
		return nil, err
	}
	return nil, nil
}

// revKeyFor returns the counter key that pairs with a blob key.
func revKeyFor(key string) string {
	switch key {
	case keyPlan:
		return keyPlanRev
	case keyAnnotations:
		return keyAnnRev
	default:
		return key + ":rev"
	}
}

func parseRev(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// Conflict persistence.

func (s *Store) conflictPath(key string) string {
	name := strings.ReplaceAll(key, ":", "_") + ".json"
	return filepath.Join(conflictsDir(s.dir), name)
}

func (s *Store) saveConflict(c Conflict) error {
	dir := conflictsDir(s.dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.conflictPath(c.Key), b, 0644)
}

// LoadConflicts returns all persisted unresolved conflicts.
func (s *Store) LoadConflicts() ([]Conflict, error) {
	dir := conflictsDir(s.dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Conflict
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c Conflict
		if err := json.Unmarshal(b, &c); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// ClearConflict removes the persisted conflict file for a key.
func (s *Store) ClearConflict(key string) error {
	err := os.Remove(s.conflictPath(key))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DropOutboxForKey removes any pending ops targeting key. Used when the user
// resolves a conflict by picking the remote version.
func (s *Store) DropOutboxForKey(key string) error {
	ops, err := s.outbox.Load()
	if err != nil {
		return err
	}
	var keep []OutboxOp
	for _, op := range ops {
		if op.Key != key {
			keep = append(keep, op)
		}
	}
	return s.outbox.Rewrite(keep)
}

// ResolveBlob writes a final resolved payload directly to Redis for a blob
// key, bumping the rev counter and updating the local cache. Also mirrors to
// the corresponding YAML file when applicable so offline reads stay fresh.
func (s *Store) ResolveBlob(key, payload string) error {
	if !s.RedisEnabled() {
		return fmt.Errorf("redis not enabled")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	// Recap keys aren't counter-backed.
	if strings.HasPrefix(key, "tack:recap:") {
		if err := s.redis.Set(ctx, key, payload); err != nil {
			return err
		}
		name := strings.TrimPrefix(key, "tack:recap:")
		_ = s.redis.SAdd(ctx, keyRecapsIndex, name)
		_ = os.MkdirAll(s.recapsDir(), 0755)
		_ = os.WriteFile(filepath.Join(s.recapsDir(), name+".md"), []byte(payload), 0644)
		return nil
	}
	if err := s.redis.Set(ctx, key, payload); err != nil {
		return err
	}
	newRev, err := s.redis.Incr(ctx, revKeyFor(key))
	if err != nil {
		return err
	}
	_ = s.syncState.SetRev(key, newRev)
	// Mirror to YAML for offline reads.
	switch key {
	case keyPlan:
		var plan model.Plan
		if err := json.Unmarshal([]byte(payload), &plan); err == nil {
			_ = s.saveYAML(s.planPath(), &plan)
		}
	case keyAnnotations:
		var ann model.Annotations
		if err := json.Unmarshal([]byte(payload), &ann); err == nil {
			_ = s.saveYAML(s.annotationsPath(), &ann)
		}
	}
	return nil
}

// HasUnresolvedConflicts is a convenience for callers that only care about the
// boolean.
func (s *Store) HasUnresolvedConflicts() bool {
	cs, err := s.LoadConflicts()
	return err == nil && len(cs) > 0
}

// OutboxPending returns the number of buffered ops.
func (s *Store) OutboxPending() int {
	if s.outbox == nil {
		return 0
	}
	return s.outbox.Len()
}

func (s *Store) reconcileCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// helpful for tests / debugging
var _ = fmt.Sprintf
