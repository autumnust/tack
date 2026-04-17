package github

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/autumnust/tack/internal/model"
)

func TestParseProjectURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    projectRef
		wantErr bool
	}{
		{
			name:   "org project",
			rawURL: "https://github.com/orgs/acme/projects/12",
			want:   projectRef{ownerType: ownerOrg, owner: "acme", number: 12},
		},
		{
			name:   "user project",
			rawURL: "https://github.com/users/alice/projects/7",
			want:   projectRef{ownerType: ownerUser, owner: "alice", number: 7},
		},
		{
			name:    "bad path",
			rawURL:  "https://github.com/acme/projects/12",
			wantErr: true,
		},
		{
			name:    "bad number",
			rawURL:  "https://github.com/orgs/acme/projects/not-a-number",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProjectURL(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseProjectURL(%q) error = nil, want non-nil", tt.rawURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProjectURL(%q) error = %v", tt.rawURL, err)
			}
			if got != tt.want {
				t.Fatalf("parseProjectURL(%q) = %#v, want %#v", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestParseItems(t *testing.T) {
	raw := map[string]any{
		"node": map[string]any{
			"items": map[string]any{
				"pageInfo": map[string]any{
					"hasNextPage": true,
					"endCursor":   "cursor-2",
				},
				"nodes": []any{
					map[string]any{
						"id": "pi-101",
						"content": map[string]any{
							"id":     "n-101",
							"title":  "Issue 101",
							"number": 101,
							"url":    "https://github.com/acme/repo/issues/101",
							"body":   "hello",
							"state":  "OPEN",
							"assignees": map[string]any{"nodes": []any{
								map[string]any{"login": "alice"},
								map[string]any{"login": "bob"},
							}},
							"labels": map[string]any{"nodes": []any{
								map[string]any{"name": "area:backend"},
							}},
							"trackedInIssues": map[string]any{"nodes": []any{
								map[string]any{
									"title":  "Epic 100",
									"number": 100,
									"url":    "https://github.com/acme/repo/issues/100",
									"repository": map[string]any{
										"nameWithOwner": "acme/repo",
									},
								},
							}},
							"repository": map[string]any{"nameWithOwner": "acme/repo"},
							"comments": map[string]any{"nodes": []any{
								map[string]any{
									"body":      "first",
									"author":    map[string]any{"login": "alice"},
									"createdAt": "2026-04-10T12:00:00Z",
								},
								map[string]any{
									"body":      "second",
									"author":    map[string]any{"login": "bob"},
									"createdAt": "not-a-time",
								},
							}},
						},
						"fieldValues": map[string]any{"nodes": []any{
							map[string]any{
								"name":  "In Progress",
								"field": map[string]any{"id": "sf-1", "name": "Status"},
							},
						}},
					},
					map[string]any{
						"id": "pi-draft",
						"content": map[string]any{
							"title": "",
						},
					},
				},
			},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	items, pi, err := parseItems(data, "Status")
	if err != nil {
		t.Fatalf("parseItems() error = %v", err)
	}
	if !pi.hasNext || pi.endCursor != "cursor-2" {
		t.Fatalf("pageInfo = %#v, want hasNext=true endCursor=cursor-2", pi)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}

	item := items[0]
	if item.ID != "n-101" || item.ItemID != "pi-101" {
		t.Fatalf("item ids = (%q, %q), want (n-101, pi-101)", item.ID, item.ItemID)
	}
	if item.Status != "In Progress" {
		t.Fatalf("item.Status = %q, want In Progress", item.Status)
	}
	if !reflect.DeepEqual(item.Assignees, []string{"alice", "bob"}) {
		t.Fatalf("item.Assignees = %v", item.Assignees)
	}
	if !reflect.DeepEqual(item.Labels, []string{"area:backend"}) {
		t.Fatalf("item.Labels = %v", item.Labels)
	}
	if item.Parent == nil || item.Parent.Number != 100 || item.Parent.Title != "Epic 100" {
		t.Fatalf("item.Parent = %#v, want parent #100 Epic 100", item.Parent)
	}
	if len(item.Comments) != 2 {
		t.Fatalf("len(item.Comments) = %d, want 2", len(item.Comments))
	}
	if item.Comments[0].CreatedAt.IsZero() {
		t.Fatal("first comment should parse CreatedAt")
	}
	if !item.Comments[1].CreatedAt.IsZero() {
		t.Fatal("second comment should leave CreatedAt zero when timestamp is invalid")
	}
}

func TestResolveParentsFromSubIssues(t *testing.T) {
	items := []model.ProjectItem{
		{Number: 100, Title: "Epic 100"},
		{Number: 101, Title: "Child 101"},
		{Number: 200, Title: "Already linked", Parent: &model.ParentRef{Number: 999, Title: "Existing"}},
	}
	subIssueMap := map[int][]model.SubIssue{
		100: {
			{Number: 101, Title: "Child 101"},
		},
		300: {
			{Number: 200, Title: "Child 200"},
		},
	}

	ResolveParentsFromSubIssues(items, subIssueMap, "acme/repo")

	if items[1].Parent == nil {
		t.Fatal("child item 101 should have parent assigned")
	}
	if items[1].Parent.Number != 100 || items[1].Parent.Title != "Epic 100" || items[1].Parent.Repo != "acme/repo" {
		t.Fatalf("item 101 parent = %#v, want parent #100 with title Epic 100", items[1].Parent)
	}
	if items[2].Parent.Number != 999 || items[2].Parent.Title != "Existing" {
		t.Fatalf("existing parent should not be overwritten, got %#v", items[2].Parent)
	}
}
