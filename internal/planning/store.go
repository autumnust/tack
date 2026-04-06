package planning

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/standup-kanban/standup-kanban/internal/model"
	"gopkg.in/yaml.v3"
)

// Store manages reading/writing planning files from a configurable directory.
type Store struct {
	dir string
}

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
	return &Store{dir: dir}, nil
}

func (s *Store) Dir() string { return s.dir }

// Plan

func (s *Store) planPath() string       { return filepath.Join(s.dir, "plan.yaml") }
func (s *Store) annotationsPath() string { return filepath.Join(s.dir, "annotations.yaml") }
func (s *Store) inboxPath() string       { return filepath.Join(s.dir, "inbox.yaml") }
func (s *Store) scratchPath() string     { return filepath.Join(s.dir, "scratch.md") }

func (s *Store) LoadPlan() (*model.Plan, error) {
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
	return s.saveYAML(s.planPath(), plan)
}

// Annotations

func (s *Store) LoadAnnotations() (*model.Annotations, error) {
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
	return s.saveYAML(s.annotationsPath(), ann)
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

// Inbox

func (s *Store) LoadInbox() (*model.Inbox, error) {
	var inbox model.Inbox
	if err := s.loadYAML(s.inboxPath(), &inbox); err != nil {
		if os.IsNotExist(err) {
			return &model.Inbox{}, nil
		}
		return nil, err
	}
	return &inbox, nil
}

func (s *Store) SaveInbox(inbox *model.Inbox) error {
	return s.saveYAML(s.inboxPath(), inbox)
}

func (s *Store) ClearInbox() error {
	return s.SaveInbox(&model.Inbox{})
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
