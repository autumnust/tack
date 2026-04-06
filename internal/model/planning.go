package model

import "time"

// Plan holds the user's personal planning data.
type Plan struct {
	WeekFocus []FocusItem   `yaml:"week_focus,omitempty"`
	Today     []TodoItem    `yaml:"today,omitempty"`
	Scratch   []ScratchNote `yaml:"scratch,omitempty"`
}

// FocusItem is a weekly focus entry — either a freeform goal or a pinned GitHub issue.
type FocusItem struct {
	Text      string `yaml:"text,omitempty"`       // freeform goal description
	IssueNum  int    `yaml:"issue_num,omitempty"`   // pinned GitHub issue number (0 if freeform)
	IssueRepo string `yaml:"issue_repo,omitempty"`  // repo for the pinned issue
	Pinned    bool   `yaml:"pinned,omitempty"`      // true if pinned from standup mode
}

// TodoItem is a daily task.
type TodoItem struct {
	Text     string `yaml:"text"`
	Done     bool   `yaml:"done,omitempty"`
	IssueNum int    `yaml:"issue_num,omitempty"` // optional linked issue
}

// ScratchNote is a freeform note.
type ScratchNote struct {
	Text      string    `yaml:"text"`
	CreatedAt time.Time `yaml:"created_at"`
}

// Annotation is a private note attached to a GitHub issue.
type Annotation struct {
	IssueNum  int       `yaml:"issue_num"`
	IssueRepo string    `yaml:"issue_repo,omitempty"`
	Notes     []string  `yaml:"notes"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

// Annotations is the full set of per-issue annotations.
type Annotations struct {
	Items []Annotation `yaml:"items"`
}

// InboxItem is a task/note pushed from an external process.
type InboxItem struct {
	From      string    `yaml:"from,omitempty"`
	Text      string    `yaml:"text"`
	Refs      []string  `yaml:"refs,omitempty"`       // issue refs like "org/repo#123"
	CreatedAt time.Time `yaml:"created_at"`
}

// Inbox holds items pushed from external sources.
type Inbox struct {
	Items []InboxItem `yaml:"items"`
}
