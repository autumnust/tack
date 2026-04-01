package tui

import (
	"fmt"
	"strings"

	"github.com/standup-kanban/standup-kanban/internal/model"
)

type OpKind int

const (
	OpMove OpKind = iota
	OpComment
	OpFocusAdd
	OpFocusRemove
	OpFocusClear
	OpFocusSave
	OpAssign
)

type PendingOp struct {
	Kind     OpKind
	Checked  bool // for review screen
	IssueNum int
	IssueID  string // node ID for comments
	ItemID   string // project item ID for moves

	// OpMove
	NewStatus string

	// OpComment
	CommentBody string

	// OpFocusAdd / OpFocusRemove
	FocusTarget string // "@Name" for per-person, "" for global
	FocusNumber int

	// OpAssign
	AssignLogin string
}

func (op PendingOp) Description() string {
	switch op.Kind {
	case OpMove:
		return fmt.Sprintf(":mv #%d → %s", op.IssueNum, op.NewStatus)
	case OpComment:
		body := op.CommentBody
		if len(body) > 50 {
			body = body[:47] + "..."
		}
		return fmt.Sprintf(":c #%d %q", op.IssueNum, body)
	case OpFocusAdd:
		if op.FocusTarget != "" {
			return fmt.Sprintf(":focus %s %d", op.FocusTarget, op.FocusNumber)
		}
		return fmt.Sprintf(":focus %d", op.FocusNumber)
	case OpFocusRemove:
		if op.FocusTarget != "" {
			return fmt.Sprintf(":unfocus %s %d", op.FocusTarget, op.FocusNumber)
		}
		return fmt.Sprintf(":unfocus %d", op.FocusNumber)
	case OpFocusClear:
		return ":focus clear"
	case OpFocusSave:
		return ":focus save"
	case OpAssign:
		return fmt.Sprintf(":assign #%d @%s", op.IssueNum, op.AssignLogin)
	}
	return "unknown"
}

// IsGitHubOp returns true if this op requires a GitHub API call.
func (op PendingOp) IsGitHubOp() bool {
	return op.Kind == OpMove || op.Kind == OpComment || op.Kind == OpAssign
}

// IsConfigOp returns true if this op modifies config.yaml.
func (op PendingOp) IsConfigOp() bool {
	return op.Kind == OpFocusAdd || op.Kind == OpFocusRemove || op.Kind == OpFocusClear || op.Kind == OpFocusSave
}

type OpQueue struct {
	ops []PendingOp
}

func (q *OpQueue) Push(op PendingOp) {
	op.Checked = true // default to checked
	q.ops = append(q.ops, op)
}

func (q *OpQueue) Pop() *PendingOp {
	if len(q.ops) == 0 {
		return nil
	}
	op := q.ops[len(q.ops)-1]
	q.ops = q.ops[:len(q.ops)-1]
	return &op
}

func (q *OpQueue) Len() int {
	return len(q.ops)
}

func (q *OpQueue) Ops() []PendingOp {
	return q.ops
}

func (q *OpQueue) Toggle(idx int) {
	if idx >= 0 && idx < len(q.ops) {
		q.ops[idx].Checked = !q.ops[idx].Checked
	}
}

func (q *OpQueue) CheckAll() {
	for i := range q.ops {
		q.ops[i].Checked = true
	}
}

func (q *OpQueue) UncheckAll() {
	for i := range q.ops {
		q.ops[i].Checked = false
	}
}

func (q *OpQueue) CheckedOps() []PendingOp {
	var result []PendingOp
	for _, op := range q.ops {
		if op.Checked {
			result = append(result, op)
		}
	}
	return result
}

// ExecuteChecked runs all checked operations. Returns a summary.
func (q *OpQueue) ExecuteChecked(project *model.Project, client interface {
	MoveItem(project *model.Project, itemID string, statusName string) error
	AddComment(issueNodeID string, body string) error
	AssignIssue(issueNodeID string, login string) error
}, configPath string, config *model.Config) (string, error) {
	checked := q.CheckedOps()
	if len(checked) == 0 {
		return "No operations to push", nil
	}

	var succeeded, failed int
	var errors []string

	// Collect config ops to batch
	needConfigSave := false

	for _, op := range checked {
		switch op.Kind {
		case OpMove:
			if err := client.MoveItem(project, op.ItemID, op.NewStatus); err != nil {
				failed++
				errors = append(errors, fmt.Sprintf("#%d move: %s", op.IssueNum, err))
			} else {
				succeeded++
			}
		case OpComment:
			if err := client.AddComment(op.IssueID, op.CommentBody); err != nil {
				failed++
				errors = append(errors, fmt.Sprintf("#%d comment: %s", op.IssueNum, err))
			} else {
				succeeded++
			}
		case OpAssign:
			if err := client.AssignIssue(op.IssueID, op.AssignLogin); err != nil {
				failed++
				errors = append(errors, fmt.Sprintf("#%d assign: %s", op.IssueNum, err))
			} else {
				succeeded++
			}
		case OpFocusSave:
			needConfigSave = true
			succeeded++
		case OpFocusAdd, OpFocusRemove, OpFocusClear:
			// These were already applied locally; only persist if :focus save is also checked
			needConfigSave = true
			succeeded++
		}
	}

	if needConfigSave {
		if err := saveConfig(configPath, config); err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("config save: %s", err))
		}
	}

	summary := fmt.Sprintf("Pushed %d operations", succeeded)
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed: %s", failed, strings.Join(errors, "; "))
	}
	return summary, nil
}
