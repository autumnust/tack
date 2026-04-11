package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autumnust/tack/internal/model"
)

// --- Mock client for ExecuteChecked ---

type mockClient struct {
	moveErr    error
	commentErr error
	assignErr  error
	moveCalls  int
	commentCalls int
	assignCalls  int
}

func (m *mockClient) MoveItem(_ *model.Project, _ string, _ string) error {
	m.moveCalls++
	return m.moveErr
}

func (m *mockClient) AddComment(_ string, _ string) error {
	m.commentCalls++
	return m.commentErr
}

func (m *mockClient) AssignIssue(_ string, _ string) error {
	m.assignCalls++
	return m.assignErr
}

// --- OpQueue tests ---

func TestOpQueue_PushPopLen(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove, IssueNum: 1})
	q.Push(PendingOp{Kind: OpComment, IssueNum: 2})
	q.Push(PendingOp{Kind: OpAssign, IssueNum: 3})

	if q.Len() != 3 {
		t.Fatalf("expected Len=3, got %d", q.Len())
	}

	op := q.Pop()
	if op == nil || op.IssueNum != 3 {
		t.Error("Pop should return last pushed item")
	}
	if q.Len() != 2 {
		t.Errorf("expected Len=2 after Pop, got %d", q.Len())
	}
}

func TestOpQueue_PopEmpty(t *testing.T) {
	var q OpQueue
	if q.Pop() != nil {
		t.Error("Pop on empty queue should return nil")
	}
}

func TestOpQueue_PushDefaultsChecked(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove})
	if !q.Ops()[0].Checked {
		t.Error("Push should default Checked=true")
	}
}

func TestOpQueue_Toggle(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove})

	if !q.Ops()[0].Checked {
		t.Fatal("expected Checked=true initially")
	}
	q.Toggle(0)
	if q.Ops()[0].Checked {
		t.Error("expected Checked=false after Toggle")
	}
	q.Toggle(0)
	if !q.Ops()[0].Checked {
		t.Error("expected Checked=true after second Toggle")
	}
}

func TestOpQueue_ToggleOutOfBounds(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove})
	q.Toggle(-1)  // should not panic
	q.Toggle(99)  // should not panic
}

func TestOpQueue_CheckAll_UncheckAll(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove})
	q.Push(PendingOp{Kind: OpComment})
	q.Push(PendingOp{Kind: OpAssign})

	q.UncheckAll()
	if len(q.CheckedOps()) != 0 {
		t.Errorf("expected 0 checked after UncheckAll, got %d", len(q.CheckedOps()))
	}

	q.CheckAll()
	if len(q.CheckedOps()) != 3 {
		t.Errorf("expected 3 checked after CheckAll, got %d", len(q.CheckedOps()))
	}
}

func TestOpQueue_CheckedOps(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove, IssueNum: 1})
	q.Push(PendingOp{Kind: OpComment, IssueNum: 2})
	q.Push(PendingOp{Kind: OpAssign, IssueNum: 3})

	q.Toggle(1) // uncheck middle one
	checked := q.CheckedOps()
	if len(checked) != 2 {
		t.Fatalf("expected 2 checked, got %d", len(checked))
	}
}

// --- PendingOp tests ---

func TestPendingOp_Description(t *testing.T) {
	tests := []struct {
		name   string
		op     PendingOp
		expect string
	}{
		{"move", PendingOp{Kind: OpMove, IssueNum: 101, NewStatus: "Done"}, ":mv #101 → Done"},
		{"comment short", PendingOp{Kind: OpComment, IssueNum: 42, CommentBody: "hello"}, `:c #42 "hello"`},
		{"comment truncated", PendingOp{Kind: OpComment, IssueNum: 42, CommentBody: strings.Repeat("x", 60)}, `:c #42 "`},
		{"focus global", PendingOp{Kind: OpFocusAdd, FocusNumber: 100}, ":focus 100"},
		{"focus person", PendingOp{Kind: OpFocusAdd, FocusTarget: "@Alice", FocusNumber: 100}, ":focus @Alice 100"},
		{"unfocus global", PendingOp{Kind: OpFocusRemove, FocusNumber: 100}, ":unfocus 100"},
		{"focus clear", PendingOp{Kind: OpFocusClear}, ":focus clear"},
		{"focus save", PendingOp{Kind: OpFocusSave}, ":focus save"},
		{"assign", PendingOp{Kind: OpAssign, IssueNum: 101, AssignLogin: "alice"}, ":assign #101 @alice"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.op.Description()
			if !strings.Contains(got, tt.expect) {
				t.Errorf("Description() = %q, want contains %q", got, tt.expect)
			}
		})
	}
}

func TestPendingOp_IsGitHubOp(t *testing.T) {
	gitOps := []OpKind{OpMove, OpComment, OpAssign}
	for _, k := range gitOps {
		if !(PendingOp{Kind: k}).IsGitHubOp() {
			t.Errorf("expected OpKind %d to be GitHub op", k)
		}
	}
	configOps := []OpKind{OpFocusAdd, OpFocusRemove, OpFocusClear, OpFocusSave}
	for _, k := range configOps {
		if (PendingOp{Kind: k}).IsGitHubOp() {
			t.Errorf("expected OpKind %d to NOT be GitHub op", k)
		}
	}
}

func TestPendingOp_IsConfigOp(t *testing.T) {
	configOps := []OpKind{OpFocusAdd, OpFocusRemove, OpFocusClear, OpFocusSave}
	for _, k := range configOps {
		if !(PendingOp{Kind: k}).IsConfigOp() {
			t.Errorf("expected OpKind %d to be config op", k)
		}
	}
	gitOps := []OpKind{OpMove, OpComment, OpAssign}
	for _, k := range gitOps {
		if (PendingOp{Kind: k}).IsConfigOp() {
			t.Errorf("expected OpKind %d to NOT be config op", k)
		}
	}
}

// --- ExecuteChecked tests ---

func TestExecuteChecked_AllSuccess(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove, IssueNum: 1, ItemID: "i1", NewStatus: "Done"})
	q.Push(PendingOp{Kind: OpComment, IssueNum: 2, IssueID: "n2", CommentBody: "test"})
	q.Push(PendingOp{Kind: OpAssign, IssueNum: 3, IssueID: "n3", AssignLogin: "alice"})

	mock := &mockClient{}
	project := &model.Project{}
	summary, err := q.ExecuteChecked(project, mock, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "3 operations") {
		t.Errorf("expected '3 operations' in summary, got %q", summary)
	}
	if mock.moveCalls != 1 || mock.commentCalls != 1 || mock.assignCalls != 1 {
		t.Errorf("expected 1 call each, got move=%d comment=%d assign=%d",
			mock.moveCalls, mock.commentCalls, mock.assignCalls)
	}
}

func TestExecuteChecked_PartialFailure(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove, IssueNum: 1, ItemID: "i1", NewStatus: "Done"})
	q.Push(PendingOp{Kind: OpComment, IssueNum: 2, IssueID: "n2", CommentBody: "test"})

	mock := &mockClient{commentErr: fmt.Errorf("API error")}
	project := &model.Project{}
	summary, _ := q.ExecuteChecked(project, mock, "", nil)
	if !strings.Contains(summary, "1 failed") {
		t.Errorf("expected '1 failed' in summary, got %q", summary)
	}
}

func TestExecuteChecked_NoCheckedOps(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpMove})
	q.UncheckAll()

	mock := &mockClient{}
	summary, _ := q.ExecuteChecked(&model.Project{}, mock, "", nil)
	if !strings.Contains(summary, "No operations") {
		t.Errorf("expected 'No operations', got %q", summary)
	}
	if mock.moveCalls != 0 {
		t.Error("expected no API calls for unchecked ops")
	}
}

func TestExecuteChecked_ConfigSave(t *testing.T) {
	var q OpQueue
	q.Push(PendingOp{Kind: OpFocusSave})

	mock := &mockClient{}
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	config := &model.Config{Project: "test"}

	summary, err := q.ExecuteChecked(&model.Project{}, mock, configPath, config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "1 operations") {
		t.Errorf("expected '1 operations' in summary, got %q", summary)
	}
}
