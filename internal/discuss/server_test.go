package discuss

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubAnthropic returns an httptest.Server that emits a fixed
// SSE stream — a few text deltas followed by message_stop.
func stubAnthropic(t *testing.T, deltas []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			http.Error(w, "missing key", http.StatusUnauthorized)
			return
		}
		// Decode and assert a model + system + at least one user message.
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["stream"] != true {
			http.Error(w, "stream=true expected", http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		flusher := w.(http.Flusher)
		emit := func(line string) {
			fmt.Fprintln(w, line)
			fmt.Fprintln(w)
			flusher.Flush()
		}
		emit(`event: message_start`)
		emit(`data: {"type":"message_start"}`)
		for _, d := range deltas {
			payload, _ := json.Marshal(map[string]any{
				"type":  "content_block_delta",
				"delta": map[string]string{"type": "text_delta", "text": d},
			})
			emit("data: " + string(payload))
		}
		emit(`data: {"type":"message_stop"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestServer_TurnRoundTrip(t *testing.T) {
	stub := stubAnthropic(t, []string{"hello", " ", "world"})

	dir := t.TempDir()
	id := NewID()
	srv, err := StartServer(Options{
		APIKey:   "test-key",
		Model:    "claude-test",
		Seeds:    []Seed{{NoteID: "n1", Text: "what should i do?", CreatedAt: time.Now()}},
		LogPath:  filepath.Join(dir, "discussions", string(id)+".jsonl"),
		Endpoint: stub.URL,
	})
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}()

	// Hit /api/state — should reflect the seed.
	resp, err := http.Get(srv.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state: %v", err)
	}
	var state stateResponse
	_ = json.NewDecoder(resp.Body).Decode(&state)
	resp.Body.Close()
	if len(state.SeedNotes) != 1 || state.SeedNotes[0].Text != "what should i do?" {
		t.Errorf("seed not surfaced: %+v", state.SeedNotes)
	}

	// Send a turn and read the SSE stream.
	turnReq, _ := http.NewRequest(http.MethodPost, srv.URL()+"/api/turn",
		strings.NewReader(`{"text":"hi"}`))
	turnReq.Header.Set("content-type", "application/json")
	turnResp, err := http.DefaultClient.Do(turnReq)
	if err != nil {
		t.Fatalf("POST /api/turn: %v", err)
	}
	defer turnResp.Body.Close()
	if turnResp.StatusCode != 200 {
		body, _ := io.ReadAll(turnResp.Body)
		t.Fatalf("turn status %d: %s", turnResp.StatusCode, body)
	}
	var assembled bytes.Buffer
	sc := bufio.NewScanner(turnResp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Msg  string `json:"msg"`
		}
		_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev)
		if ev.Type == "text" {
			assembled.WriteString(ev.Text)
		}
	}
	if assembled.String() != "hello world" {
		t.Errorf("assembled stream: got %q, want %q", assembled.String(), "hello world")
	}

	// Log should have created + user + assistant events.
	events, err := srv.log.Read()
	if err != nil {
		t.Fatalf("log read: %v", err)
	}
	wantOps := []Op{OpCreated, OpUserMsg, OpAssistant}
	if len(events) < len(wantOps) {
		t.Fatalf("expected at least %d events, got %d", len(wantOps), len(events))
	}
	for i, op := range wantOps {
		if events[i].Op != op {
			t.Errorf("event %d op: got %s, want %s", i, events[i].Op, op)
		}
	}
	if events[2].Text != "hello world" {
		t.Errorf("assistant text: %q", events[2].Text)
	}
}

func TestServer_CloseAppendsEvent(t *testing.T) {
	stub := stubAnthropic(t, []string{"x"})
	dir := t.TempDir()
	id := NewID()
	srv, err := StartServer(Options{
		APIKey:   "test-key",
		Seeds:    []Seed{{NoteID: "n1", Text: "seed"}},
		LogPath:  filepath.Join(dir, "discussions", string(id)+".jsonl"),
		Endpoint: stub.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL()+"/api/close", "", nil)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	resp.Body.Close()

	// Wait briefly for the deferred shutdown.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.IsClosed() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !srv.IsClosed() {
		t.Fatal("server should be closed")
	}

	events, _ := srv.log.Read()
	var sawClose bool
	for _, e := range events {
		if e.Op == OpClosed {
			sawClose = true
		}
	}
	if !sawClose {
		t.Errorf("expected OpClosed event in log; events=%+v", events)
	}
}

func TestServer_RootServesHTML(t *testing.T) {
	dir := t.TempDir()
	id := NewID()
	srv, err := StartServer(Options{
		APIKey:  "test-key",
		Seeds:   []Seed{{NoteID: "n1", Text: "seed"}},
		LogPath: filepath.Join(dir, "discussions", string(id)+".jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}()

	resp, err := http.Get(srv.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("content-type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type: got %q, want text/html…", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, hook := range []string{`id="scrollback"`, `id="input"`, `id="send-btn"`, `id="close-btn"`} {
		if !strings.Contains(string(body), hook) {
			t.Errorf("html missing %q", hook)
		}
	}
}

func TestStartServer_RejectsMissingKey(t *testing.T) {
	_, err := StartServer(Options{LogPath: "/tmp/x.jsonl"})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}
