package github

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rewriteTransport struct {
	baseURL string
	base    http.RoundTripper
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := t.baseURL + req.URL.Path
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	clone := req.Clone(req.Context())
	urlReq, err := http.NewRequestWithContext(req.Context(), req.Method, target, req.Body)
	if err != nil {
		return nil, err
	}
	urlReq.Header = clone.Header.Clone()
	return t.base.RoundTrip(urlReq)
}

func testClient(server *httptest.Server) *Client {
	return &Client{
		token: "test-token",
		httpClient: &http.Client{
			Transport: rewriteTransport{
				baseURL: server.URL,
				base:    http.DefaultTransport,
			},
		},
	}
}

func TestGraphQL_SendsHeadersAndReturnsData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("path = %q, want /graphql", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization header = %q, want Bearer test-token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if !strings.Contains(string(body), `"query":"query Test { viewer { login } }"`) {
			t.Fatalf("request body = %s, missing query", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"alice"}}}`))
	}))
	defer server.Close()

	data, err := testClient(server).graphql("query Test { viewer { login } }", nil)
	if err != nil {
		t.Fatalf("graphql() error = %v", err)
	}
	if string(data) != `{"viewer":{"login":"alice"}}` {
		t.Fatalf("graphql() data = %s", string(data))
	}
}

func TestGraphQL_PropagatesGraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"bad query"}]}`))
	}))
	defer server.Close()

	_, err := testClient(server).graphql("query Test {}", nil)
	if err == nil || !strings.Contains(err.Error(), "bad query") {
		t.Fatalf("graphql() error = %v, want GraphQL error containing bad query", err)
	}
}

func TestGraphQL_PropagatesHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusForbidden)
	}))
	defer server.Close()

	_, err := testClient(server).graphql("query Test {}", nil)
	if err == nil || !strings.Contains(err.Error(), "GitHub API returned 403") {
		t.Fatalf("graphql() error = %v, want HTTP status failure", err)
	}
}

func TestRestGet_SendsHeadersAndReturnsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/test/repo/issues/1" {
			t.Fatalf("path = %q, want /repos/test/repo/issues/1", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization header = %q, want Bearer test-token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Fatalf("Accept = %q, want application/vnd.github+json", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	data, err := testClient(server).RestGet("/repos/test/repo/issues/1")
	if err != nil {
		t.Fatalf("RestGet() error = %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("RestGet() body = %s", string(data))
	}
}

func TestRestGet_PropagatesHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()

	_, err := testClient(server).RestGet("/repos/test/repo/issues/1")
	if err == nil || !strings.Contains(err.Error(), "GitHub API returned 404") {
		t.Fatalf("RestGet() error = %v, want HTTP status failure", err)
	}
}
