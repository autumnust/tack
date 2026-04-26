package hibana

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// fakeBackend is an in-memory implementation of Backend for tests. It can
// be programmed to fail specific operations to exercise error paths.
type fakeBackend struct {
	mu      sync.Mutex
	hashes  map[string]map[string]string // key → field → value
	sets    map[string]map[string]struct{} // key → member → present
	failOn  map[string]int                 // command → remaining failures
	calls   []string                       // log of commands for assertion
	enabled bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		hashes:  map[string]map[string]string{},
		sets:    map[string]map[string]struct{}{},
		failOn:  map[string]int{},
		enabled: true,
	}
}

func (f *fakeBackend) Enabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enabled
}

func (f *fakeBackend) failOnce(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOn[cmd]++
}

func (f *fakeBackend) shouldFail(cmd string) bool {
	if n := f.failOn[cmd]; n > 0 {
		f.failOn[cmd] = n - 1
		return true
	}
	return false
}

func (f *fakeBackend) HSet(_ context.Context, key, field, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "HSET")
	if f.shouldFail("HSET") {
		return errors.New("fake: HSET failed")
	}
	if f.hashes[key] == nil {
		f.hashes[key] = map[string]string{}
	}
	f.hashes[key][field] = value
	return nil
}

func (f *fakeBackend) HDel(_ context.Context, key, field string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "HDEL")
	if f.shouldFail("HDEL") {
		return errors.New("fake: HDEL failed")
	}
	if h, ok := f.hashes[key]; ok {
		delete(h, field)
	}
	return nil
}

func (f *fakeBackend) HKeys(_ context.Context, key string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "HKEYS")
	if f.shouldFail("HKEYS") {
		return nil, errors.New("fake: HKEYS failed")
	}
	out := make([]string, 0, len(f.hashes[key]))
	for k := range f.hashes[key] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeBackend) HMGet(_ context.Context, key string, fields ...string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "HMGET")
	if f.shouldFail("HMGET") {
		return nil, errors.New("fake: HMGET failed")
	}
	out := map[string]string{}
	for _, fld := range fields {
		if v, ok := f.hashes[key][fld]; ok {
			out[fld] = v
		}
	}
	return out, nil
}

func (f *fakeBackend) SAdd(_ context.Context, key, member string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "SADD")
	if f.shouldFail("SADD") {
		return errors.New("fake: SADD failed")
	}
	if f.sets[key] == nil {
		f.sets[key] = map[string]struct{}{}
	}
	f.sets[key][member] = struct{}{}
	return nil
}

func (f *fakeBackend) SRem(_ context.Context, key, member string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "SREM")
	if f.shouldFail("SREM") {
		return errors.New("fake: SREM failed")
	}
	if s, ok := f.sets[key]; ok {
		delete(s, member)
	}
	return nil
}

func (f *fakeBackend) SMembers(_ context.Context, key string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "SMEMBERS")
	if f.shouldFail("SMEMBERS") {
		return nil, errors.New("fake: SMEMBERS failed")
	}
	out := make([]string, 0, len(f.sets[key]))
	for m := range f.sets[key] {
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

// helpers used by sync_test.go:

func (f *fakeBackend) hashLen(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hashes[key])
}

func (f *fakeBackend) graveLen(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sets[key])
}

func (f *fakeBackend) graveHas(key, member string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sets[key][member]
	return ok
}

func (f *fakeBackend) hashGet(key, field string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.hashes[key][field]
	return v, ok
}

// putHash pre-loads remote state for "another device already pushed" tests.
func (f *fakeBackend) putHash(key, field, val string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hashes[key] == nil {
		f.hashes[key] = map[string]string{}
	}
	f.hashes[key][field] = val
}

func (f *fakeBackend) putGrave(key, member string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sets[key] == nil {
		f.sets[key] = map[string]struct{}{}
	}
	f.sets[key][member] = struct{}{}
}
