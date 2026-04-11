package grouping

import (
	"testing"

	"github.com/autumnust/tack/internal/model"
)

// --- ByEpic tests ---

func TestByEpic_ChildrenGroupedUnderParent(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 101, Title: "Child 1", Parent: &model.ParentRef{Number: 100, Title: "Epic"}},
		{Number: 102, Title: "Child 2", Parent: &model.ParentRef{Number: 100, Title: "Epic"}},
	}
	groups := ByEpic{}.Group(items)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Parent == nil || groups[0].Parent.Number != 100 {
		t.Error("expected group parent to be #100")
	}
	if len(groups[0].Issues) != 2 {
		t.Errorf("expected 2 issues in group, got %d", len(groups[0].Issues))
	}
}

func TestByEpic_StandaloneItems(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 300, Title: "Standalone A"},
		{Number: 301, Title: "Standalone B"},
	}
	groups := ByEpic{}.Group(items)
	if len(groups) != 2 {
		t.Fatalf("expected 2 standalone groups, got %d", len(groups))
	}
	for _, g := range groups {
		if g.Parent != nil {
			t.Error("standalone groups should have nil Parent")
		}
		if len(g.Issues) != 1 {
			t.Error("each standalone group should have 1 issue")
		}
	}
}

func TestByEpic_ParentDedup(t *testing.T) {
	// Item #100 is itself in the list AND is a parent of #101.
	// It should appear as an epic header, not as a duplicate standalone.
	items := []model.ProjectItem{
		{Number: 100, Title: "Epic Alpha"},
		{Number: 101, Title: "Child 1", Parent: &model.ParentRef{Number: 100, Title: "Epic Alpha"}},
	}
	groups := ByEpic{}.Group(items)
	// Should be 1 group (epic #100 with child #101), not 2
	if len(groups) != 1 {
		t.Fatalf("expected 1 group (parent deduped), got %d", len(groups))
	}
	if groups[0].Parent == nil || groups[0].Parent.Number != 100 {
		t.Error("expected epic group for #100")
	}
	if len(groups[0].Issues) != 1 {
		t.Errorf("expected 1 child issue, got %d", len(groups[0].Issues))
	}
}

func TestByEpic_KnownParentNoChildren(t *testing.T) {
	// Item #200 is in ParentNumbers but has no children assigned to this person
	items := []model.ProjectItem{
		{Number: 200, Title: "Epic Beta", URL: "https://example.com/200", Repo: "org/repo"},
	}
	groups := ByEpic{ParentNumbers: map[int]bool{200: true}}.Group(items)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Parent == nil {
		t.Fatal("expected epic header for known parent")
	}
	if groups[0].Parent.Number != 200 {
		t.Errorf("expected parent #200, got #%d", groups[0].Parent.Number)
	}
	// No children for this person
	if len(groups[0].Issues) != 0 {
		t.Errorf("expected 0 children, got %d", len(groups[0].Issues))
	}
}

func TestByEpic_EmptyInput(t *testing.T) {
	groups := ByEpic{}.Group(nil)
	if len(groups) != 0 {
		t.Errorf("expected 0 groups for empty input, got %d", len(groups))
	}
}

func TestByEpic_EpicsSortedByNumber(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 201, Title: "Child of 200", Parent: &model.ParentRef{Number: 200, Title: "Beta"}},
		{Number: 101, Title: "Child of 100", Parent: &model.ParentRef{Number: 100, Title: "Alpha"}},
	}
	groups := ByEpic{}.Group(items)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].Parent.Number != 100 {
		t.Errorf("expected first group parent #100, got #%d", groups[0].Parent.Number)
	}
	if groups[1].Parent.Number != 200 {
		t.Errorf("expected second group parent #200, got #%d", groups[1].Parent.Number)
	}
}

// --- ByLabel tests ---

func TestByLabel_MatchingPrefix(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Title: "Backend task", Labels: []string{"area:backend", "priority:high"}},
		{Number: 2, Title: "Another backend", Labels: []string{"area:backend"}},
		{Number: 3, Title: "Frontend task", Labels: []string{"area:frontend"}},
	}
	groups := ByLabel{Prefix: "area:"}.Group(items)
	// 2 groups: area:backend (2 items), area:frontend (1 item)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].Parent.Title != "area:backend" {
		t.Errorf("expected first group 'area:backend', got %q", groups[0].Parent.Title)
	}
	if len(groups[0].Issues) != 2 {
		t.Errorf("expected 2 issues in backend group, got %d", len(groups[0].Issues))
	}
}

func TestByLabel_NoMatchingLabels(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Title: "No labels"},
		{Number: 2, Title: "Wrong label", Labels: []string{"priority:high"}},
	}
	groups := ByLabel{Prefix: "area:"}.Group(items)
	// Both standalone
	if len(groups) != 2 {
		t.Fatalf("expected 2 standalone groups, got %d", len(groups))
	}
	for _, g := range groups {
		if g.Parent != nil {
			t.Error("expected nil parent for unmatched items")
		}
	}
}

func TestByLabel_EmptyInput(t *testing.T) {
	groups := ByLabel{Prefix: "x:"}.Group(nil)
	if len(groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(groups))
	}
}

func TestByLabel_FirstMatchWins(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Title: "Multi-label", Labels: []string{"area:backend", "area:frontend"}},
	}
	groups := ByLabel{Prefix: "area:"}.Group(items)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Parent.Title != "area:backend" {
		t.Errorf("expected 'area:backend' (first match), got %q", groups[0].Parent.Title)
	}
}

// --- GroupByPerson tests ---

func TestGroupByPerson_BasicGrouping(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Assignees: []string{"alice"}},
		{Number: 2, Assignees: []string{"bob"}},
		{Number: 3, Assignees: []string{"alice"}},
	}
	result := GroupByPerson(items, []string{"alice", "bob"}, ByEpic{}, map[string]string{})
	if len(result) != 2 {
		t.Fatalf("expected 2 persons, got %d", len(result))
	}
	// alice has 2 items
	aliceIssues := 0
	for _, g := range result[0].Groups {
		aliceIssues += len(g.Issues)
	}
	if aliceIssues != 2 {
		t.Errorf("expected alice to have 2 issues, got %d", aliceIssues)
	}
}

func TestGroupByPerson_MultiAssigneeFanout(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Assignees: []string{"alice", "bob"}},
	}
	result := GroupByPerson(items, []string{"alice", "bob"}, ByEpic{}, map[string]string{})
	for _, p := range result {
		count := 0
		for _, g := range p.Groups {
			count += len(g.Issues)
		}
		if count != 1 {
			t.Errorf("expected person %s to have 1 issue (fanout), got %d", p.Login, count)
		}
	}
}

func TestGroupByPerson_TeamOrdering(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Assignees: []string{"alice"}},
		{Number: 2, Assignees: []string{"bob"}},
	}
	// Team order is bob, alice — result should follow
	result := GroupByPerson(items, []string{"bob", "alice"}, ByEpic{}, map[string]string{})
	if result[0].Login != "bob" {
		t.Errorf("expected first person bob, got %s", result[0].Login)
	}
	if result[1].Login != "alice" {
		t.Errorf("expected second person alice, got %s", result[1].Login)
	}
}

func TestGroupByPerson_FocusFiltering(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 100, Assignees: []string{"alice"}},
		{Number: 200, Assignees: []string{"alice"}},
	}
	focuses := map[string]map[int]bool{"alice": {100: true}}
	result := GroupByPerson(items, []string{"alice"}, ByEpic{}, map[string]string{}, focuses)
	count := 0
	for _, g := range result[0].Groups {
		count += len(g.Issues)
	}
	if count != 1 {
		t.Errorf("expected 1 item after focus filter, got %d", count)
	}
}

func TestGroupByPerson_FocusIncludesChildren(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 101, Assignees: []string{"alice"}, Parent: &model.ParentRef{Number: 100}},
		{Number: 200, Assignees: []string{"alice"}},
	}
	// Focus on parent #100 — child #101 should be kept
	focuses := map[string]map[int]bool{"alice": {100: true}}
	result := GroupByPerson(items, []string{"alice"}, ByEpic{}, map[string]string{}, focuses)
	count := 0
	for _, g := range result[0].Groups {
		count += len(g.Issues)
	}
	if count != 1 {
		t.Errorf("expected 1 item (child of focused parent), got %d", count)
	}
}

func TestGroupByPerson_DisplayNames(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Assignees: []string{"alice"}},
	}
	dn := map[string]string{"alice": "Alice Smith"}
	result := GroupByPerson(items, []string{"alice"}, ByEpic{}, dn)
	if result[0].DisplayName != "Alice Smith" {
		t.Errorf("expected display name 'Alice Smith', got %q", result[0].DisplayName)
	}
}

func TestGroupByPerson_EmptyTeamShowsAll(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 1, Assignees: []string{"charlie"}},
		{Number: 2, Assignees: []string{"alice"}},
	}
	result := GroupByPerson(items, nil, ByEpic{}, map[string]string{})
	if len(result) != 2 {
		t.Fatalf("expected 2 persons with empty team, got %d", len(result))
	}
	// Should be alphabetically sorted
	if result[0].Login != "alice" {
		t.Errorf("expected first person alice (alphabetical), got %s", result[0].Login)
	}
}

func TestGroupByPerson_EmptyTeamMember(t *testing.T) {
	// Team member with no items still appears
	items := []model.ProjectItem{
		{Number: 1, Assignees: []string{"alice"}},
	}
	result := GroupByPerson(items, []string{"alice", "bob"}, ByEpic{}, map[string]string{})
	if len(result) != 2 {
		t.Fatalf("expected 2 persons (bob with 0 items), got %d", len(result))
	}
	if result[1].Login != "bob" {
		t.Errorf("expected bob as second person, got %s", result[1].Login)
	}
}
