package hibana

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const samplePlanYAML = `scratch:
    - text: "first note"
      created_at: 2026-04-01T10:00:00Z
    - text: "second note"
      created_at: 2026-04-02T10:00:00Z
      updated_at: 2026-04-03T10:00:00Z
`

func TestMigrateFromYAMLOnly(t *testing.T) {
	yamlPath := filepath.Join(t.TempDir(), "plan.yaml")
	if err := os.WriteFile(yamlPath, []byte(samplePlanYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, _ := Open(t.TempDir(), NopBackend())
	rep, err := Migrate(dest, MigrateSource{PlanYAMLPath: yamlPath})
	if err != nil {
		t.Fatal(err)
	}
	if rep.FromYAML != 2 || rep.Imported != 2 {
		t.Errorf("counts: %+v", rep)
	}
	notes, _ := dest.List()
	if len(notes) != 2 {
		t.Fatalf("dest list: %+v", notes)
	}
	// Order: stable sort by created_at.
	if notes[0].Text != "first note" || notes[1].Text != "second note" {
		t.Errorf("order: %+v", notes)
	}
	if !notes[1].UpdatedAt.Equal(time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("UpdatedAt not preserved: %v", notes[1].UpdatedAt)
	}
}

// TestMigrateIdempotent: running migrate twice must not duplicate.
func TestMigrateIdempotent(t *testing.T) {
	yamlPath := filepath.Join(t.TempDir(), "plan.yaml")
	os.WriteFile(yamlPath, []byte(samplePlanYAML), 0o644)
	dest, _ := Open(t.TempDir(), NopBackend())
	if _, err := Migrate(dest, MigrateSource{PlanYAMLPath: yamlPath}); err != nil {
		t.Fatal(err)
	}
	rep2, err := Migrate(dest, MigrateSource{PlanYAMLPath: yamlPath})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Imported != 0 {
		t.Errorf("second run should import 0, got %d", rep2.Imported)
	}
	if rep2.Duplicates != 2 {
		t.Errorf("expected 2 duplicates skipped, got %d", rep2.Duplicates)
	}
	notes, _ := dest.List()
	if len(notes) != 2 {
		t.Errorf("dest list should still have 2, got %d", len(notes))
	}
}

// TestMigrateFromList: pull notes from a Redis-LIST source.
func TestMigrateFromList(t *testing.T) {
	notes := []legacyScratch{
		{Text: "from list A", CreatedAt: time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)},
		{Text: "from list B", CreatedAt: time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)},
	}
	rawList := make([]string, 0, len(notes))
	for _, n := range notes {
		b, _ := json.Marshal(n)
		rawList = append(rawList, string(b))
	}
	be := &fakeListBackend{lrange: rawList}
	dest, _ := Open(t.TempDir(), NopBackend())
	rep, err := Migrate(dest, MigrateSource{LegacyBackend: be, LegacyListKey: "tack:hibana"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.FromList != 2 || rep.Imported != 2 {
		t.Errorf("counts: %+v", rep)
	}
}

// TestMigrateMergesYAMLAndList: dedup across sources.
func TestMigrateMergesYAMLAndList(t *testing.T) {
	created := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
	yamlPath := filepath.Join(t.TempDir(), "plan.yaml")
	os.WriteFile(yamlPath, []byte(`scratch:
    - text: "shared"
      created_at: `+created.Format(time.RFC3339)+`
    - text: "yaml-only"
      created_at: 2026-04-10T10:00:00Z
`), 0o644)

	listNote := legacyScratch{Text: "shared", CreatedAt: created}
	other := legacyScratch{Text: "list-only", CreatedAt: time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)}
	bShared, _ := json.Marshal(listNote)
	bOther, _ := json.Marshal(other)
	be := &fakeListBackend{lrange: []string{string(bShared), string(bOther)}}

	dest, _ := Open(t.TempDir(), NopBackend())
	rep, err := Migrate(dest, MigrateSource{
		PlanYAMLPath:  yamlPath,
		LegacyBackend: be,
		LegacyListKey: "tack:hibana",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Shared appears in both → 1 dup. Imported = 3 unique.
	if rep.Imported != 3 {
		t.Errorf("Imported = %d, want 3", rep.Imported)
	}
	if rep.Duplicates != 1 {
		t.Errorf("Duplicates = %d, want 1", rep.Duplicates)
	}
}

// TestMigrateRecoversFromGitCommit: this is the Corgi Cafe scenario. We
// build a tiny git repo, commit a plan.yaml that contains a note, then
// remove the note in a later commit. Migration should pull the note from
// the older commit even though it's gone from HEAD.
func TestMigrateRecoversFromGitCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	cmd := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cmd("init", "--initial-branch=main")
	cmd("config", "user.email", "t@example.com")
	cmd("config", "user.name", "test")

	// Commit 1: file contains the doomed Corgi note.
	yamlDir := filepath.Join(repo, "tack")
	os.MkdirAll(yamlDir, 0o755)
	doomed := `scratch:
    - text: "kept note"
      created_at: 2026-04-20T10:00:00Z
    - text: "testing the offline mode written from Corgi Cafe"
      created_at: 2026-04-25T19:11:29Z
`
	os.WriteFile(filepath.Join(yamlDir, "plan.yaml"), []byte(doomed), 0o644)
	cmd("add", "tack/plan.yaml")
	cmd("commit", "-m", "include corgi note")

	// Capture the commit hash before we delete the note.
	hashCmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	hashOut, err := hashCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	corgiCommit := strings.TrimSpace(string(hashOut))

	// Commit 2: only the kept note remains in HEAD.
	survived := `scratch:
    - text: "kept note"
      created_at: 2026-04-20T10:00:00Z
`
	os.WriteFile(filepath.Join(yamlDir, "plan.yaml"), []byte(survived), 0o644)
	cmd("add", "tack/plan.yaml")
	cmd("commit", "-m", "drop corgi note")

	dest, _ := Open(t.TempDir(), NopBackend())
	rep, err := Migrate(dest, MigrateSource{
		PlanYAMLPath: filepath.Join(repo, "tack/plan.yaml"),
		GitRepoPath:  repo,
		GitCommit:    corgiCommit,
		GitRelPath:   "tack/plan.yaml",
	})
	if err != nil {
		t.Fatal(err)
	}
	// HEAD has 1 note. Git source has 2 (including Corgi). Dedup leaves
	// 1 + 1 unique = 2 imported, 1 duplicate.
	if rep.Imported != 2 {
		t.Errorf("Imported = %d, want 2", rep.Imported)
	}
	notes, _ := dest.List()
	var found bool
	for _, n := range notes {
		if strings.Contains(n.Text, "Corgi Cafe") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Corgi Cafe note not recovered; notes: %+v", notes)
	}
}

// TestMigrateMissingGitCommitIsNonFatal: an unreachable git source should
// not abort migration of the other sources.
func TestMigrateMissingGitCommitIsNonFatal(t *testing.T) {
	yamlPath := filepath.Join(t.TempDir(), "plan.yaml")
	os.WriteFile(yamlPath, []byte(samplePlanYAML), 0o644)
	dest, _ := Open(t.TempDir(), NopBackend())
	rep, err := Migrate(dest, MigrateSource{
		PlanYAMLPath: yamlPath,
		GitRepoPath:  t.TempDir(), // not a git repo
		GitCommit:    "deadbeef",
	})
	if err != nil {
		t.Fatalf("missing git source should not abort: %v", err)
	}
	if rep.Imported != 2 {
		t.Errorf("Imported = %d, want 2 from yaml", rep.Imported)
	}
}

// fakeListBackend implements MigrateSource.LegacyBackend.
type fakeListBackend struct {
	lrange []string
}

func (f *fakeListBackend) Enabled() bool { return true }
func (f *fakeListBackend) LRange(_ context.Context, _ string, _, _ int) ([]string, error) {
	return f.lrange, nil
}
