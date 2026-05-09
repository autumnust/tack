package discuss

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"
)

// IdleTimeout auto-closes a discussion after this much inactivity.
const IdleTimeout = 30 * time.Minute

// Server hosts the chat UI for one discussion. Started fresh per
// `:discuss`; one process serves at most one Server at a time (the
// caller is responsible for that — the TUI tracks it).
type Server struct {
	id        ID
	log       *Log
	system    string
	model     string
	client    *AnthropicClient
	httpSrv   *http.Server
	listener  net.Listener
	url       string
	started   time.Time
	lastTouch time.Time
	mu        sync.Mutex
	closed    bool
	turns     int
	// History is the running message list sent on each /api/turn call.
	// The seed-note context is the first user turn (added at Start);
	// every additional turn appends one user + one assistant entry.
	history []Message
	// Seeds are the snapshotted seed notes, reflected back to the
	// browser via /api/state for UI rendering.
	seeds []Seed
}

// Options bundles the params for StartServer. Keeps the signature
// stable as we add knobs.
type Options struct {
	APIKey       string
	Model        string // optional; falls back to DefaultModel
	SystemPrompt string // optional; falls back to DefaultSystemPrompt
	Seeds        []Seed
	LogPath      string // absolute path to the discussion's .jsonl
	// Endpoint overrides AnthropicEndpoint for this server only (tests).
	Endpoint string
}

// StartServer mints a discussion id, writes the "created" event,
// listens on 127.0.0.1:<ephemeral>, and returns a started Server.
// The caller should open URL() in a browser.
func StartServer(opts Options) (*Server, error) {
	if opts.APIKey == "" {
		return nil, fmt.Errorf("discuss: anthropic api key is required")
	}
	if opts.LogPath == "" {
		return nil, fmt.Errorf("discuss: log path is required")
	}

	system := ResolveSystemPrompt(opts.SystemPrompt)
	model := ResolveModel(opts.Model)
	id := NewID()

	log, err := NewLog(opts.LogPath)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	created := Event{
		EventID:      NewID(),
		DiscussionID: id,
		Op:           OpCreated,
		TS:           now,
		SystemPrompt: system,
		Model:        model,
		SeedNotes:    opts.Seeds,
	}
	for _, s := range opts.Seeds {
		created.SeedNoteIDs = append(created.SeedNoteIDs, s.NoteID)
	}
	if err := log.Append(created); err != nil {
		return nil, err
	}

	client := NewAnthropicClient(opts.APIKey)
	if opts.Endpoint != "" {
		client.Endpoint = opts.Endpoint
	}

	s := &Server{
		id:        id,
		log:       log,
		system:    system,
		model:     model,
		client:    client,
		started:   now,
		lastTouch: now,
		seeds:     opts.Seeds,
	}

	// Build the seed user turn (the conversation primer the model sees).
	if len(opts.Seeds) > 0 {
		s.history = []Message{{Role: "user", Content: buildSeedTurn(opts.Seeds)}}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.listener = listener
	s.url = fmt.Sprintf("http://%s", listener.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/turn", s.handleTurn)
	mux.HandleFunc("/api/close", s.handleClose)

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		_ = s.httpSrv.Serve(listener)
	}()
	go s.idleWatcher()

	return s, nil
}

// URL returns the loopback URL the browser should hit.
func (s *Server) URL() string { return s.url }

// ID returns the discussion id.
func (s *Server) ID() ID { return s.id }

// Turns returns the number of completed user→assistant exchanges.
func (s *Server) Turns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns
}

// IsClosed reports whether the server has shut down.
func (s *Server) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Shutdown closes the listener and stops the http server. Idempotent.
// Appends a "closed" event to the log if not already closed.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.log.Append(Event{
		EventID:      NewID(),
		DiscussionID: s.id,
		Op:           OpClosed,
		TS:           time.Now().UTC(),
	})
	return s.httpSrv.Shutdown(ctx)
}

// touch resets the idle clock.
func (s *Server) touch() {
	s.mu.Lock()
	s.lastTouch = time.Now()
	s.mu.Unlock()
}

func (s *Server) idleWatcher() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		closed := s.closed
		idle := time.Since(s.lastTouch)
		s.mu.Unlock()
		if closed {
			return
		}
		if idle >= IdleTimeout {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Shutdown(ctx)
			cancel()
			return
		}
	}
}

// --- HTTP handlers ---

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	s.touch()
	data, err := fs.ReadFile(WebFS(), "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

type stateResponse struct {
	DiscussionID string             `json:"discussion_id"`
	SeedNotes    []Seed             `json:"seed_notes"`
	Transcript   []transcriptEntry  `json:"transcript"`
	Closed       bool               `json:"closed"`
	Started      string             `json:"started"`
	Model        string             `json:"model"`
}

type transcriptEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.touch()
	s.mu.Lock()
	closed := s.closed
	// Skip the first user turn in history (that's the seed primer; the
	// browser renders seeds separately as labeled blocks).
	transcript := []transcriptEntry{}
	for i, m := range s.history {
		if i == 0 && len(s.seeds) > 0 {
			continue
		}
		transcript = append(transcript, transcriptEntry{Role: m.Role, Text: m.Content})
	}
	resp := stateResponse{
		DiscussionID: string(s.id),
		SeedNotes:    s.seeds,
		Transcript:   transcript,
		Closed:       closed,
		Started:      s.started.Format(time.RFC3339),
		Model:        s.model,
	}
	s.mu.Unlock()
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleTurn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.touch()
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	body.Text = trimSpace(body.Text)
	if body.Text == "" {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "discussion closed", http.StatusGone)
		return
	}
	s.history = append(s.history, Message{Role: "user", Content: body.Text})
	hist := make([]Message, len(s.history))
	copy(hist, s.history)
	system := s.system
	model := s.model
	s.mu.Unlock()

	_ = s.log.Append(Event{
		EventID:      NewID(),
		DiscussionID: s.id,
		Op:           OpUserMsg,
		TS:           time.Now().UTC(),
		Text:         body.Text,
	})

	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream := s.client.Stream(ctx, model, system, hist)

	var assistantBuf string
	writeSSE := func(payload any) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	streamErr := false
	for chunk := range stream {
		if chunk.Err != nil {
			writeSSE(map[string]string{"type": "error", "msg": chunk.Err.Error()})
			streamErr = true
			break
		}
		if chunk.Text != "" {
			assistantBuf += chunk.Text
			writeSSE(map[string]string{"type": "text", "text": chunk.Text})
		}
		if chunk.Done {
			writeSSE(map[string]string{"type": "done"})
			break
		}
	}

	if !streamErr && assistantBuf != "" {
		s.mu.Lock()
		s.history = append(s.history, Message{Role: "assistant", Content: assistantBuf})
		s.turns++
		s.mu.Unlock()
		_ = s.log.Append(Event{
			EventID:      NewID(),
			DiscussionID: s.id,
			Op:           OpAssistant,
			TS:           time.Now().UTC(),
			Text:         assistantBuf,
		})
	}
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	go func() {
		// Give the response a beat to flush before tearing down.
		time.Sleep(150 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.Shutdown(ctx)
		cancel()
	}()
	w.WriteHeader(http.StatusNoContent)
}

// buildSeedTurn renders the snapshotted notes into the first user
// message the model sees. Labeled blocks so it can refer back to
// "note 2" the way the user does.
func buildSeedTurn(seeds []Seed) string {
	var sb stringBuilder
	for i, s := range seeds {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "## Note %d", i+1)
		if !s.CreatedAt.IsZero() {
			fmt.Fprintf(&sb, " (created %s)", s.CreatedAt.Format("2006-01-02"))
		}
		if s.NoteID != "" {
			fmt.Fprintf(&sb, " [id=%s]", s.NoteID)
		}
		sb.WriteString("\n")
		sb.WriteString(s.Text)
	}
	return sb.String()
}

// stringBuilder is a thin alias so we can use Fprintf without importing
// strings directly here.
type stringBuilder struct {
	buf []byte
}

func (b *stringBuilder) Write(p []byte) (int, error)     { b.buf = append(b.buf, p...); return len(p), nil }
func (b *stringBuilder) WriteString(s string) (int, error) { b.buf = append(b.buf, s...); return len(s), nil }
func (b *stringBuilder) String() string                   { return string(b.buf) }

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end {
		c := s[start]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			start++
			continue
		}
		break
	}
	for end > start {
		c := s[end-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			end--
			continue
		}
		break
	}
	return s[start:end]
}
