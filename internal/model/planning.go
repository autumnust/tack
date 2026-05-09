package model

import "time"

// Plan holds the user's personal planning data.
type Plan struct {
	WeekFocus      []FocusItem     `yaml:"week_focus,omitempty"`
	Today          []TodoItem      `yaml:"today,omitempty"`
	Completed      []TodoItem      `yaml:"completed,omitempty"` // archived done items from previous days
	Scratch        []ScratchNote   `yaml:"scratch,omitempty"`
	MonthlyTargets []MonthlyTarget `yaml:"monthly_targets,omitempty"`
	UpstashTasks   []UpstashTask   `yaml:"upstash_tasks,omitempty"`
}

// UpstashTask is a board item with no GitHub backing — the result of
// graduating a hibana note via :ship before any :github elevation. It
// renders on the board under the user's own person group; :github
// removes it and creates a real GH issue in its place.
type UpstashTask struct {
	Id        string    `yaml:"id"`
	Text      string    `yaml:"text"`
	Status    string    `yaml:"status,omitempty"`
	CreatedAt time.Time `yaml:"created_at,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty"`
}

// FocusItem is a weekly focus entry — either a freeform goal or a pinned GitHub issue.
type FocusItem struct {
	Text      string    `yaml:"text,omitempty"`
	IssueNum  int       `yaml:"issue_num,omitempty"`
	IssueRepo string    `yaml:"issue_repo,omitempty"`
	Pinned    bool      `yaml:"pinned,omitempty"`
	Done      bool      `yaml:"done,omitempty"`
	DoneAt    time.Time `yaml:"done_at,omitempty"`
	SubItems  []SubItem `yaml:"sub_items,omitempty"` // breakdown items
}

// SubItem is a breakdown task under a weekly focus goal.
type SubItem struct {
	Text     string `yaml:"text"`
	Done     bool   `yaml:"done,omitempty"`
	IssueNum int    `yaml:"issue_num,omitempty"` // optional linked issue
}

// TodoItem is a daily task.
type TodoItem struct {
	Text      string    `yaml:"text"`
	Done      bool      `yaml:"done,omitempty"`
	IssueNum  int       `yaml:"issue_num,omitempty"`
	CreatedAt time.Time `yaml:"created_at,omitempty"`
	DoneAt    time.Time `yaml:"done_at,omitempty"`
}

// ScratchNote is a freeform note. Id is the hibana NoteID; it's empty for
// notes loaded from legacy plan.yaml that haven't yet been migrated, in
// which case the TUI shouldn't allow edit/delete operations on them
// (they'll vanish on next plan save).
type ScratchNote struct {
	Id        string    `yaml:"id,omitempty"`
	Text      string    `yaml:"text"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty"`
}

// MonthlyTarget is a high-level target for the month. Month is the
// YYYY-MM bucket the target belongs to; older buckets stay in the active
// plan until they're sealed via :reflect into the Obsidian vault.
type MonthlyTarget struct {
	Text      string    `yaml:"text"`
	Done      bool      `yaml:"done,omitempty"`
	Month     string    `yaml:"month,omitempty"`
	CreatedAt time.Time `yaml:"created_at,omitempty"`
	DoneAt    time.Time `yaml:"done_at,omitempty"`
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
