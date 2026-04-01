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
// If an item is itself a parent (its number is an epic header), it's not shown
// as a standalone — the epic header already represents it.
type ByEpic struct {
	// ParentNumbers is the set of issue numbers that are known parents
	// (i.e., keys from ChildrenMap). Used to deduplicate parent items.
	ParentNumbers map[int]bool
}

func (b ByEpic) Name() string { return "epic" }

func (b ByEpic) Group(items []model.ProjectItem) []model.IssueGroup {
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

	// Standalone issues at the end — but skip items that are already
	// represented as epic headers (they ARE the parent ticket).
	for _, item := range standalone {
		if _, isEpicHeader := epicMap[item.Number]; isEpicHeader {
			continue // already shown as the epic group header
		}
		if b.ParentNumbers != nil && b.ParentNumbers[item.Number] {
			// This item is a known parent from ChildrenMap but has no
			// children assigned to this person — still show as epic header
			// with empty children (user can drill in to see all sub-issues).
			groups = append(groups, model.IssueGroup{
				Parent: &model.ParentRef{
					Title:  item.Title,
					Number: item.Number,
					URL:    item.URL,
					Repo:   item.Repo,
				},
			})
			continue
		}
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
// displayNames maps login -> friendly name. focusSets maps login -> set of focus issue numbers.
func GroupByPerson(items []model.ProjectItem, team []string, strategy Strategy, displayNames map[string]string, focusSets ...map[string]map[int]bool) []model.PersonGroup {
	var focuses map[string]map[int]bool
	if len(focusSets) > 0 {
		focuses = focusSets[0]
	}

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

	nameFor := func(login string) string {
		if n, ok := displayNames[login]; ok && n != "" {
			return n
		}
		return login
	}

	filterItems := func(login string, items []model.ProjectItem) []model.ProjectItem {
		if focuses == nil {
			return items
		}
		focus, ok := focuses[login]
		if !ok || len(focus) == 0 {
			return items
		}
		var filtered []model.ProjectItem
		for _, item := range items {
			// Keep if: issue number is in focus, or parent number is in focus
			if focus[item.Number] {
				filtered = append(filtered, item)
			} else if item.Parent != nil && focus[item.Parent.Number] {
				filtered = append(filtered, item)
			}
		}
		return filtered
	}

	// Build in team order, then append anyone not in team list
	seen := make(map[string]bool)
	var result []model.PersonGroup

	for _, login := range team {
		seen[login] = true
		personItems := filterItems(login, byPerson[login])
		if len(personItems) == 0 {
			result = append(result, model.PersonGroup{Login: login, DisplayName: nameFor(login)})
			continue
		}
		result = append(result, model.PersonGroup{
			Login:       login,
			DisplayName: nameFor(login),
			Groups:      strategy.Group(personItems),
		})
	}

	// Only show team members when a team list is configured.
	// Others are excluded — use :add to include them.
	if len(team) == 0 {
		var others []string
		for login := range byPerson {
			if !seen[login] {
				others = append(others, login)
			}
		}
		sort.Strings(others)
		for _, login := range others {
			personItems := filterItems(login, byPerson[login])
			result = append(result, model.PersonGroup{
				Login:       login,
				DisplayName: nameFor(login),
				Groups:      strategy.Group(personItems),
			})
		}
	}

	return result
}
