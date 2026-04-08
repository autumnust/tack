package github

import (
	"encoding/json"
	"fmt"

	"github.com/autumnust/tack/internal/model"
)

const updateFieldMutation = `
mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $projectId
    itemId: $itemId
    fieldId: $fieldId
    value: { singleSelectOptionId: $optionId }
  }) {
    projectV2Item { id }
  }
}
`

const addCommentMutation = `
mutation($subjectId: ID!, $body: String!) {
  addComment(input: { subjectId: $subjectId, body: $body }) {
    commentEdge {
      node { id }
    }
  }
}
`

func (c *Client) MoveItem(project *model.Project, itemID string, statusName string) error {
	var optionID string
	for _, opt := range project.StatusField.Options {
		if opt.Name == statusName {
			optionID = opt.ID
			break
		}
	}
	if optionID == "" {
		available := make([]string, len(project.StatusField.Options))
		for i, o := range project.StatusField.Options {
			available[i] = o.Name
		}
		return fmt.Errorf("unknown status %q (available: %v)", statusName, available)
	}

	_, err := c.graphql(updateFieldMutation, map[string]any{
		"projectId": project.ID,
		"itemId":    itemID,
		"fieldId":   project.StatusField.ID,
		"optionId":  optionID,
	})
	return err
}

const addAssigneeMutation = `
mutation($issueId: ID!, $assigneeIds: [ID!]!) {
  addAssigneesToAssignable(input: { assignableId: $issueId, assigneeIds: $assigneeIds }) {
    assignable {
      ... on Issue { id }
    }
  }
}
`

const getUserIDQuery = `
query($login: String!) {
  user(login: $login) {
    id
  }
}
`

func (c *Client) AssignIssue(issueNodeID string, login string) error {
	// Resolve login to user node ID
	data, err := c.graphql(getUserIDQuery, map[string]any{"login": login})
	if err != nil {
		return fmt.Errorf("lookup user %s: %w", login, err)
	}
	var userResp struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &userResp); err != nil {
		return fmt.Errorf("parse user response: %w", err)
	}
	if userResp.User.ID == "" {
		return fmt.Errorf("user %s not found", login)
	}

	_, err = c.graphql(addAssigneeMutation, map[string]any{
		"issueId":     issueNodeID,
		"assigneeIds": []string{userResp.User.ID},
	})
	return err
}

func (c *Client) AddComment(issueNodeID string, body string) error {
	_, err := c.graphql(addCommentMutation, map[string]any{
		"subjectId": issueNodeID,
		"body":      body,
	})
	return err
}
