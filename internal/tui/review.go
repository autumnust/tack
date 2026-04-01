package tui

import (
	"fmt"
	"strings"
)

type ReviewModel struct {
	ops       *OpQueue
	cursorIdx int
}

func NewReviewModel(ops *OpQueue) ReviewModel {
	return ReviewModel{ops: ops}
}

func (r *ReviewModel) CursorDown() {
	if r.cursorIdx < r.ops.Len()-1 {
		r.cursorIdx++
	}
}

func (r *ReviewModel) CursorUp() {
	if r.cursorIdx > 0 {
		r.cursorIdx--
	}
}

func (r *ReviewModel) Toggle() {
	r.ops.Toggle(r.cursorIdx)
}

func (r *ReviewModel) CheckAll() {
	r.ops.CheckAll()
}

func (r *ReviewModel) UncheckAll() {
	r.ops.UncheckAll()
}

func (r ReviewModel) View(width, height int) string {
	var sb strings.Builder

	title := fmt.Sprintf("Pending Changes (%d)", r.ops.Len())
	sb.WriteString(detailHeaderStyle.Render(title))
	sb.WriteString("\n\n")

	ops := r.ops.Ops()
	if len(ops) == 0 {
		sb.WriteString(helpStyle.Render("  No pending changes."))
		return sb.String()
	}

	for i, op := range ops {
		cursor := "  "
		if i == r.cursorIdx {
			cursor = cursorStyle.Render("► ")
		}

		check := "[ ]"
		if op.Checked {
			check = "[x]"
		}

		// Color GitHub ops differently from config ops
		desc := op.Description()
		var styled string
		if op.IsGitHubOp() {
			styled = issueTitleStyle.Render(desc)
		} else {
			styled = helpStyle.Render(desc)
		}

		sb.WriteString(fmt.Sprintf("%s %s %s\n", cursor, check, styled))
	}

	sb.WriteString("\n")
	checkedCount := len(r.ops.CheckedOps())
	sb.WriteString(statusBarStyle.Render(
		fmt.Sprintf("  %d of %d selected", checkedCount, r.ops.Len()),
	))
	sb.WriteString("\n\n")
	sb.WriteString(helpStyle.Render("  Enter=toggle  a=all  n=none  y=push & quit  d=discard & quit  Esc=back"))

	return sb.String()
}
