package ship

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// IssueRef identifies a GitHub issue across the operations a ship flow
// performs. NodeID is needed for GraphQL (project add, sub-issue link);
// URL is what `gh project item-add` accepts.
type IssueRef struct {
	Number int
	Repo   string // <owner>/<repo>
	URL    string
	NodeID string
}

// CreateIssue runs `gh issue create` and parses the resulting issue
// number from the URL the CLI prints on success.
func CreateIssue(gh GHRunner, repo, title, body string) (IssueRef, error) {
	out, err := gh.Run("issue", "create", "-R", repo, "-t", title, "-b", body)
	if err != nil {
		return IssueRef{}, err
	}
	url := strings.TrimSpace(string(out))
	num, err := parseIssueNumber(url)
	if err != nil {
		return IssueRef{}, fmt.Errorf("parse issue URL %q: %w", url, err)
	}
	// Resolve node ID for downstream GraphQL calls.
	ref := IssueRef{Number: num, Repo: repo, URL: url}
	if nodeID, err := fetchNodeID(gh, repo, num); err == nil {
		ref.NodeID = nodeID
	}
	return ref, nil
}

var issueURLNumRE = regexp.MustCompile(`/issues/(\d+)`)

func parseIssueNumber(url string) (int, error) {
	m := issueURLNumRE.FindStringSubmatch(url)
	if m == nil {
		return 0, fmt.Errorf("no /issues/<N> in URL")
	}
	return strconv.Atoi(m[1])
}

// ValidateIssue confirms an issue exists in the given repo and returns
// its IssueRef. Wraps `gh issue view --json number,title,url,id`.
func ValidateIssue(gh GHRunner, repo string, num int) (IssueRef, error) {
	out, err := gh.Run("issue", "view", strconv.Itoa(num), "-R", repo,
		"--json", "number,title,url,id")
	if err != nil {
		return IssueRef{}, fmt.Errorf("issue #%d not found in %s: %w", num, repo, err)
	}
	var raw struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return IssueRef{}, fmt.Errorf("parse gh output: %w", err)
	}
	if raw.Number != num {
		return IssueRef{}, fmt.Errorf("issue number mismatch: want %d, got %d", num, raw.Number)
	}
	return IssueRef{
		Number: raw.Number,
		Repo:   repo,
		URL:    raw.URL,
		NodeID: raw.ID,
	}, nil
}

func fetchNodeID(gh GHRunner, repo string, num int) (string, error) {
	out, err := gh.Run("issue", "view", strconv.Itoa(num), "-R", repo, "--json", "id")
	if err != nil {
		return "", err
	}
	var raw struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return "", err
	}
	return raw.ID, nil
}

// AddToProject adds an issue to a GitHub Project (v2). Uses `gh project
// item-add`, which takes the project number and the issue URL.
func AddToProject(gh GHRunner, projectURL string, ref IssueRef) error {
	owner, projNum, err := parseProjectURL(projectURL)
	if err != nil {
		return err
	}
	args := []string{"project", "item-add", strconv.Itoa(projNum), "--owner", owner, "--url", ref.URL}
	if _, err := gh.Run(args...); err != nil {
		return fmt.Errorf("project item-add: %w", err)
	}
	return nil
}

var projectURLRE = regexp.MustCompile(`github\.com/(?:orgs|users)/([^/]+)/projects/(\d+)`)

func parseProjectURL(url string) (string, int, error) {
	m := projectURLRE.FindStringSubmatch(url)
	if m == nil {
		return "", 0, fmt.Errorf("not a GitHub project URL: %q", url)
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, err
	}
	return m[1], n, nil
}

// LinkSubIssue idempotently links child as a sub-issue of parent via the
// GraphQL addSubIssue mutation. If child already has parent: no-op. If
// child has a *different* parent: error (no silent re-parent).
func LinkSubIssue(gh GHRunner, parent, child IssueRef) error {
	if parent.NodeID == "" || child.NodeID == "" {
		return fmt.Errorf("LinkSubIssue: both parent and child need NodeID")
	}

	existingParentID, err := fetchSubIssueParent(gh, child.NodeID)
	if err != nil {
		return fmt.Errorf("query existing parent: %w", err)
	}
	switch existingParentID {
	case "":
		// no parent yet — proceed to mutate
	case parent.NodeID:
		// already linked to this parent — no-op
		return nil
	default:
		return fmt.Errorf("issue #%d already has a different parent (id=%s); refusing to re-parent", child.Number, existingParentID)
	}

	mutation := `mutation($parent: ID!, $child: ID!) {
  addSubIssue(input: {issueId: $parent, subIssueId: $child}) {
    issue { id }
  }
}`
	args := []string{
		"api", "graphql",
		"-H", "GraphQL-Features: sub_issues",
		"-f", "query=" + mutation,
		"-f", "parent=" + parent.NodeID,
		"-f", "child=" + child.NodeID,
	}
	if _, err := gh.Run(args...); err != nil {
		return fmt.Errorf("addSubIssue: %w", err)
	}
	return nil
}

// fetchSubIssueParent returns the current parent's NodeID (or "") for an
// issue. Uses the parent field from the sub_issues GraphQL preview.
func fetchSubIssueParent(gh GHRunner, childNodeID string) (string, error) {
	query := `query($id: ID!) {
  node(id: $id) {
    ... on Issue { parent { id number } }
  }
}`
	args := []string{
		"api", "graphql",
		"-H", "GraphQL-Features: sub_issues",
		"-f", "query=" + query,
		"-f", "id=" + childNodeID,
	}
	out, err := gh.Run(args...)
	if err != nil {
		return "", err
	}
	var raw struct {
		Data struct {
			Node struct {
				Parent *struct {
					ID     string `json:"id"`
					Number int    `json:"number"`
				} `json:"parent"`
			} `json:"node"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return "", err
	}
	if raw.Data.Node.Parent == nil {
		return "", nil
	}
	return raw.Data.Node.Parent.ID, nil
}
