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
	"gopkg.in/yaml.v3"
)

// Store manages reading/writing planning files from a configurable directory.
//
// When a Redis backend is configured, it becomes the source of truth across
// devices; the on-disk YAML is still written as an offline-readable backup.
// Hibana (scratch) notes live in a dedicated Redis list (tack:hibana) so
// concurrent appends from multiple devices don't clobber each other.
type Store struct {
	dir       string
	syncer    Syncer
	redis     redisBackend
	outbox    *outbox
	syncState *syncState
}

// NewStore creates a store rooted at dir with no Redis backend.
func NewStore(dir string) (*Store, error) {
	return NewStoreWithRedis(dir, "", "")
}

// NewStoreWithRedis creates a store rooted at dir. If redisURL and redisToken
// are both non-empty, a REST-based Upstash backend is attached. Dir is also
// auto-attached to a GitSyncer when inside a git work tree.
func NewStoreWithRedis(dir, redisURL, redisToken string) (*Store, error) {
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, dir[1:])
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       dir,
		redis:     nopBackend{},
		outbox:    newOutbox(dir),
		syncState: newSyncState(dir),
	}
	if redisURL != "" && redisToken != "" {
		s.redis = newRESTBackend(redisURL, redisToken)
	}
	if gs := NewGitSyncer(dir); gs != nil {
		s.syncer = gs
	}
	return s, nil
}

// SetSyncer overrides the auto-detected syncer (pass nil to disable sync).
func (s *Store) SetSyncer(syncer Syncer) { s.syncer = syncer }

// setBackend is used by tests to inject a fake redisBackend.
func (s *Store) setBackend(b redisBackend) { s.redis = b }

// RedisEnabled reports whether a Redis backend is attached.
func (s *Store) RedisEnabled() bool { return s.redis != nil && s.redis.Enabled() }

func (s *Store) Dir() string { return s.dir }

func (s *Store) planPath() string        { return filepath.Join(s.dir, "plan.yaml") }
func (s *Store) annotationsPath() string { return filepath.Join(s.dir, "annotations.yaml") }

// A short per-call timeout keeps a flaky network from blocking the CLI.
func (s *Store) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

// Plan

// LoadPlan returns the plan from Redis (when configured and present) falling
// back to the local YAML. Scratch notes always come from the tack:hibana list
// when Redis is enabled.
func (s *Store) LoadPlan() (*model.Plan, error) {
	s.pull()
	if s.RedisEnabled() {
		_, _ = s.Reconcile()
	}

	plan, err := s.loadPlanRedisOrYAML()
	if err != nil {
		return nil, err
	}

	if s.RedisEnabled() {
		if notes, err := s.fetchHibanaList(); err == nil && notes != nil {
			plan.Scratch = notes
		}
		// On Redis fetch success, mirror to disk so offline mode stays hydrated.
		_ = s.saveYAML(s.planPath(), plan)
	}
	return plan, nil
}

func (s *Store) loadPlanRedisOrYAML() (*model.Plan, error) {
	if s.RedisEnabled() {
		ctx, cancel := s.ctx()
		defer cancel()
		if val, ok, err := s.redis.Get(ctx, keyPlan); err == nil && ok {
			var plan model.Plan
			if err := json.Unmarshal([]byte(val), &plan); err == nil {
				return &plan, nil
			}
			// Bad JSON — fall through to YAML.
		}
	}
	var plan model.Plan
	if err := s.loadYAML(s.planPath(), &plan); err != nil {
		if os.IsNotExist(err) {
			return &model.Plan{}, nil
		}
		return nil, err
	}
	return &plan, nil
}

func (s *Store) fetchHibanaList() ([]model.ScratchNote, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	raw, err := s.redis.LRange(ctx, keyHibana, 0, -1)
	if err != nil {
		return nil, err
	}
	out := make([]model.ScratchNote, 0, len(raw))
	for _, s := range raw {
		var n model.ScratchNote
		if err := json.Unmarshal([]byte(s), &n); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// SavePlan writes the plan everywhere it belongs:
//   - local YAML (always, for offline reads and git sync)
//   - Redis tack:plan (without Scratch — that's the list's job) + rev bump
//   - Redis tack:hibana (full rewrite via DEL + RPUSH)
//
// Redis failures route the write into the local outbox for later replay.
func (s *Store) SavePlan(plan *model.Plan) error {
	if err := s.saveYAML(s.planPath(), plan); err != nil {
		return err
	}

	if s.RedisEnabled() {
		planCopy := *plan
		planCopy.Scratch = nil
		if blob, err := json.Marshal(&planCopy); err == nil {
			s.writeBlob(keyPlan, string(blob))
		}
		s.rewriteHibanaList(plan.Scratch)
	}

	s.commitAndPush(SyncMsg("update plan"), []string{"plan.yaml"})
	return nil
}

// writeBlob pushes a blob key to Redis, bumps its rev counter, and updates the
// local cache. On failure the op lands in the outbox for replay.
func (s *Store) writeBlob(key, payload string) {
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.redis.Set(ctx, key, payload); err != nil {
		_ = s.outbox.Append(OutboxOp{TS: time.Now(), Op: "set", Key: key, Payload: payload})
		fmt.Fprintf(os.Stderr, "warn: redis set %s (buffered): %s\n", key, err)
		return
	}
	newRev, err := s.redis.Incr(ctx, revKeyFor(key))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: redis incr %s: %s\n", revKeyFor(key), err)
		return
	}
	_ = s.syncState.SetRev(key, newRev)
}

// rewriteHibanaList replaces the Redis list with the full current Scratch
// slice. Used on TUI saves where reorder/delete semantics require an
// authoritative overwrite. The --hibana CLI path uses AddHibana instead, which
// is append-only and safe against concurrent writes from other devices.
func (s *Store) rewriteHibanaList(notes []model.ScratchNote) {
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.redis.Del(ctx, keyHibana); err != nil {
		fmt.Fprintf(os.Stderr, "warn: redis del %s: %s\n", keyHibana, err)
		return
	}
	if len(notes) == 0 {
		return
	}
	vals := make([]string, 0, len(notes))
	for _, n := range notes {
		b, err := json.Marshal(n)
		if err != nil {
			continue
		}
		vals = append(vals, string(b))
	}
	if err := s.redis.RPush(ctx, keyHibana, vals...); err != nil {
		fmt.Fprintf(os.Stderr, "warn: redis rpush %s: %s\n", keyHibana, err)
	}
}

// AddHibana appends a single scratch note using RPUSH (atomic across devices)
// and mirrors the append to the local plan.yaml. Prefer this over the
// LoadPlan/append/SavePlan dance for CLI quick-note entry.
func (s *Store) AddHibana(note model.ScratchNote) error {
	if s.RedisEnabled() {
		if b, err := json.Marshal(note); err == nil {
			ctx, cancel := s.ctx()
			if err := s.redis.RPush(ctx, keyHibana, string(b)); err != nil {
				_ = s.outbox.Append(OutboxOp{TS: time.Now(), Op: "rpush", Key: keyHibana, Payload: string(b)})
				fmt.Fprintf(os.Stderr, "warn: redis rpush %s (buffered): %s\n", keyHibana, err)
			}
			cancel()
		}
	}

	// Mirror to local YAML so offline reads still see the note. We load from
	// disk (not Redis) to avoid the round-trip cost on the hot path.
	var plan model.Plan
	if err := s.loadYAML(s.planPath(), &plan); err != nil && !os.IsNotExist(err) {
		return err
	}
	plan.Scratch = append(plan.Scratch, note)
	if err := s.saveYAML(s.planPath(), &plan); err != nil {
		return err
	}
	s.commitAndPush(SyncMsg("hibana"), []string{"plan.yaml"})
	return nil
}

// Annotations

func (s *Store) LoadAnnotations() (*model.Annotations, error) {
	s.pull()
	if s.RedisEnabled() {
		_, _ = s.Reconcile()
	}

	if s.RedisEnabled() {
		ctx, cancel := s.ctx()
		val, ok, err := s.redis.Get(ctx, keyAnnotations)
		cancel()
		if err == nil && ok {
			var ann model.Annotations
			if err := json.Unmarshal([]byte(val), &ann); err == nil {
				_ = s.saveYAML(s.annotationsPath(), &ann)
				return &ann, nil
			}
		}
	}

	var ann model.Annotations
	if err := s.loadYAML(s.annotationsPath(), &ann); err != nil {
		if os.IsNotExist(err) {
			return &model.Annotations{}, nil
		}
		return nil, err
	}
	return &ann, nil
}

func (s *Store) SaveAnnotations(ann *model.Annotations) error {
	if err := s.saveYAML(s.annotationsPath(), ann); err != nil {
		return err
	}
	if s.RedisEnabled() {
		if blob, err := json.Marshal(ann); err == nil {
			s.writeBlob(keyAnnotations, string(blob))
		}
	}
	s.commitAndPush(SyncMsg("update annotations"), []string{"annotations.yaml"})
	return nil
}

// GetAnnotation returns the annotation for a specific issue, or nil.
func (s *Store) GetAnnotation(ann *model.Annotations, issueNum int) *model.Annotation {
	for i := range ann.Items {
		if ann.Items[i].IssueNum == issueNum {
			return &ann.Items[i]
		}
	}
	return nil
}

// Rollover archives done items from previous days and keeps undone items
// with their original CreatedAt so the UI can show overdue signals.
func (s *Store) Rollover(plan *model.Plan) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var kept []model.TodoItem
	for _, item := range plan.Today {
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		itemDate := time.Date(item.CreatedAt.Year(), item.CreatedAt.Month(), item.CreatedAt.Day(), 0, 0, 0, 0, item.CreatedAt.Location())
		if item.Done && itemDate.Before(today) {
			plan.Completed = append(plan.Completed, item)
		} else {
			kept = append(kept, item)
		}
	}
	plan.Today = kept
}

// Usage log

func (s *Store) usagePath() string { return filepath.Join(s.dir, "usage.log") }
func (s *Store) recapsDir() string { return filepath.Join(s.dir, "recaps") }

// UsageEntry is the append-only record pushed to tack:usage.
type UsageEntry struct {
	TS  time.Time `json:"ts"`
	Cmd string    `json:"cmd"`
}

func (s *Store) LogUsage(command string) {
	entry := UsageEntry{TS: time.Now(), Cmd: command}

	// Local file mirror (tab-separated, for offline debugging and migration).
	if f, err := os.OpenFile(s.usagePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		fmt.Fprintf(f, "%s\t%s\n", entry.TS.Format(time.RFC3339), command)
		f.Close()
	}

	if !s.RedisEnabled() {
		return
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.redis.RPush(ctx, keyUsage, string(b)); err != nil {
		_ = s.outbox.Append(OutboxOp{TS: entry.TS, Op: "rpush", Key: keyUsage, Payload: string(b)})
	}
}

func (s *Store) LoadUsageStats() (map[string]int, error) {
	if s.RedisEnabled() {
		ctx, cancel := s.ctx()
		raw, err := s.redis.LRange(ctx, keyUsage, 0, -1)
		cancel()
		if err == nil {
			stats := make(map[string]int)
			for _, line := range raw {
				var e UsageEntry
				if err := json.Unmarshal([]byte(line), &e); err == nil && e.Cmd != "" {
					stats[e.Cmd]++
				}
			}
			return stats, nil
		}
	}
	data, err := os.ReadFile(s.usagePath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int{}, nil
		}
		return nil, err
	}
	stats := make(map[string]int)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			stats[parts[1]]++
		}
	}
	return stats, nil
}

// Recaps

func (s *Store) SaveRecap(name string, content string) error {
	dir := s.recapsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	relPath := filepath.Join("recaps", name+".md")
	if err := os.WriteFile(filepath.Join(s.dir, relPath), []byte(content), 0644); err != nil {
		return err
	}

	if s.RedisEnabled() {
		ctx, cancel := s.ctx()
		defer cancel()
		key := recapKey(name)
		if err := s.redis.Set(ctx, key, content); err != nil {
			_ = s.outbox.Append(OutboxOp{TS: time.Now(), Op: "set", Key: key, Payload: content})
		} else {
			if err := s.redis.SAdd(ctx, keyRecapsIndex, name); err != nil {
				_ = s.outbox.Append(OutboxOp{TS: time.Now(), Op: "sadd", Key: keyRecapsIndex, Payload: name})
			}
		}
	}

	s.commitAndPush(SyncMsg("save recap "+name), []string{relPath})
	return nil
}

// LoadRecap returns the markdown body for a single recap, preferring Redis.
func (s *Store) LoadRecap(name string) (string, error) {
	if s.RedisEnabled() {
		ctx, cancel := s.ctx()
		val, ok, err := s.redis.Get(ctx, recapKey(name))
		cancel()
		if err == nil && ok {
			return val, nil
		}
	}
	b, err := os.ReadFile(filepath.Join(s.recapsDir(), name+".md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ListRecaps returns the set of recap names, preferring the Redis index.
func (s *Store) ListRecaps() ([]string, error) {
	if s.RedisEnabled() {
		ctx, cancel := s.ctx()
		members, err := s.redis.SMembers(ctx, keyRecapsIndex)
		cancel()
		if err == nil {
			return members, nil
		}
	}
	entries, err := os.ReadDir(s.recapsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".md") {
			out = append(out, strings.TrimSuffix(n, ".md"))
		}
	}
	return out, nil
}

// sync helpers — delegate to the optional Syncer, silently ignoring errors.

func (s *Store) pull() {
	if s.syncer != nil {
		_ = s.syncer.Pull()
	}
}

func (s *Store) commitAndPush(msg string, paths []string) {
	if s.syncer != nil {
		_ = s.syncer.CommitAndPush(msg, paths)
	}
}

// yaml helpers

func (s *Store) loadYAML(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, v)
}

func (s *Store) saveYAML(path string, v interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
