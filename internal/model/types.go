package model

import "time"

type TeamMember struct {
	Login string `yaml:"login"`
	Name  string `yaml:"name"`
	Focus []int  `yaml:"focus,omitempty"` // issue numbers (parent or leaf) to filter on
}

type PlanningConfig struct {
	Dir string `yaml:"dir"` // directory for planning files (plan.yaml, annotations.yaml, inbox.yaml, scratch.md)
}

type Config struct {
	Project     string         `yaml:"project"`
	Team        []TeamMember   `yaml:"team"`
	StatusField string         `yaml:"status_field"`
	Focus       []int          `yaml:"focus,omitempty"`
	Planning    PlanningConfig `yaml:"planning,omitempty"`
}

// TeamLogins returns just the login strings.
func (c Config) TeamLogins() []string {
	logins := make([]string, len(c.Team))
	for i, m := range c.Team {
		logins[i] = m.Login
	}
	return logins
}

// FocusSet returns the set of focus issue numbers for a login, or nil if no filter.
func (c Config) FocusSet(login string) map[int]bool {
	for _, m := range c.Team {
		if m.Login == login && len(m.Focus) > 0 {
			s := make(map[int]bool, len(m.Focus))
			for _, n := range m.Focus {
				s[n] = true
			}
			return s
		}
	}
	return nil
}

// DisplayName returns the display name for a login, falling back to login itself.
func (c Config) DisplayName(login string) string {
	for _, m := range c.Team {
		if m.Login == login {
			if m.Name != "" {
				return m.Name
			}
			return m.Login
		}
	}
	return login
}

type Project struct {
	ID          string
	NodeID      string
	Title       string
	StatusField FieldInfo
	Items       []ProjectItem
	ChildrenMap map[int][]SubIssue // parent number -> sub-issues
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

type SubIssue struct {
	Number int
	Title  string
	State  string
	URL    string
}

type Comment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

type PersonGroup struct {
	Login       string
	DisplayName string
	Groups      []IssueGroup
}

type IssueGroup struct {
	Parent *ParentRef // nil for standalone issues
	Issues []ProjectItem
}
