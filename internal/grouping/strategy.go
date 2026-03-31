package grouping

import (
	"sort"

	"github.com/standup-kanban/standup-kanban/internal/model"
)

// Strategy defines how issues within a person are grouped.
type Strategy interface {
	Name() string
	Group(items []model.ProjectItem) []model.IssueGroup
}

// ByEpic groups issues by their parent (tracked-in) issue.
// Standalone issues (no parent) are placed in their own group.
type ByEpic struct{}

func (ByEpic) Name() string { return "epic" }

func (ByEpic) Group(items []model.ProjectItem) []model.IssueGroup {
	epicMap := make(map[int]*model.IssueGroup) // keyed by parent issue number
	var standalone []model.ProjectItem
	var epicOrder []int

	for _, item := range items {
		if item.Parent != nil {
			key := item.Parent.Number
			if _, ok := epicMap[key]; !ok {
				epicMap[key] = &model.IssueGroup{
					Parent: item.Parent,
				}
				epicOrder = append(epicOrder, key)
			}
			epicMap[key].Issues = append(epicMap[key].Issues, item)
		} else {
			standalone = append(standalone, item)
		}
	}

	// Sort epics by number
	sort.Ints(epicOrder)

	var groups []model.IssueGroup
	for _, key := range epicOrder {
		groups = append(groups, *epicMap[key])
	}

	// Standalone issues at the end, each in its own group
	for _, item := range standalone {
		groups = append(groups, model.IssueGroup{Issues: []model.ProjectItem{item}})
	}

	return groups
}

// ByLabel groups issues by a specified label prefix.
type ByLabel struct {
	Prefix string
}

func (b ByLabel) Name() string { return "label:" + b.Prefix }

func (b ByLabel) Group(items []model.ProjectItem) []model.IssueGroup {
	labelMap := make(map[string]*model.IssueGroup)
	var labelOrder []string
	var noLabel []model.ProjectItem

	for _, item := range items {
		found := false
		for _, l := range item.Labels {
			if len(l) > len(b.Prefix) && l[:len(b.Prefix)] == b.Prefix {
				key := l
				if _, ok := labelMap[key]; !ok {
					labelMap[key] = &model.IssueGroup{
						Parent: &model.ParentRef{Title: key},
					}
					labelOrder = append(labelOrder, key)
				}
				labelMap[key].Issues = append(labelMap[key].Issues, item)
				found = true
				break
			}
		}
		if !found {
			noLabel = append(noLabel, item)
		}
	}

	sort.Strings(labelOrder)

	var groups []model.IssueGroup
	for _, key := range labelOrder {
		groups = append(groups, *labelMap[key])
	}
	for _, item := range noLabel {
		groups = append(groups, model.IssueGroup{Issues: []model.ProjectItem{item}})
	}

	return groups
}

// GroupByPerson takes all project items and groups them by assignee, then by strategy.
func GroupByPerson(items []model.ProjectItem, team []string, strategy Strategy) []model.PersonGroup {
	byPerson := make(map[string][]model.ProjectItem)
	for _, item := range items {
		if len(item.Assignees) == 0 {
			byPerson["unassigned"] = append(byPerson["unassigned"], item)
			continue
		}
		for _, a := range item.Assignees {
			byPerson[a] = append(byPerson[a], item)
		}
	}

	// Build in team order, then append anyone not in team list
	seen := make(map[string]bool)
	var result []model.PersonGroup

	for _, login := range team {
		seen[login] = true
		items := byPerson[login]
		if len(items) == 0 {
			result = append(result, model.PersonGroup{Login: login})
			continue
		}
		result = append(result, model.PersonGroup{
			Login:  login,
			Groups: strategy.Group(items),
		})
	}

	// Others not in team config
	var others []string
	for login := range byPerson {
		if !seen[login] {
			others = append(others, login)
		}
	}
	sort.Strings(others)
	for _, login := range others {
		result = append(result, model.PersonGroup{
			Login:  login,
			Groups: strategy.Group(byPerson[login]),
		})
	}

	return result
}
