package model

import "time"

type Config struct {
	Project     string   `yaml:"project"`
	Team        []string `yaml:"team"`
	StatusField string   `yaml:"status_field"`
}

type Project struct {
	ID          string
	NodeID      string
	Title       string
	StatusField FieldInfo
	Items       []ProjectItem
}

type FieldInfo struct {
	ID      string
	Name    string
	Options []FieldOption
}

type FieldOption struct {
	ID   string
	Name string
}

type ProjectItem struct {
	ID        string // issue/PR node ID (for comments)
	ItemID    string // project item ID (for field mutations)
	Title     string
	Number    int
	URL       string
	Body      string
	State     string
	Status    string
	Assignees []string
	Labels    []string
	Repo      string
	Parent    *ParentRef
	Comments  []Comment
}

type ParentRef struct {
	Title  string
	Number int
	URL    string
	Repo   string
}

type Comment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

type PersonGroup struct {
	Login  string
	Groups []IssueGroup
}

type IssueGroup struct {
	Parent *ParentRef // nil for standalone issues
	Issues []ProjectItem
}
