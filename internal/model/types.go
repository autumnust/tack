package model

import "time"

type TeamMember struct {
	Login string `yaml:"login"`
	Name  string `yaml:"name"`
	Focus []int  `yaml:"focus,omitempty"` // issue numbers (parent or leaf) to filter on
}

type PlanningConfig struct {
	Dir          string         `yaml:"dir"`
	MaxWeekFocus int            `yaml:"max_week_focus,omitempty"` // max weekly focus items (default 3)
	RedisURL     string         `yaml:"redis_url,omitempty"`      // Upstash REST URL; also reads UPSTASH_REDIS_REST_URL env
	RedisToken   string         `yaml:"redis_token,omitempty"`    // Upstash REST token; also reads UPSTASH_REDIS_REST_TOKEN env
	Obsidian     ObsidianConfig `yaml:"obsidian,omitempty"`
}

// ObsidianConfig points at an Obsidian vault that receives sealed
// monthly target reflections. Vault is required; MonthlySubdir defaults
// to "monthly".
type ObsidianConfig struct {
	Vault         string `yaml:"vault"`
	MonthlySubdir string `yaml:"monthly_subdir,omitempty"`
}

type Config struct {
	Project     string         `yaml:"project"`
	Team        []TeamMember   `yaml:"team"`
	StatusField string         `yaml:"status_field"`
	Focus       []int          `yaml:"focus,omitempty"`
	Planning    PlanningConfig `yaml:"planning,omitempty"`

	// Me is the current user's GitHub login. Required for :ship: it's the
	// person bucket that upstash-backed (non-GitHub) board items live
	// under. If unset, :ship surfaces an error pointing here.
	Me string `yaml:"me,omitempty"`

	// Repos maps "<owner>/<repo>" → absolute path of the local clone on
	// the current host. Used by :start to set the working tree of the
	// new tmux session. Missing entries fall back to ~/work/<basename>.
	Repos map[string]string `yaml:"repos,omitempty"`

	// WorkspaceDir is the root under which :start creates a per-ticket
	// scratch folder (PLAN.md, PROGRESS.md, etc). The folder name mirrors
	// the tmux session name. Defaults to ~/.tack/workspace when empty.
	WorkspaceDir string `yaml:"workspace_dir,omitempty"`

	// ShipHost names the tmux host used by :start and :ship. SSH alias
	// when running tack on a local laptop ("aws"), or "local" when tack
	// itself is running on the remote box. Defaults to "aws" when empty.
	ShipHost string `yaml:"ship_host,omitempty"`
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

// SourceUpstash marks a board item that lives only in the local/upstash
// store (no GitHub backing). Empty Source means GitHub — the historical
// default — so existing call sites don't need to set it.
const SourceUpstash = "upstash"

type ProjectItem struct {
	ID        string // issue/PR node ID (for comments). For upstash items, the UpstashTask Id.
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
	Source    string    // "" (GitHub, default) or SourceUpstash
	CreatedAt time.Time // set for upstash items so the board can shade by age
}

// IsUpstash reports whether this item is upstash-backed (no GitHub
// identity yet). Operations like :open / :mv / :c / :a need a GitHub
// node and should reject upstash items with a hint to run :github first.
func (p ProjectItem) IsUpstash() bool {
	return p.Source == SourceUpstash
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
