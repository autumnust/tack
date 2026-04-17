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

	cached := LoadAny(projectURL)
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

	if err := Save(projectURL, project, map[int]bool{100: true}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cached := LoadAny(projectURL)
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

	if got := Load(projectURL, time.Hour); got != nil {
		t.Fatalf("Load() = %#v, want nil for expired cache", got)
	}
}

func TestIsFresh(t *testing.T) {
	withTempHome(t)
	projectURL := "https://github.com/orgs/test/projects/1"
	project := testProject()

	if IsFresh(projectURL, time.Hour) {
		t.Fatal("IsFresh() should be false before cache exists")
	}
	if err := Save(projectURL, project, nil); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !IsFresh(projectURL, time.Hour) {
		t.Fatal("IsFresh() should be true for a newly written cache")
	}
}
