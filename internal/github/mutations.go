package github

import (
	"fmt"

	"github.com/standup-kanban/standup-kanban/internal/model"
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

func (c *Client) AddComment(issueNodeID string, body string) error {
	_, err := c.graphql(addCommentMutation, map[string]any{
		"subjectId": issueNodeID,
		"body":      body,
	})
	return err
}
