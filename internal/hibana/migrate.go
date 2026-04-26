package hibana

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateSource describes where the migration tool should pull existing
// notes from. All fields are optional — missing sources are skipped.
type MigrateSource struct {
	// PlanYAMLPath: path to the legacy plan.yaml whose `scratch:` entries
	// should be imported. Typically ~/Documents/leisure_vault/tack/plan.yaml.
	PlanYAMLPath string

	// LegacyHashKey / LegacyListKey: Redis keys for the previous schema. If
	// either is set and Backend is enabled, we'll pull from Redis too.
	LegacyListKey string

	// Backend: optional Redis backend used for legacy LIST reads. The new
	// hibana keys are written via the Store's own backend.
	LegacyBackend interface {
		Enabled() bool
		LRange(ctx context.Context, key string, start, stop int) ([]string, error)
	}

	// GitRepoPath / GitCommit: when set, we'll also `git show
	// <commit>:<relpath>` to recover scratch entries that exist in history
	// but not in the working tree (e.g., the Corgi Cafe note destroyed by
	// commit d187a7f). RelPath defaults to "tack/plan.yaml".
	GitRepoPath string
	GitCommit   string
	GitRelPath  string
}

// MigrateReport describes what migration did.
type MigrateReport struct {
	FromYAML   int            // notes scraped from plan.yaml
	FromList   int            // notes scraped from Redis LIST
	FromGit    int            // notes recovered from a git commit
	Imported   int            // notes actually written (after dedup)
	Duplicates int            // notes skipped because already present
	NewNotes   []Note         // the imported notes, in their final form
	NoteByText map[string]Note // for callers that want to find a specific recovered note
}

// Migrate populates `dest` with notes scraped from the given sources. It is
// idempotent across runs: notes are deduplicated by (text, created_at)
// against both the source and the existing destination.
//
// Caller is responsible for creating `dest` (use Open). Migrate does NOT
// push to Redis — call dest.Sync after if you want immediate propagation.
func Migrate(dest *Store, src MigrateSource) (MigrateReport, error) {
	rep := MigrateReport{NoteByText: map[string]Note{}}

	// Existing notes in dest, keyed by (text, createdAt) so re-runs don't
	// duplicate. We can't use NoteID because legacy data has none.
	existing, err := dest.List()
	if err != nil {
		return rep, err
	}
	existKey := map[string]struct{}{}
	for _, n := range existing {
		existKey[migrateKey(n.Text, n.CreatedAt)] = struct{}{}
	}

	// Load all sources into a single de-duplicated slice.
	var candidates []legacyScratch
	if src.PlanYAMLPath != "" {
		notes, err := loadScratchFromYAML(src.PlanYAMLPath)
		if err != nil {
			return rep, fmt.Errorf("read %s: %w", src.PlanYAMLPath, err)
		}
		rep.FromYAML = len(notes)
		candidates = append(candidates, notes...)
	}
	if src.LegacyBackend != nil && src.LegacyBackend.Enabled() && src.LegacyListKey != "" {
		notes, err := loadScratchFromList(src.LegacyBackend, src.LegacyListKey)
		if err != nil {
			return rep, fmt.Errorf("read redis list %s: %w", src.LegacyListKey, err)
		}
		rep.FromList = len(notes)
		candidates = append(candidates, notes...)
	}
	if src.GitRepoPath != "" && src.GitCommit != "" {
		rel := src.GitRelPath
		if rel == "" {
			rel = "tack/plan.yaml"
		}
		notes, err := loadScratchFromGit(src.GitRepoPath, src.GitCommit, rel)
		if err != nil {
			// A missing commit/path is informational, not fatal: we still
			// want the rest of the migration to proceed.
			fmt.Fprintf(os.Stderr, "hibana: skipping git source %s@%s: %s\n", rel, src.GitCommit, err)
		} else {
			rep.FromGit = len(notes)
			candidates = append(candidates, notes...)
		}
	}

	// Dedup candidates against each other (intra-source) and against
	// destination (inter-run idempotency).
	seenInRun := map[string]struct{}{}
	deduped := candidates[:0]
	for _, c := range candidates {
		k := migrateKey(c.Text, c.CreatedAt)
		if _, dup := seenInRun[k]; dup {
			rep.Duplicates++
			continue
		}
		if _, dup := existKey[k]; dup {
			rep.Duplicates++
			continue
		}
		seenInRun[k] = struct{}{}
		deduped = append(deduped, c)
	}

	// Stable sort by created_at so the new file is reproducible.
	sort.SliceStable(deduped, func(i, j int) bool {
		return deduped[i].CreatedAt.Before(deduped[j].CreatedAt)
	})

	// Write to destination.
	for _, c := range deduped {
		updated := c.UpdatedAt
		if updated.IsZero() {
			updated = c.CreatedAt
		}
		n, err := dest.AddWithTimestamps(c.Text, c.CreatedAt, updated)
		if err != nil {
			return rep, fmt.Errorf("write note %q: %w", short(c.Text), err)
		}
		rep.Imported++
		rep.NewNotes = append(rep.NewNotes, n)
		rep.NoteByText[c.Text] = n
	}
	return rep, nil
}

// legacyScratch mirrors model.ScratchNote without importing the model
// package (avoids a circular dep risk and keeps hibana standalone).
type legacyScratch struct {
	Text      string    `yaml:"text" json:"text"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
}

type legacyPlan struct {
	Scratch []legacyScratch `yaml:"scratch"`
}

func loadScratchFromYAML(path string) ([]legacyScratch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p legacyPlan
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return p.Scratch, nil
}

func loadScratchFromList(be interface {
	LRange(ctx context.Context, key string, start, stop int) ([]string, error)
}, key string) ([]legacyScratch, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raws, err := be.LRange(ctx, key, 0, -1)
	if err != nil {
		return nil, err
	}
	out := make([]legacyScratch, 0, len(raws))
	for _, raw := range raws {
		var n legacyScratch
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

// loadScratchFromGit shells out to `git -C <repo> show <commit>:<path>` and
// parses the resulting YAML. Returns the scratch entries from that
// historical version of the file.
func loadScratchFromGit(repo, commit, relPath string) ([]legacyScratch, error) {
	repo = expandHome(repo)
	cmd := exec.Command("git", "-C", repo, "show", fmt.Sprintf("%s:%s", commit, relPath))
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var p legacyPlan
	if err := yaml.Unmarshal(out, &p); err != nil {
		return nil, err
	}
	return p.Scratch, nil
}

// migrateKey is the dedup key — text + createdAt millisecond. Microsecond
// precision is dropped because YAML round-tripping in some sources strips
// it; comparing by text alone risks merging genuine duplicates with the
// same text taken at different times.
func migrateKey(text string, ts time.Time) string {
	return strings.TrimSpace(text) + "\x00" + ts.UTC().Format(time.RFC3339)
}

func short(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// MigrateLegacyBackendAdapter wraps any LRange-providing backend so it
// satisfies MigrateSource.LegacyBackend. Useful when callers have a
// pre-existing planning.Store with its own restBackend; they pass that
// in via this adapter.
type MigrateLegacyBackendAdapter struct {
	enabled bool
	lrange  func(ctx context.Context, key string, start, stop int) ([]string, error)
}

func NewMigrateLegacyBackendAdapter(enabled bool, lrange func(ctx context.Context, key string, start, stop int) ([]string, error)) *MigrateLegacyBackendAdapter {
	return &MigrateLegacyBackendAdapter{enabled: enabled, lrange: lrange}
}

func (a *MigrateLegacyBackendAdapter) Enabled() bool { return a.enabled }
func (a *MigrateLegacyBackendAdapter) LRange(ctx context.Context, key string, start, stop int) ([]string, error) {
	return a.lrange(ctx, key, start, stop)
}

// Sanity helpers used by the CLI.

// LegacyDefaults returns the canonical legacy locations: the last vault
// path I used, plus the Redis LIST key. Only used as flag defaults.
func LegacyDefaults() (yamlPath string, listKey string) {
	home, _ := os.UserHomeDir()
	yamlPath = filepath.Join(home, "Documents/leisure_vault/tack/plan.yaml")
	listKey = "tack:hibana"
	return
}
