package tui

import (
	"strings"
	"testing"

	"github.com/autumnust/tack/internal/model"
)

// A closed GitHub issue should render as Done in the board even when
// the project's Status field is still "ToDo" (a common drift: people
// close issues but don't move them on the project board). Two bugs
// fed into the original failure:
//
//   - isDone compared state case-sensitively against "closed", but
//     the cache stores "CLOSED" — so a closed issue was missed.
//   - renderStatus only looked at the project Status field, so even
//     when the doneness check elsewhere caught it, the label still
//     read "○ Todo".
func TestIsDone_ClosedIssueRegardlessOfProjectStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
		state  string
		want   bool
	}{
		{"closed-uppercase + ToDo status", "ToDo", "CLOSED", true},
		{"closed-lowercase + ToDo status", "ToDo", "closed", true},
		{"closed + Done status", "Done", "CLOSED", true},
		{"open + Done status", "Done", "OPEN", true},
		{"open + InProgress", "InProgress", "OPEN", false},
		{"open + ToDo", "ToDo", "OPEN", false},
		{"open + empty status", "", "OPEN", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDone(c.status, c.state); got != c.want {
				t.Errorf("isDone(%q, %q) = %v, want %v", c.status, c.state, got, c.want)
			}
		})
	}
}

// renderStatus must show "● Done" when the issue is closed, regardless
// of what the project Status field says.
func TestRenderStatus_ClosedIssueShowsDone(t *testing.T) {
	out := renderStatus("ToDo", "CLOSED")
	if !strings.Contains(out, "Done") {
		t.Errorf("renderStatus(\"ToDo\", \"CLOSED\") = %q, want it to contain 'Done'", out)
	}
	if strings.Contains(out, "Todo") {
		t.Errorf("renderStatus(\"ToDo\", \"CLOSED\") leaked the stale project status: %q", out)
	}
}

// Open issues keep using their Project Status verbatim — we don't want
// to override anything for genuinely-in-flight work.
func TestRenderStatus_OpenIssueUsesProjectStatus(t *testing.T) {
	out := renderStatus("InProgress", "OPEN")
	if !strings.Contains(strings.ToLower(out), "progress") {
		t.Errorf("renderStatus(\"InProgress\", \"OPEN\") = %q, want it to mention 'progress'", out)
	}
}

// ToggleShowDone hides closed/done items and the epic headers that
// would otherwise dangle without children.
func TestBoard_ToggleShowDone_HidesDoneRowsAndEmptyEpics(t *testing.T) {
	persons := []model.PersonGroup{
		{
			Login: "alice",
			Groups: []model.IssueGroup{
				// Standalone open + done
				{Issues: []model.ProjectItem{{Number: 1, Title: "open", State: "open"}}},
				{Issues: []model.ProjectItem{{Number: 2, Title: "closed-standalone", State: "CLOSED"}}},
				// Epic with one open and one done child
				{
					Parent: &model.ParentRef{Number: 100, Title: "Epic A"},
					Issues: []model.ProjectItem{
						{Number: 10, Title: "open-child", Parent: &model.ParentRef{Number: 100}, State: "open"},
						{Number: 11, Title: "done-child", Parent: &model.ParentRef{Number: 100}, Status: "Done", State: "open"},
					},
				},
				// Epic where every child is done — should hide entirely
				{
					Parent: &model.ParentRef{Number: 200, Title: "Epic B (all-done)"},
					Issues: []model.ProjectItem{
						{Number: 20, Title: "done-only-1", Parent: &model.ParentRef{Number: 200}, State: "CLOSED"},
					},
				},
			},
		},
	}
	b := NewBoardModel(persons)
	if !b.ShowingDone() {
		t.Fatalf("default should show done items (matches plan view)")
	}

	collectTitles := func() []string {
		var out []string
		for _, it := range b.visibleItems {
			switch it.kind {
			case kindEpicHeader:
				out = append(out, "epic:"+it.parent.Title)
			case kindIssue:
				out = append(out, "issue:"+it.issue.Title)
			}
		}
		return out
	}

	full := collectTitles()
	for _, want := range []string{"issue:open", "issue:closed-standalone", "epic:Epic A", "issue:done-child", "epic:Epic B (all-done)"} {
		found := false
		for _, t := range full {
			if t == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default render should include %q; got %v", want, full)
		}
	}

	b.ToggleShowDone()
	if b.ShowingDone() {
		t.Fatalf("toggle should have hidden done items")
	}
	hidden := collectTitles()
	for _, banned := range []string{"issue:closed-standalone", "issue:done-child", "epic:Epic B (all-done)"} {
		for _, h := range hidden {
			if h == banned {
				t.Errorf("toggle off should hide %q; still in %v", banned, hidden)
			}
		}
	}
	// Epic A still has open-child, so the header should remain.
	hasEpicA := false
	for _, h := range hidden {
		if h == "epic:Epic A" {
			hasEpicA = true
		}
	}
	if !hasEpicA {
		t.Errorf("Epic A should still render (has visible child); got %v", hidden)
	}
}
