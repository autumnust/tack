package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/autumnust/tack/internal/model"
)

const cacheDir = ".cache/tack"

// CachedProject is the on-disk shape of the per-project cache.
//
// FocusHash captures which focus set the cached items were filtered against
// at write time. Save filters items to only those whose Number or Parent
// is in focusSet, so a cache written under one focus is missing items that
// would be in scope under a different focus. Load* compares the requested
// focus's hash against the stored one and returns nil on mismatch — that
// forces a fresh fetch when config focus changes.
type CachedProject struct {
	FetchedAt time.Time           `json:"fetched_at"`
	Project   *model.Project      `json:"project"`
	Items     []model.ProjectItem `json:"items"`
	FocusHash string              `json:"focus_hash,omitempty"`
}

func cachePath(projectURL string) string {
	home, _ := os.UserHomeDir()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(projectURL)))[:12]
	return filepath.Join(home, cacheDir, hash+".json")
}

// hashFocus produces a deterministic short string for a focus set. Empty/nil
// returns the empty string (matches "all items, no filter" caches).
func hashFocus(focusSet map[int]bool) string {
	if len(focusSet) == 0 {
		return ""
	}
	nums := make([]int, 0, len(focusSet))
	for n := range focusSet {
		if focusSet[n] {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	h := sha256.New()
	for _, n := range nums {
		fmt.Fprintf(h, "%d\n", n)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// readCacheFile reads the cache for projectURL. Returns nil if missing,
// unparseable, or filtered against a different focus set than requested.
// Pass nil/empty focusSet to skip the focus check (legacy callers).
func readCacheFile(projectURL string, focusSet map[int]bool, requireFocusMatch bool) *CachedProject {
	path := cachePath(projectURL)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cached CachedProject
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil
	}
	if requireFocusMatch && cached.FocusHash != hashFocus(focusSet) {
		return nil
	}
	return &cached
}

// Load returns the cached project data, or nil if no cache, expired, or
// filtered against a different focus set.
func Load(projectURL string, focusSet map[int]bool, maxAge time.Duration) *CachedProject {
	cached := readCacheFile(projectURL, focusSet, true)
	if cached == nil {
		return nil
	}
	if time.Since(cached.FetchedAt) > maxAge {
		return nil
	}
	return cached
}

// LoadAny returns cached data regardless of age, but still requires the
// focus hash to match (a cache filtered against the wrong focus would be
// silently incomplete — see the bug behind issue #2).
func LoadAny(projectURL string, focusSet map[int]bool) *CachedProject {
	return readCacheFile(projectURL, focusSet, true)
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

	projCopy := *project
	projCopy.Items = nil
	cached := CachedProject{
		FetchedAt: time.Now(),
		Project:   &projCopy,
		Items:     items,
		FocusHash: hashFocus(focusSet),
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// IsFresh returns true if the cache is younger than maxAge AND was written
// against the same focus set. A cache filtered against a different focus
// would render an incomplete board, so we treat that as "not fresh."
func IsFresh(projectURL string, focusSet map[int]bool, maxAge time.Duration) bool {
	cached := readCacheFile(projectURL, focusSet, true)
	if cached == nil {
		return false
	}
	return time.Since(cached.FetchedAt) <= maxAge
}
