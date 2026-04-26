package hibana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Redis keys for the hibana feature. The HASH holds live notes (id → JSON);
// the graves SET holds ids of deleted notes so cross-device deletions
// propagate. No event log is stored remotely — Redis only mirrors current
// state.
const (
	HashKey   = "tack:hibana:v2"
	GravesKey = "tack:hibana:v2:graves"
)

// Backend is the minimal Redis surface hibana needs. Tests pass a fake; the
// REST implementation talks to Upstash. Methods return errors verbatim from
// the transport so callers can decide whether to surface or swallow.
type Backend interface {
	Enabled() bool
	HSet(ctx context.Context, key, field, value string) error
	HDel(ctx context.Context, key, field string) error
	HKeys(ctx context.Context, key string) ([]string, error)
	HMGet(ctx context.Context, key string, fields ...string) (map[string]string, error)
	SAdd(ctx context.Context, key, member string) error
	SRem(ctx context.Context, key, member string) error
	SMembers(ctx context.Context, key string) ([]string, error)
}

// nopBackend is the no-Redis stand-in: writes succeed silently, reads return
// nothing. Callers that depend on Enabled() will see false and avoid the
// network entirely.
type nopBackend struct{}

func NopBackend() Backend { return nopBackend{} }

func (nopBackend) Enabled() bool                                              { return false }
func (nopBackend) HSet(context.Context, string, string, string) error         { return nil }
func (nopBackend) HDel(context.Context, string, string) error                 { return nil }
func (nopBackend) HKeys(context.Context, string) ([]string, error)            { return nil, nil }
func (nopBackend) HMGet(context.Context, string, ...string) (map[string]string, error) {
	return nil, nil
}
func (nopBackend) SAdd(context.Context, string, string) error      { return nil }
func (nopBackend) SRem(context.Context, string, string) error      { return nil }
func (nopBackend) SMembers(context.Context, string) ([]string, error) { return nil, nil }

// REST implementation against Upstash.

type restBackend struct {
	url    string
	token  string
	client *http.Client
}

// NewRESTBackend returns a backend that talks to Upstash via its HTTP REST
// gateway. Empty url/token disables it (Enabled() returns false).
func NewRESTBackend(url, token string) Backend {
	return &restBackend{
		url:    strings.TrimRight(url, "/"),
		token:  token,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (r *restBackend) Enabled() bool { return r != nil && r.url != "" && r.token != "" }

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

func (r *restBackend) HSet(ctx context.Context, key, field, value string) error {
	_, err := r.do(ctx, "HSET", key, field, value)
	return err
}

func (r *restBackend) HDel(ctx context.Context, key, field string) error {
	_, err := r.do(ctx, "HDEL", key, field)
	return err
}

func (r *restBackend) HKeys(ctx context.Context, key string) ([]string, error) {
	raw, err := r.do(ctx, "HKEYS", key)
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

func (r *restBackend) HMGet(ctx context.Context, key string, fields ...string) (map[string]string, error) {
	if len(fields) == 0 {
		return map[string]string{}, nil
	}
	args := append([]string{"HMGET", key}, fields...)
	raw, err := r.do(ctx, args...)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]string{}, nil
	}
	var arr []*string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(fields))
	for i, f := range fields {
		if i >= len(arr) || arr[i] == nil {
			continue
		}
		out[f] = *arr[i]
	}
	return out, nil
}

func (r *restBackend) SAdd(ctx context.Context, key, member string) error {
	_, err := r.do(ctx, "SADD", key, member)
	return err
}

func (r *restBackend) SRem(ctx context.Context, key, member string) error {
	_, err := r.do(ctx, "SREM", key, member)
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
