package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autumnust/tack/internal/model"
	"gopkg.in/yaml.v3"
)

// Store manages reading/writing planning files from a configurable directory.
type Store struct {
	dir    string
	syncer Syncer
}

// NewStore creates a store rooted at dir. If dir is inside a git work tree,
// a GitSyncer is attached automatically. Use SetSyncer to override.
func NewStore(dir string) (*Store, error) {
	// Expand ~ to home dir
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, dir[1:])
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	if gs := NewGitSyncer(dir); gs != nil {
		s.syncer = gs
	}
	return s, nil
}

// SetSyncer overrides the auto-detected syncer (pass nil to disable sync).
func (s *Store) SetSyncer(syncer Syncer) { s.syncer = syncer }

func (s *Store) Dir() string { return s.dir }

// Plan

func (s *Store) planPath() string       { return filepath.Join(s.dir, "plan.yaml") }
func (s *Store) annotationsPath() string { return filepath.Join(s.dir, "annotations.yaml") }
func (s *Store) hibanaPath() string { return filepath.Join(s.dir, "hibana.md") }

func (s *Store) LoadPlan() (*model.Plan, error) {
	s.pull()

	var plan model.Plan
	if err := s.loadYAML(s.planPath(), &plan); err != nil {
		if os.IsNotExist(err) {
			return &model.Plan{}, nil
		}
		return nil, err
	}
	return &plan, nil
}

func (s *Store) SavePlan(plan *model.Plan) error {
	if err := s.saveYAML(s.planPath(), plan); err != nil {
		return err
	}
	s.commitAndPush(SyncMsg("update plan"), []string{"plan.yaml"})
	return nil
}

// Annotations

func (s *Store) LoadAnnotations() (*model.Annotations, error) {
	s.pull()

	var ann model.Annotations
	if err := s.loadYAML(s.annotationsPath(), &ann); err != nil {
		if os.IsNotExist(err) {
			return &model.Annotations{}, nil
		}
		return nil, err
	}
	return &ann, nil
}

func (s *Store) SaveAnnotations(ann *model.Annotations) error {
	if err := s.saveYAML(s.annotationsPath(), ann); err != nil {
		return err
	}
	s.commitAndPush(SyncMsg("update annotations"), []string{"annotations.yaml"})
	return nil
}

// GetAnnotation returns the annotation for a specific issue, or nil.
func (s *Store) GetAnnotation(ann *model.Annotations, issueNum int) *model.Annotation {
	for i := range ann.Items {
		if ann.Items[i].IssueNum == issueNum {
			return &ann.Items[i]
		}
	}
	return nil
}

// Rollover archives done items from previous days and keeps undone items
// with their original CreatedAt so the UI can show overdue signals.
func (s *Store) Rollover(plan *model.Plan) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var kept []model.TodoItem
	for _, item := range plan.Today {
		// Ensure CreatedAt is set
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}

		itemDate := time.Date(item.CreatedAt.Year(), item.CreatedAt.Month(), item.CreatedAt.Day(), 0, 0, 0, 0, item.CreatedAt.Location())

		if item.Done && itemDate.Before(today) {
			// Done + from a previous day → archive
			plan.Completed = append(plan.Completed, item)
		} else {
			kept = append(kept, item)
		}
	}
	plan.Today = kept
}

// Usage log

func (s *Store) usagePath() string { return filepath.Join(s.dir, "usage.log") }
func (s *Store) recapsDir() string { return filepath.Join(s.dir, "recaps") }

func (s *Store) LogUsage(command string) {
	f, err := os.OpenFile(s.usagePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\n", time.Now().Format(time.RFC3339), command)
}

func (s *Store) LoadUsageStats() (map[string]int, error) {
	data, err := os.ReadFile(s.usagePath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int{}, nil
		}
		return nil, err
	}
	stats := make(map[string]int)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			stats[parts[1]]++
		}
	}
	return stats, nil
}

// Recaps

func (s *Store) SaveRecap(name string, content string) error {
	dir := s.recapsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	relPath := filepath.Join("recaps", name+".md")
	if err := os.WriteFile(filepath.Join(s.dir, relPath), []byte(content), 0644); err != nil {
		return err
	}
	s.commitAndPush(SyncMsg("save recap "+name), []string{relPath})
	return nil
}

// sync helpers — delegate to the optional Syncer, silently ignoring errors
// so that offline or non-VCS usage is never blocked.

func (s *Store) pull() {
	if s.syncer != nil {
		_ = s.syncer.Pull()
	}
}

func (s *Store) commitAndPush(msg string, paths []string) {
	if s.syncer != nil {
		_ = s.syncer.CommitAndPush(msg, paths)
	}
}

// helpers

func (s *Store) loadYAML(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, v)
}

func (s *Store) saveYAML(path string, v interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
