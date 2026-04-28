// Package ship implements the :ship workflow: turning a Today todo into
// (issue + project + tmux session) anchored to a GitHub issue.
//
// The package is split into pure pieces (template parse/render) and
// side-effecting pieces (issue ops, ssh) wired through interfaces in
// runner.go so the whole flow can be unit-tested without network.
package ship

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/autumnust/tack/internal/model"
)

// ShipForm is the parsed user-facing template the editor produces.
type ShipForm struct {
	Title    string
	Body     string
	Repo     string // <owner>/<repo>
	IssueNew bool   // true if Issue == "new"
	IssueNum int    // populated when !IssueNew
	EpicNum  int    // 0 = no epic
	EpicRepo string // empty = same as Repo
	Host     string // "local" or an ssh host alias
}

// RenderOptions tunes the template render. Both fields fall back to
// sensible defaults when empty.
type RenderOptions struct {
	DefaultRepo string // e.g. "kumo-ai/kumo"
	DefaultHost string // e.g. "local"
}

// RenderTemplate produces the template text the user edits. The Body is
// pre-filled from the todo text; everything else is defaulted.
func RenderTemplate(todo model.TodoItem, opts RenderOptions) string {
	repo := opts.DefaultRepo
	if repo == "" {
		repo = "kumo-ai/kumo"
	}
	host := opts.DefaultHost
	if host == "" {
		host = "local"
	}
	return fmt.Sprintf(`# Title


# Body
%s

# Repo
%s

# Issue
new

# Epic


# Host
%s
`, todo.Text, repo, host)
}

// section names recognized in the template.
var sectionHeaders = []string{"Title", "Body", "Repo", "Issue", "Epic", "Host"}

// Parse extracts a ShipForm from the template text.
func Parse(text string) (ShipForm, error) {
	sections, err := splitSections(text)
	if err != nil {
		return ShipForm{}, err
	}

	form := ShipForm{
		Title: strings.TrimSpace(sections["Title"]),
		Body:  strings.TrimRight(strings.TrimLeft(sections["Body"], "\n"), "\n"),
		Repo:  strings.TrimSpace(sections["Repo"]),
		Host:  strings.TrimSpace(sections["Host"]),
	}

	// Issue
	issueRaw := strings.TrimSpace(sections["Issue"])
	switch {
	case issueRaw == "":
		return form, fmt.Errorf("Issue is required (use 'new' or '#<N>')")
	case strings.EqualFold(issueRaw, "new"):
		form.IssueNew = true
	case strings.HasPrefix(issueRaw, "#"):
		num, err := strconv.Atoi(strings.TrimPrefix(issueRaw, "#"))
		if err != nil || num <= 0 {
			return form, fmt.Errorf("Issue: invalid issue number %q", issueRaw)
		}
		form.IssueNum = num
	default:
		return form, fmt.Errorf("Issue must be 'new' or '#<N>', got %q", issueRaw)
	}

	// Epic — empty, #N, or owner/repo#N
	epicRaw := strings.TrimSpace(sections["Epic"])
	if epicRaw != "" {
		epicNum, epicRepo, err := parseEpic(epicRaw)
		if err != nil {
			return form, err
		}
		form.EpicNum = epicNum
		form.EpicRepo = epicRepo
	}

	// Required field validation
	if form.Repo == "" {
		return form, fmt.Errorf("Repo is required")
	}
	if !repoSlashRE.MatchString(form.Repo) {
		return form, fmt.Errorf("Repo must be in <owner>/<repo> form, got %q", form.Repo)
	}
	if form.Host == "" {
		return form, fmt.Errorf("Host is required (use 'local' or an ssh host alias)")
	}
	if form.IssueNew {
		if form.Title == "" {
			return form, fmt.Errorf("Title is required when Issue: new")
		}
		if strings.TrimSpace(form.Body) == "" {
			return form, fmt.Errorf("Body is required when Issue: new")
		}
	}

	return form, nil
}

var (
	repoSlashRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	bareEpicRE   = regexp.MustCompile(`^#(\d+)$`)
	crossEpicRE  = regexp.MustCompile(`^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)#(\d+)$`)
	headerLineRE = regexp.MustCompile(`^# +(.+)\s*$`)
)

func parseEpic(raw string) (int, string, error) {
	if m := bareEpicRE.FindStringSubmatch(raw); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, "", nil
	}
	if m := crossEpicRE.FindStringSubmatch(raw); m != nil {
		n, _ := strconv.Atoi(m[2])
		return n, m[1], nil
	}
	return 0, "", fmt.Errorf("Epic: must be empty, '#<N>', or '<owner>/<repo>#<N>', got %q", raw)
}

// splitSections walks the lines and groups content under each `# Header`
// line. Required headers must all appear; extras are ignored.
func splitSections(text string) (map[string]string, error) {
	sections := map[string]string{}
	current := ""
	var buf strings.Builder

	flush := func() {
		if current != "" {
			sections[current] = buf.String()
		}
		buf.Reset()
	}

	known := map[string]bool{}
	for _, h := range sectionHeaders {
		known[h] = true
	}

	for _, line := range strings.Split(text, "\n") {
		if m := headerLineRE.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if known[name] {
				flush()
				current = name
				continue
			}
		}
		if current == "" {
			// content before any known header is ignored
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	flush()

	for _, h := range sectionHeaders {
		if _, ok := sections[h]; !ok {
			return nil, fmt.Errorf("missing section: # %s", h)
		}
	}
	return sections, nil
}
