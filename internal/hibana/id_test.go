package hibana

import (
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestNewIDLength(t *testing.T) {
	id := NewID()
	if len(id) != 26 {
		t.Fatalf("expected 26 chars, got %d (%q)", len(id), id)
	}
	if !ValidID(string(id)) {
		t.Fatalf("ValidID rejected freshly-minted id %q", id)
	}
}

func TestNewIDAlphabet(t *testing.T) {
	id := NewID()
	for _, c := range id {
		if !strings.ContainsRune(idAlphabet, c) {
			t.Fatalf("char %q not in alphabet", c)
		}
	}
}

// TestNewIDUniqueAcrossManyCalls verifies that we don't collide even when
// minting many IDs as fast as possible — the same-millisecond branch must
// keep monotonically increasing the random tail without repeating.
func TestNewIDUniqueAcrossManyCalls(t *testing.T) {
	const n = 10_000
	seen := make(map[ID]struct{}, n)
	for i := 0; i < n; i++ {
		id := NewID()
		if _, dup := seen[id]; dup {
			t.Fatalf("collision on %dth id: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestNewIDSortable: ids minted in order should sort in the same order.
func TestNewIDSortable(t *testing.T) {
	const n = 1000
	ids := make([]ID, n)
	for i := range ids {
		ids[i] = NewID()
	}
	sorted := make([]ID, n)
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("position %d: minted=%s sorted=%s", i, ids[i], sorted[i])
		}
	}
}

// TestNewIDConcurrent: concurrent NewID callers must not collide.
func TestNewIDConcurrent(t *testing.T) {
	const goroutines = 32
	const perG = 500
	var mu sync.Mutex
	seen := map[ID]struct{}{}
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]ID, perG)
			for i := range local {
				local[i] = NewID()
			}
			mu.Lock()
			defer mu.Unlock()
			for _, id := range local {
				if _, dup := seen[id]; dup {
					t.Errorf("duplicate id under concurrency: %s", id)
				}
				seen[id] = struct{}{}
			}
		}()
	}
	wg.Wait()
}

func TestValidID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"01HXX1234567890ABCDEFGHJKM", true},
		{"", false},
		{"too-short", false},
		{strings.Repeat("A", 27), false},
	}
	for _, c := range cases {
		if got := ValidID(c.in); got != c.want {
			t.Errorf("ValidID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
