package ship

import (
	"strings"
	"testing"

	"github.com/autumnust/tack/internal/model"
)

func TestRenderTemplate_FromTodoBody(t *testing.T) {
	todo := model.TodoItem{Text: "wire up the syncer\nwith retries"}
	out := RenderTemplate(todo, RenderOptions{
		DefaultRepo: "kumo-ai/kumo",
		DefaultHost: "local",
	})

	for _, want := range []string{
		"# Title",
		"# Body",
		"wire up the syncer",
		"with retries",
		"# Repo",
		"kumo-ai/kumo",
		"# Issue",
		"new",
		"# Epic",
		"# Host",
		"local",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered template missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestParse_HappyPath_NewIssue(t *testing.T) {
	tpl := `# Title
Make redis fast

# Body
We need to debounce writes.

# Repo
kumo-ai/kumo

# Issue
new

# Epic


# Host
local
`
	form, err := Parse(tpl)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if form.Title != "Make redis fast" {
		t.Errorf("Title = %q", form.Title)
	}
	if !strings.Contains(form.Body, "debounce") {
		t.Errorf("Body missing content: %q", form.Body)
	}
	if form.Repo != "kumo-ai/kumo" {
		t.Errorf("Repo = %q", form.Repo)
	}
	if !form.IssueNew {
		t.Errorf("expected IssueNew=true")
	}
	if form.IssueNum != 0 {
		t.Errorf("IssueNum = %d, want 0 for new", form.IssueNum)
	}
	if form.EpicNum != 0 || form.EpicRepo != "" {
		t.Errorf("expected empty Epic, got %d / %q", form.EpicNum, form.EpicRepo)
	}
	if form.Host != "local" {
		t.Errorf("Host = %q", form.Host)
	}
}

func TestParse_AttachExistingIssue(t *testing.T) {
	tpl := `# Title


# Body
attaching to existing.

# Repo
kumo-ai/kumo

# Issue
#28151

# Epic


# Host
aws
`
	form, err := Parse(tpl)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if form.IssueNew {
		t.Errorf("expected IssueNew=false")
	}
	if form.IssueNum != 28151 {
		t.Errorf("IssueNum = %d", form.IssueNum)
	}
	if form.Host != "aws" {
		t.Errorf("Host = %q", form.Host)
	}
}

func TestParse_EpicVariants(t *testing.T) {
	cases := []struct {
		name     string
		epic     string
		wantNum  int
		wantRepo string
		wantErr  bool
	}{
		{"empty", "", 0, "", false},
		{"bare hash", "#42", 42, "", false},
		{"cross-repo", "owner/repo#42", 42, "owner/repo", false},
		{"malformed no hash", "owner/repo42", 0, "", true},
		{"malformed bad num", "#abc", 0, "", true},
		{"malformed empty hash", "#", 0, "", true},
	}

	base := func(epic string) string {
		return `# Title


# Body
b

# Repo
kumo-ai/kumo

# Issue
#1

# Epic
` + epic + `

# Host
local
`
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form, err := Parse(base(tc.epic))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for epic %q, got nil", tc.epic)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if form.EpicNum != tc.wantNum {
				t.Errorf("EpicNum = %d, want %d", form.EpicNum, tc.wantNum)
			}
			if form.EpicRepo != tc.wantRepo {
				t.Errorf("EpicRepo = %q, want %q", form.EpicRepo, tc.wantRepo)
			}
		})
	}
}

func TestParse_RejectsMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		tpl  string
	}{
		{"missing body when new", `# Title
Foo

# Body


# Repo
kumo-ai/kumo

# Issue
new

# Epic


# Host
local
`},
		{"missing title when new", `# Title


# Body
b

# Repo
kumo-ai/kumo

# Issue
new

# Epic


# Host
local
`},
		{"missing repo", `# Title
T

# Body
B

# Repo


# Issue
new

# Epic


# Host
local
`},
		{"bad repo format", `# Title
T

# Body
B

# Repo
not-a-slash-form

# Issue
new

# Epic


# Host
local
`},
		{"bad issue value", `# Title


# Body
b

# Repo
kumo-ai/kumo

# Issue
nope

# Epic


# Host
local
`},
		{"missing host", `# Title


# Body
b

# Repo
kumo-ai/kumo

# Issue
#5

# Epic


# Host

`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.tpl); err == nil {
				t.Errorf("expected error, got nil for %s", tc.name)
			}
		})
	}
}

func TestRoundTrip_ExistingIssue(t *testing.T) {
	// Render with all defaults, parse back, fields should match
	todo := model.TodoItem{Text: "round trip"}
	rendered := RenderTemplate(todo, RenderOptions{
		DefaultRepo: "kumo-ai/kumo",
		DefaultHost: "local",
	})
	// User typically fills Title; simulate that for new-issue case
	rendered = strings.Replace(rendered, "# Title\n\n", "# Title\nMy title\n\n", 1)

	form, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if form.Title != "My title" {
		t.Errorf("Title = %q", form.Title)
	}
	if !strings.Contains(form.Body, "round trip") {
		t.Errorf("Body lost text: %q", form.Body)
	}
	if form.Repo != "kumo-ai/kumo" {
		t.Errorf("Repo = %q", form.Repo)
	}
	if !form.IssueNew {
		t.Errorf("expected new")
	}
	if form.Host != "local" {
		t.Errorf("Host = %q", form.Host)
	}
}
