package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/standup-kanban/standup-kanban/internal/model"
)

const cacheDir = ".cache/standup-kanban"

type CachedProject struct {
	FetchedAt time.Time          `json:"fetched_at"`
	Project   *model.Project     `json:"project"`
	Items     []model.ProjectItem `json:"items"`
}

func cachePath(projectURL string) string {
	home, _ := os.UserHomeDir()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(projectURL)))[:12]
	return filepath.Join(home, cacheDir, hash+".json")
}

// Load returns the cached project data, or nil if no cache or expired.
func Load(projectURL string, maxAge time.Duration) *CachedProject {
	path := cachePath(projectURL)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cached CachedProject
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil
	}
	if time.Since(cached.FetchedAt) > maxAge {
		return nil
	}
	return &cached
}

// LoadAny returns cached data regardless of age (for instant startup).
// Returns nil only if no cache file exists.
func LoadAny(projectURL string) *CachedProject {
	path := cachePath(projectURL)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cached CachedProject
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil
	}
	return &cached
}

// Save writes project data to the cache, filtered to only focused items.
// focusSet is the merged set of all focus issue numbers (global + per-person).
// If nil, all items are cached.
func Save(projectURL string, project *model.Project, focusSet map[int]bool) error {
	path := cachePath(projectURL)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	items := project.Items
	if len(focusSet) > 0 {
		var filtered []model.ProjectItem
		for _, item := range items {
			if focusSet[item.Number] {
				filtered = append(filtered, item)
			} else if item.Parent != nil && focusSet[item.Parent.Number] {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	// Cache project metadata without items (items stored separately after filtering)
	projCopy := *project
	projCopy.Items = nil
	cached := CachedProject{
		FetchedAt: time.Now(),
		Project:   &projCopy,
		Items:     items,
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// IsFresh returns true if the cache is younger than maxAge.
func IsFresh(projectURL string, maxAge time.Duration) bool {
	cached := LoadAny(projectURL)
	if cached == nil {
		return false
	}
	return time.Since(cached.FetchedAt) <= maxAge
}
