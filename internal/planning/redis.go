package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Redis keys used by the cloud backend.
const (
	keyPlan        = "tack:plan"
	keyAnnotations = "tack:annotations"
	keyHibana      = "tack:hibana"
	keyUsage       = "tack:usage"
	keyRecapsIndex = "tack:recaps:index"

	keyPlanRev = "tack:plan:rev"
	keyAnnRev  = "tack:annotations:rev"
)

// recapKey returns the Redis key for a single recap by name (e.g. "2026-W16").
func recapKey(name string) string { return "tack:recap:" + name }

// redisBackend abstracts the small slice of Redis we need so the store can be
// tested with an in-memory fake.
type redisBackend interface {
	Enabled() bool
	Get(ctx context.Context, key string) (val string, ok bool, err error)
	Set(ctx context.Context, key, val string) error
	Del(ctx context.Context, key string) error
	RPush(ctx context.Context, key string, vals ...string) error
	LRange(ctx context.Context, key string, start, stop int) ([]string, error)
	Incr(ctx context.Context, key string) (int64, error)
	SAdd(ctx context.Context, key string, members ...string) error
	SMembers(ctx context.Context, key string) ([]string, error)
}

// nopBackend is used when no Redis credentials are configured. Gets always
// miss; writes are silently dropped. This keeps the store's call sites free of
// nil-checks.
type nopBackend struct{}

func (nopBackend) Enabled() bool                                                { return false }
func (nopBackend) Get(context.Context, string) (string, bool, error)            { return "", false, nil }
func (nopBackend) Set(context.Context, string, string) error                    { return nil }
func (nopBackend) Del(context.Context, string) error                            { return nil }
func (nopBackend) RPush(context.Context, string, ...string) error               { return nil }
func (nopBackend) LRange(context.Context, string, int, int) ([]string, error)   { return nil, nil }
func (nopBackend) Incr(context.Context, string) (int64, error)                  { return 0, nil }
func (nopBackend) SAdd(context.Context, string, ...string) error                { return nil }
func (nopBackend) SMembers(context.Context, string) ([]string, error)           { return nil, nil }

// restBackend talks to Upstash's HTTP REST endpoint.
type restBackend struct {
	url    string
	token  string
	client *http.Client
}

func newRESTBackend(url, token string) *restBackend {
	return &restBackend{
		url:    strings.TrimRight(url, "/"),
		token:  token,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (r *restBackend) Enabled() bool { return r != nil && r.url != "" && r.token != "" }

// do sends a command as a JSON array body (Upstash's canonical form) and
// returns the raw "result" field.
func (r *restBackend) do(ctx context.Context, args ...string) (json.RawMessage, error) {
	payload, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstash %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	return out.Result, nil
}

func (r *restBackend) Get(ctx context.Context, key string) (string, bool, error) {
	raw, err := r.do(ctx, "GET", key)
	if err != nil {
		return "", false, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, err
	}
	return s, true, nil
}

func (r *restBackend) Set(ctx context.Context, key, val string) error {
	_, err := r.do(ctx, "SET", key, val)
	return err
}

func (r *restBackend) Del(ctx context.Context, key string) error {
	_, err := r.do(ctx, "DEL", key)
	return err
}

func (r *restBackend) RPush(ctx context.Context, key string, vals ...string) error {
	if len(vals) == 0 {
		return nil
	}
	args := append([]string{"RPUSH", key}, vals...)
	_, err := r.do(ctx, args...)
	return err
}

func (r *restBackend) Incr(ctx context.Context, key string) (int64, error) {
	raw, err := r.do(ctx, "INCR", key)
	if err != nil {
		return 0, err
	}
	// Upstash returns numbers as JSON numbers for INCR.
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	// Some proxies stringify the result; fall back to parsing.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

func (r *restBackend) SAdd(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	args := append([]string{"SADD", key}, members...)
	_, err := r.do(ctx, args...)
	return err
}

func (r *restBackend) SMembers(ctx context.Context, key string) ([]string, error) {
	raw, err := r.do(ctx, "SMEMBERS", key)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *restBackend) LRange(ctx context.Context, key string, start, stop int) ([]string, error) {
	raw, err := r.do(ctx, "LRANGE", key, strconv.Itoa(start), strconv.Itoa(stop))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
