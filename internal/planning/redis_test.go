package planning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autumnust/tack/internal/model"
	"gopkg.in/yaml.v3"
)

// fakeBackend is an in-memory redisBackend for tests. It supports error
// injection via failNext so we can exercise offline-fallback paths.
type fakeBackend struct {
	mu       sync.Mutex
	kv       map[string]string
	lists    map[string][]string
	failNext error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{kv: map[string]string{}, lists: map[string][]string{}}
}

func (f *fakeBackend) Enabled() bool { return true }

func (f *fakeBackend) takeFail() error {
	if f.failNext != nil {
		e := f.failNext
		f.failNext = nil
		return e
	}
	return nil
}

func (f *fakeBackend) Get(_ context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFail(); err != nil {
		return "", false, err
	}
	v, ok := f.kv[key]
	return v, ok, nil
}

func (f *fakeBackend) Set(_ context.Context, key, val string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFail(); err != nil {
		return err
	}
	f.kv[key] = val
	return nil
}

func (f *fakeBackend) Del(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFail(); err != nil {
		return err
	}
	delete(f.kv, key)
	delete(f.lists, key)
	return nil
}

func (f *fakeBackend) RPush(_ context.Context, key string, vals ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFail(); err != nil {
		return err
	}
	f.lists[key] = append(f.lists[key], vals...)
	return nil
}

func (f *fakeBackend) LRange(_ context.Context, key string, start, stop int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFail(); err != nil {
		return nil, err
	}
	list := f.lists[key]
	if stop == -1 || stop >= len(list) {
		stop = len(list) - 1
	}
	if start < 0 || start > stop {
		return []string{}, nil
	}
	out := make([]string, stop-start+1)
	copy(out, list[start:stop+1])
	return out, nil
}

func storeWithFake(t *testing.T) (*Store, *fakeBackend) {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := newFakeBackend()
	s.setBackend(fb)
	return s, fb
}

func TestSavePlan_WritesRedisAndLocal(t *testing.T) {
	s, fb := storeWithFake(t)
	plan := &model.Plan{
		Today:   []model.TodoItem{{Text: "a", CreatedAt: time.Now().Truncate(time.Second)}},
		Scratch: []model.ScratchNote{{Text: "n1", CreatedAt: time.Now().Truncate(time.Second)}},
	}
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}

	// Redis tack:plan must not include Scratch.
	raw, ok := fb.kv[keyPlan]
	if !ok {
		t.Fatal("expected tack:plan in redis")
	}
	if strings.Contains(raw, "n1") {
		t.Errorf("tack:plan should not carry scratch text: %s", raw)
	}

	// Scratch lives in tack:hibana list.
	if len(fb.lists[keyHibana]) != 1 {
		t.Errorf("expected 1 hibana entry, got %d", len(fb.lists[keyHibana]))
	}

	// Local yaml still has everything.
	data, err := os.ReadFile(filepath.Join(s.Dir(), "plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "n1") {
		t.Errorf("local yaml missing scratch: %s", data)
	}
}

func TestLoadPlan_PrefersRedis_OverlaysHibana(t *testing.T) {
	s, fb := storeWithFake(t)

	// Seed Redis directly with a plan (no scratch) and a hibana list.
	plan := &model.Plan{Today: []model.TodoItem{{Text: "from-cloud"}}}
	blob, _ := json.Marshal(plan)
	fb.kv[keyPlan] = string(blob)
	n := model.ScratchNote{Text: "cloud-note", CreatedAt: time.Now().Truncate(time.Second)}
	nb, _ := json.Marshal(n)
	fb.lists[keyHibana] = []string{string(nb)}

	// Write a conflicting local yaml — Redis should win.
	yblob, _ := yaml.Marshal(&model.Plan{Today: []model.TodoItem{{Text: "stale-local"}}})
	_ = os.WriteFile(filepath.Join(s.Dir(), "plan.yaml"), yblob, 0644)

	got, err := s.LoadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Today) != 1 || got.Today[0].Text != "from-cloud" {
		t.Errorf("expected cloud plan, got %+v", got.Today)
	}
	if len(got.Scratch) != 1 || got.Scratch[0].Text != "cloud-note" {
		t.Errorf("expected hibana overlay, got %+v", got.Scratch)
	}

	// Redis load must mirror to local disk.
	data, _ := os.ReadFile(filepath.Join(s.Dir(), "plan.yaml"))
	if !strings.Contains(string(data), "from-cloud") {
		t.Errorf("local yaml not refreshed after Redis load: %s", data)
	}
}

func TestLoadPlan_FallsBackToYAMLOnRedisError(t *testing.T) {
	s, fb := storeWithFake(t)

	// Seed local yaml.
	yblob, _ := yaml.Marshal(&model.Plan{Today: []model.TodoItem{{Text: "offline"}}})
	_ = os.WriteFile(filepath.Join(s.Dir(), "plan.yaml"), yblob, 0644)

	// Inject error on Get.
	fb.failNext = errors.New("network down")

	got, err := s.LoadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Today) != 1 || got.Today[0].Text != "offline" {
		t.Errorf("expected yaml fallback, got %+v", got.Today)
	}
}

func TestSavePlan_RedisFailureDoesNotError(t *testing.T) {
	s, fb := storeWithFake(t)
	fb.failNext = errors.New("upstash exploded")

	plan := &model.Plan{Today: []model.TodoItem{{Text: "x"}}}
	if err := s.SavePlan(plan); err != nil {
		t.Fatalf("SavePlan should tolerate redis failure, got: %s", err)
	}
	// Local yaml must still exist.
	if _, err := os.Stat(filepath.Join(s.Dir(), "plan.yaml")); err != nil {
		t.Errorf("local yaml missing after save: %s", err)
	}
}

func TestAddHibana_AppendsToList_AndLocal(t *testing.T) {
	s, fb := storeWithFake(t)

	n1 := model.ScratchNote{Text: "one", CreatedAt: time.Now().Truncate(time.Second)}
	n2 := model.ScratchNote{Text: "two", CreatedAt: time.Now().Add(time.Second).Truncate(time.Second)}

	if err := s.AddHibana(n1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHibana(n2); err != nil {
		t.Fatal(err)
	}

	if got := len(fb.lists[keyHibana]); got != 2 {
		t.Fatalf("expected 2 hibana entries, got %d", got)
	}

	// Order preserved and no tack:plan rewrite.
	if _, ok := fb.kv[keyPlan]; ok {
		t.Error("AddHibana must not touch tack:plan")
	}

	// Local yaml has both notes.
	data, err := os.ReadFile(filepath.Join(s.Dir(), "plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "one") || !strings.Contains(string(data), "two") {
		t.Errorf("local yaml missing notes: %s", data)
	}
}

func TestAnnotations_RedisRoundtrip(t *testing.T) {
	s, fb := storeWithFake(t)
	ann := &model.Annotations{Items: []model.Annotation{{IssueNum: 7, Notes: []string{"x"}}}}
	if err := s.SaveAnnotations(ann); err != nil {
		t.Fatal(err)
	}
	if _, ok := fb.kv[keyAnnotations]; !ok {
		t.Fatal("expected tack:annotations in redis")
	}
	// Corrupt local yaml to prove Redis is preferred.
	_ = os.WriteFile(filepath.Join(s.Dir(), "annotations.yaml"), []byte("items: []\n"), 0644)
	got, err := s.LoadAnnotations()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].IssueNum != 7 {
		t.Errorf("expected cloud annotations, got %+v", got.Items)
	}
}

func TestMigrateLeisureVault(t *testing.T) {
	src := t.TempDir()
	// Seed source dir with plan + annotations.
	plan := &model.Plan{
		Today: []model.TodoItem{{Text: "legacy todo"}},
		Scratch: []model.ScratchNote{
			{Text: "a", CreatedAt: time.Now().Truncate(time.Second)},
			{Text: "b", CreatedAt: time.Now().Add(time.Second).Truncate(time.Second)},
		},
	}
	pb, _ := yaml.Marshal(plan)
	_ = os.WriteFile(filepath.Join(src, "plan.yaml"), pb, 0644)

	ann := &model.Annotations{Items: []model.Annotation{{IssueNum: 1, Notes: []string{"n"}}}}
	ab, _ := yaml.Marshal(ann)
	_ = os.WriteFile(filepath.Join(src, "annotations.yaml"), ab, 0644)

	s, fb := storeWithFake(t)
	res, err := s.MigrateLeisureVault(src, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlanItems != 1 || res.AnnotationRows != 1 || res.HibanaNotes != 2 {
		t.Errorf("unexpected result: %+v", res)
	}
	if _, ok := fb.kv[keyPlan]; !ok {
		t.Error("tack:plan not set")
	}
	if strings.Contains(fb.kv[keyPlan], "\"text\":\"a\"") {
		t.Error("tack:plan should not carry scratch after migration")
	}
	if len(fb.lists[keyHibana]) != 2 {
		t.Errorf("expected 2 hibana notes, got %d", len(fb.lists[keyHibana]))
	}
	if _, ok := fb.kv[keyAnnotations]; !ok {
		t.Error("tack:annotations not set")
	}

	// Idempotency: re-running without --force should refuse.
	if _, err := s.MigrateLeisureVault(src, false); err == nil {
		t.Error("expected refusal on existing tack:plan")
	}
	// With --force it should overwrite.
	if _, err := s.MigrateLeisureVault(src, true); err != nil {
		t.Errorf("--force should overwrite, got: %s", err)
	}
}

func TestMigrateLeisureVault_RequiresBackend(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MigrateLeisureVault(t.TempDir(), false); err == nil {
		t.Error("expected error when redis not configured")
	}
}
