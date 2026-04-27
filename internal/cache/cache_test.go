package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autumnust/tack/internal/model"
)

func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func testProject() *model.Project {
	return &model.Project{
		ID:    "proj-1",
		Title: "Test Project",
		Items: []model.ProjectItem{
			{Number: 100, Title: "Epic"},
			{Number: 101, Title: "Child", Parent: &model.ParentRef{Number: 100, Title: "Epic"}},
			{Number: 200, Title: "Standalone"},
		},
	}
}

func TestCachePath_UsesHomeAndHash(t *testing.T) {
	home := withTempHome(t)

	path := cachePath("https://github.com/orgs/test/projects/1")
	if filepath.Dir(path) != filepath.Join(home, cacheDir) {
		t.Fatalf("cachePath() dir = %q, want under %q", filepath.Dir(path), filepath.Join(home, cacheDir))
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("cachePath() = %q, want .json suffix", path)
	}
}

func TestSaveAndLoadAny_RoundTripAllItems(t *testing.T) {
	withTempHome(t)
	projectURL := "https://github.com/orgs/test/projects/1"
	project := testProject()

	if err := Save(projectURL, project, nil); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cached := LoadAny(projectURL, nil)
	if cached == nil {
		t.Fatal("LoadAny() returned nil")
	}
	if cached.Project == nil {
		t.Fatal("LoadAny().Project returned nil")
	}
	if len(cached.Items) != 3 {
		t.Fatalf("LoadAny().Items len = %d, want 3", len(cached.Items))
	}
	if cached.Project.Title != "Test Project" {
		t.Fatalf("LoadAny().Project.Title = %q, want %q", cached.Project.Title, "Test Project")
	}
	if len(cached.Project.Items) != 0 {
		t.Fatalf("cached project metadata should not embed items, got %d", len(cached.Project.Items))
	}
}

func TestSave_FiltersToFocusAndChildren(t *testing.T) {
	withTempHome(t)
	projectURL := "https://github.com/orgs/test/projects/1"
	project := testProject()

	focus := map[int]bool{100: true}
	if err := Save(projectURL, project, focus); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cached := LoadAny(projectURL, focus)
	if cached == nil {
		t.Fatal("LoadAny() returned nil")
	}
	if len(cached.Items) != 2 {
		t.Fatalf("LoadAny().Items len = %d, want 2", len(cached.Items))
	}
	if cached.Items[0].Number != 100 || cached.Items[1].Number != 101 {
		t.Fatalf("filtered items = [%d, %d], want [100, 101]", cached.Items[0].Number, cached.Items[1].Number)
	}
}

func TestLoad_ExpiredCacheReturnsNil(t *testing.T) {
	home := withTempHome(t)
	projectURL := "https://github.com/orgs/test/projects/1"
	path := cachePath(projectURL)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	_ = home

	old := CachedProject{
		FetchedAt: time.Now().Add(-2 * time.Hour),
		Project:   &model.Project{ID: "proj-1"},
	}
	data := []byte(`{"fetched_at":"` + old.FetchedAt.Format(time.RFC3339Nano) + `","project":{"ID":"proj-1"},"items":[]}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if got := Load(projectURL, nil, time.Hour); got != nil {
		t.Fatalf("Load() = %#v, want nil for expired cache", got)
	}
}

func TestIsFresh(t *testing.T) {
	withTempHome(t)
	projectURL := "https://github.com/orgs/test/projects/1"
	project := testProject()

	if IsFresh(projectURL, nil, time.Hour) {
		t.Fatal("IsFresh() should be false before cache exists")
	}
	if err := Save(projectURL, project, nil); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !IsFresh(projectURL, nil, time.Hour) {
		t.Fatal("IsFresh() should be true for a newly written cache")
	}
}

// TestSave_StoresFocusHash and TestLoadAny_RejectsMismatchedFocus together
// verify the bug fix from issue #2: a cache written under one focus must
// not be returned to a caller running with a different focus, because the
// items list was filtered against the *write-time* focus and would be
// silently incomplete for the new focus.
func TestSave_StoresFocusHash(t *testing.T) {
	withTempHome(t)
	url := "https://github.com/orgs/test/projects/1"
	project := testProject()

	if err := Save(url, project, map[int]bool{100: true}); err != nil {
		t.Fatal(err)
	}
	cached := LoadAny(url, map[int]bool{100: true})
	if cached == nil {
		t.Fatal("LoadAny with matching focus returned nil")
	}
	if cached.FocusHash == "" {
		t.Errorf("FocusHash should be non-empty when focus is non-empty")
	}
}

func TestLoadAny_RejectsMismatchedFocus(t *testing.T) {
	withTempHome(t)
	url := "https://github.com/orgs/test/projects/1"
	project := testProject()

	// Save under focus = {100}.
	if err := Save(url, project, map[int]bool{100: true}); err != nil {
		t.Fatal(err)
	}

	// Load under a *different* focus = {200}. The cache was filtered against
	// {100} so items[] is missing 200's children even though they exist in
	// the project. We must NOT return the stale cache.
	if got := LoadAny(url, map[int]bool{200: true}); got != nil {
		t.Fatalf("LoadAny under mismatched focus should return nil, got %d items", len(got.Items))
	}

	// Load under the original focus {100} still works.
	if got := LoadAny(url, map[int]bool{100: true}); got == nil {
		t.Fatal("LoadAny under matching focus returned nil")
	}
}

func TestLoadAny_EmptyFocusHashRoundTrip(t *testing.T) {
	withTempHome(t)
	url := "https://github.com/orgs/test/projects/1"
	project := testProject()

	// nil focus → "no filter" cache, hash stored as empty string.
	if err := Save(url, project, nil); err != nil {
		t.Fatal(err)
	}
	if got := LoadAny(url, nil); got == nil {
		t.Fatal("nil focus round-trip failed")
	}
	// nil and empty-map should both be treated as "no filter" — same hash.
	if got := LoadAny(url, map[int]bool{}); got == nil {
		t.Fatal("empty-map focus should match nil-focus cache")
	}
	// Asking with a real focus against a no-filter cache: hash differs,
	// reject. (Conservative: no-filter cache has all items, so could be
	// considered valid for any focus, but the hashes don't match and we
	// don't want surprising returns.)
	if got := LoadAny(url, map[int]bool{100: true}); got != nil {
		t.Errorf("focused LoadAny against unfiltered cache should miss, got %d items", len(got.Items))
	}
}

func TestLoad_ExpiredAndFocusBothInvalidate(t *testing.T) {
	home := withTempHome(t)
	_ = home
	url := "https://github.com/orgs/test/projects/1"
	project := testProject()

	// Save under one focus, ample freshness.
	if err := Save(url, project, map[int]bool{100: true}); err != nil {
		t.Fatal(err)
	}

	// Same focus, ample maxAge → returned.
	if got := Load(url, map[int]bool{100: true}, time.Hour); got == nil {
		t.Fatal("Load with matching focus and ample age should succeed")
	}
	// Different focus → nil even though age is fine.
	if got := Load(url, map[int]bool{200: true}, time.Hour); got != nil {
		t.Errorf("Load with mismatched focus must return nil")
	}
}
