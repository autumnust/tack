package github

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/autumnust/tack/internal/model"
)

type projectOwnerType int

const (
	ownerOrg projectOwnerType = iota
	ownerUser
)

type projectRef struct {
	ownerType projectOwnerType
	owner     string
	number    int
}

func parseProjectURL(rawURL string) (projectRef, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return projectRef{}, fmt.Errorf("invalid project URL: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Expected: orgs/<org>/projects/<num> or users/<user>/projects/<num>
	if len(parts) < 4 || parts[2] != "projects" {
		return projectRef{}, fmt.Errorf("expected URL like https://github.com/orgs/ORG/projects/NUM, got: %s", rawURL)
	}
	num, err := strconv.Atoi(parts[3])
	if err != nil {
		return projectRef{}, fmt.Errorf("invalid project number %q: %w", parts[3], err)
	}
	ref := projectRef{owner: parts[1], number: num}
	switch parts[0] {
	case "orgs":
		ref.ownerType = ownerOrg
	case "users":
		ref.ownerType = ownerUser
	default:
		return projectRef{}, fmt.Errorf("expected 'orgs' or 'users' in URL path, got %q", parts[0])
	}
	return ref, nil
}

const fetchProjectQuery = `
query($owner: String!, $number: Int!) {
  %s(login: $owner) {
    projectV2(number: $number) {
      id
      title
      fields(first: 30) {
        nodes {
          ... on ProjectV2SingleSelectField {
            id
            name
            options {
              id
              name
            }
          }
        }
      }
    }
  }
}
`

const fetchItemsQuery = `
query($projectId: ID!, $cursor: String) {
  node(id: $projectId) {
    ... on ProjectV2 {
      items(first: 100, after: $cursor) {
        pageInfo {
          hasNextPage
          endCursor
        }
        nodes {
          id
          content {
            ... on Issue {
              id
              title
              number
              url
              body
              state
              assignees(first: 10) {
                nodes { login }
              }
              labels(first: 10) {
                nodes { name }
              }
              trackedInIssues(first: 5) {
                nodes {
                  title
                  number
                  url
                  repository { nameWithOwner }
                }
              }
              repository { nameWithOwner }
              comments(last: 30) {
                nodes {
                  body
                  author { login }
                  createdAt
                }
              }
            }
            ... on PullRequest {
              id
              title
              number
              url
              body
              state
              assignees(first: 10) {
                nodes { login }
              }
              labels(first: 10) {
                nodes { name }
              }
              repository { nameWithOwner }
            }
          }
          fieldValues(first: 20) {
            nodes {
              ... on ProjectV2ItemFieldSingleSelectValue {
                name
                field {
                  ... on ProjectV2SingleSelectField {
                    id
                    name
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}
`

func (c *Client) FetchProject(projectURL string, statusFieldName string) (*model.Project, error) {
	ref, err := parseProjectURL(projectURL)
	if err != nil {
		return nil, err
	}

	ownerKind := "organization"
	if ref.ownerType == ownerUser {
		ownerKind = "user"
	}

	query := fmt.Sprintf(fetchProjectQuery, ownerKind)
	data, err := c.graphql(query, map[string]any{
		"owner":  ref.owner,
		"number": ref.number,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch project: %w", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse project response: %w", err)
	}

	var ownerData struct {
		ProjectV2 *struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Fields struct {
				Nodes []json.RawMessage `json:"nodes"`
			} `json:"fields"`
		} `json:"projectV2"`
	}

	ownerJSON := result[ownerKind]
	if err := json.Unmarshal(ownerJSON, &ownerData); err != nil {
		return nil, fmt.Errorf("parse owner data: %w", err)
	}

	if ownerData.ProjectV2 == nil {
		return nil, fmt.Errorf("project not found: %s", projectURL)
	}

	proj := &model.Project{
		ID:    ownerData.ProjectV2.ID,
		Title: ownerData.ProjectV2.Title,
	}

	// Find the status field
	for _, node := range ownerData.ProjectV2.Fields.Nodes {
		var field struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Options []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"options"`
		}
		if err := json.Unmarshal(node, &field); err != nil {
			continue
		}
		if field.Name == statusFieldName && field.ID != "" {
			proj.StatusField = model.FieldInfo{
				ID:   field.ID,
				Name: field.Name,
			}
			for _, opt := range field.Options {
				proj.StatusField.Options = append(proj.StatusField.Options, model.FieldOption{
					ID:   opt.ID,
					Name: opt.Name,
				})
			}
			break
		}
	}

	// Fetch all items with pagination
	var cursor *string
	for {
		vars := map[string]any{"projectId": proj.ID}
		if cursor != nil {
			vars["cursor"] = *cursor
		}
		itemData, err := c.graphql(fetchItemsQuery, vars)
		if err != nil {
			return nil, fmt.Errorf("fetch items: %w", err)
		}

		items, pageInfo, err := parseItems(itemData, statusFieldName)
		if err != nil {
			return nil, fmt.Errorf("parse items: %w", err)
		}
		proj.Items = append(proj.Items, items...)

		if !pageInfo.hasNext {
			break
		}
		cursor = &pageInfo.endCursor
	}

	return proj, nil
}

type pageInfo struct {
	hasNext   bool
	endCursor string
}

func parseItems(data json.RawMessage, statusFieldName string) ([]model.ProjectItem, pageInfo, error) {
	var resp struct {
		Node struct {
			Items struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					ID      string `json:"id"`
					Content struct {
						ID    string `json:"id"`
						Title string `json:"title"`
						Num   int    `json:"number"`
						URL   string `json:"url"`
						Body  string `json:"body"`
						State string `json:"state"`
						Assignees struct {
							Nodes []struct {
								Login string `json:"login"`
							} `json:"nodes"`
						} `json:"assignees"`
						Labels struct {
							Nodes []struct {
								Name string `json:"name"`
							} `json:"nodes"`
						} `json:"labels"`
						TrackedInIssues struct {
							Nodes []struct {
								Title string `json:"title"`
								Num   int    `json:"number"`
								URL   string `json:"url"`
								Repo  struct {
									NameWithOwner string `json:"nameWithOwner"`
								} `json:"repository"`
							} `json:"nodes"`
						} `json:"trackedInIssues"`
						Repo struct {
							NameWithOwner string `json:"nameWithOwner"`
						} `json:"repository"`
						Comments struct {
							Nodes []struct {
								Body   string `json:"body"`
								Author struct {
									Login string `json:"login"`
								} `json:"author"`
								CreatedAt string `json:"createdAt"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"content"`
					FieldValues struct {
						Nodes []struct {
							Name  string `json:"name"`
							Field struct {
								ID   string `json:"id"`
								Name string `json:"name"`
							} `json:"field"`
						} `json:"nodes"`
					} `json:"fieldValues"`
				} `json:"nodes"`
			} `json:"items"`
		} `json:"node"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, pageInfo{}, fmt.Errorf("unmarshal items: %w", err)
	}

	pi := pageInfo{
		hasNext:   resp.Node.Items.PageInfo.HasNextPage,
		endCursor: resp.Node.Items.PageInfo.EndCursor,
	}

	var items []model.ProjectItem
	for _, node := range resp.Node.Items.Nodes {
		if node.Content.Title == "" {
			continue // skip draft items with no content
		}

		item := model.ProjectItem{
			ID:     node.Content.ID,
			ItemID: node.ID,
			Title:  node.Content.Title,
			Number: node.Content.Num,
			URL:    node.Content.URL,
			Body:   node.Content.Body,
			State:  node.Content.State,
			Repo:   node.Content.Repo.NameWithOwner,
		}

		for _, a := range node.Content.Assignees.Nodes {
			item.Assignees = append(item.Assignees, a.Login)
		}
		for _, l := range node.Content.Labels.Nodes {
			item.Labels = append(item.Labels, l.Name)
		}

		// Status from project field
		for _, fv := range node.FieldValues.Nodes {
			if fv.Field.Name == statusFieldName {
				item.Status = fv.Name
				break
			}
		}

		// Parent (tracked-in) relationship
		if len(node.Content.TrackedInIssues.Nodes) > 0 {
			parent := node.Content.TrackedInIssues.Nodes[0]
			item.Parent = &model.ParentRef{
				Title:  parent.Title,
				Number: parent.Num,
				URL:    parent.URL,
				Repo:   parent.Repo.NameWithOwner,
			}
		}

		// Comments
		for _, cmt := range node.Content.Comments.Nodes {
			c := model.Comment{
				Author: cmt.Author.Login,
				Body:   cmt.Body,
			}
			if t, err := time.Parse(time.RFC3339, cmt.CreatedAt); err == nil {
				c.CreatedAt = t
			}
			item.Comments = append(item.Comments, c)
		}

		items = append(items, item)
	}

	return items, pi, nil
}

// FetchSubIssues fetches sub-issues for a given issue number using the REST API.
// Returns a map of parent issue number -> list of child issue references.
func (c *Client) FetchSubIssues(repo string, issueNumbers []int) (map[int][]model.SubIssue, error) {
	result := make(map[int][]model.SubIssue)
	for _, num := range issueNumbers {
		path := fmt.Sprintf("/repos/%s/issues/%d/sub_issues", repo, num)
		data, err := c.RestGet(path)
		if err != nil {
			// Sub-issues API might not be available or issue has none — skip
			continue
		}
		var subIssues []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			State  string `json:"state"`
			URL    string `json:"html_url"`
		}
		if err := json.Unmarshal(data, &subIssues); err != nil {
			continue
		}
		for _, si := range subIssues {
			result[num] = append(result[num], model.SubIssue{
				Number: si.Number,
				Title:  si.Title,
				State:  si.State,
				URL:    si.URL,
			})
		}
	}
	return result, nil
}

// ResolveParentsFromSubIssues sets the Parent field on project items
// based on sub-issue relationships from focus tickets.
func ResolveParentsFromSubIssues(items []model.ProjectItem, subIssueMap map[int][]model.SubIssue, repo string) {
	// Build reverse map: child number -> parent info
	childToParent := make(map[int]*model.ParentRef)
	for parentNum, children := range subIssueMap {
		for _, child := range children {
			// Find parent title from sub-issue map context
			childToParent[child.Number] = &model.ParentRef{
				Number: parentNum,
				Repo:   repo,
			}
		}
	}

	// Also build a number->title map from sub-issue parents
	// We need parent titles — fetch from items or sub-issue data
	parentTitles := make(map[int]string)
	for parentNum, children := range subIssueMap {
		// Check if parent is itself an item
		for _, item := range items {
			if item.Number == parentNum {
				parentTitles[parentNum] = item.Title
				break
			}
		}
		// If not found as item, we'll use the parent number
		if parentTitles[parentNum] == "" {
			_ = children // title will be set later
		}
	}

	// Set Parent on items that are children
	for i := range items {
		if ref, ok := childToParent[items[i].Number]; ok {
			if items[i].Parent == nil {
				items[i].Parent = &model.ParentRef{
					Number: ref.Number,
					Title:  parentTitles[ref.Number],
					Repo:   ref.Repo,
				}
			}
		}
	}
}
