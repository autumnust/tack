package discuss

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicEndpoint is the Messages API URL. Overridable in tests.
var AnthropicEndpoint = "https://api.anthropic.com/v1/messages"

// AnthropicAPIVersion is sent as the anthropic-version header.
const AnthropicAPIVersion = "2023-06-01"

// MaxTokens caps a single completion. Generous default — most thinking
// partner replies fit well under this.
const MaxTokens = 4096

// Message is one user/assistant turn in the conversation history sent
// to the API. Content is plain text in v1 — no images or tool use.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// Chunk is one streamed delta from the model.
type Chunk struct {
	// Text is the latest delta. May be empty for non-text events.
	Text string
	// Done is true when the stream has finished cleanly.
	Done bool
	// Err is non-nil when the stream errored. The caller should stop.
	Err error
}

// AnthropicClient holds the api key + http client. Reuse across calls.
type AnthropicClient struct {
	APIKey     string
	HTTPClient *http.Client
	// Endpoint overrides the package-level AnthropicEndpoint when non-empty.
	// Used by tests to point the client at a stub server.
	Endpoint string
}

func NewAnthropicClient(apiKey string) *AnthropicClient {
	return &AnthropicClient{
		APIKey: apiKey,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (c *AnthropicClient) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return AnthropicEndpoint
}

// Stream calls the Messages API with stream=true and emits Chunks
// onto the returned channel. The channel is closed when the stream
// ends (success or error). Cancel via ctx.
func (c *AnthropicClient) Stream(ctx context.Context, model, system string, messages []Message) <-chan Chunk {
	out := make(chan Chunk, 16)

	go func() {
		defer close(out)

		body := map[string]any{
			"model":      model,
			"max_tokens": MaxTokens,
			"system":     system,
			"messages":   messages,
			"stream":     true,
		}
		buf, err := json.Marshal(body)
		if err != nil {
			out <- Chunk{Err: err}
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(buf))
		if err != nil {
			out <- Chunk{Err: err}
			return
		}
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", AnthropicAPIVersion)
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			out <- Chunk{Err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			out <- Chunk{Err: fmt.Errorf("anthropic: http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))}
			return
		}

		// Parse SSE: events are pairs of "event: <name>\n" + "data: <json>\n\n".
		// We only care about content_block_delta (text) and message_stop.
		r := bufio.NewReader(resp.Body)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					out <- Chunk{Done: true}
					return
				}
				out <- Chunk{Err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "" {
				continue
			}
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue // tolerate stray parse errors mid-stream
			}
			switch ev.Type {
			case "content_block_delta":
				if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
					out <- Chunk{Text: ev.Delta.Text}
				}
			case "message_stop":
				out <- Chunk{Done: true}
				return
			case "error":
				out <- Chunk{Err: fmt.Errorf("anthropic stream error: %s: %s", ev.Error.Type, ev.Error.Message)}
				return
			}
		}
	}()

	return out
}
