package planning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// syncState tracks the last Redis revision this client saw for each blob key.
// Used to detect conflicts on reconnect: if our cached rev is behind the
// current remote rev AND we have a pending local write for the same key,
// another device wrote while we were offline.
type syncState struct {
	mu   sync.Mutex
	path string
	data syncData
}

type syncData struct {
	Revs map[string]int64 `json:"revs"`
}

func newSyncState(dir string) *syncState {
	ss := &syncState{path: filepath.Join(dir, ".sync_state.json"), data: syncData{Revs: map[string]int64{}}}
	ss.load()
	return ss
}

func (s *syncState) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var d syncData
	if err := json.Unmarshal(b, &d); err != nil {
		return
	}
	if d.Revs == nil {
		d.Revs = map[string]int64{}
	}
	s.data = d
}

func (s *syncState) Rev(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Revs[key]
}

func (s *syncState) SetRev(key string, rev int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Revs[key] = rev
	return s.persistLocked()
}

func (s *syncState) persistLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// conflictsDir holds per-key pending conflict files, one JSON per unresolved
// conflict. Saving here persists the conflict until the user resolves it via
// `tack --resolve-conflicts`.
func conflictsDir(dir string) string { return filepath.Join(dir, ".conflicts") }
