package tui

import (
	"strings"
	"testing"
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
