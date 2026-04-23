package planning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRESTBackendIncrParsesStringResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []string
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(args) != 2 || args[0] != "INCR" || args[1] != "tack:plan:rev" {
			t.Fatalf("unexpected command: %v", args)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "7"})
	}))
	defer srv.Close()

	b := newRESTBackend(srv.URL, "token")
	got, err := b.Incr(context.Background(), "tack:plan:rev")
	if err != nil {
		t.Fatal(err)
	}
	if got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}

func TestRESTBackendDoReturnsErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "boom"})
	}))
	defer srv.Close()

	b := newRESTBackend(srv.URL, "token")
	if err := b.Set(context.Background(), "tack:plan", "{}"); err == nil || err.Error() != "boom" {
		t.Fatalf("expected error field to surface, got %v", err)
	}
}
