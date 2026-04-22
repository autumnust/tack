package planning

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRESTBackend_Live hits the real Upstash REST endpoint. Skipped unless
// UPSTASH_REDIS_REST_URL and UPSTASH_REDIS_REST_TOKEN are set in the
// environment. Intended as a one-shot smoke check after credential changes.
func TestRESTBackend_Live(t *testing.T) {
	url := os.Getenv("UPSTASH_REDIS_REST_URL")
	token := os.Getenv("UPSTASH_REDIS_REST_TOKEN")
	if url == "" || token == "" {
		t.Skip("UPSTASH_REDIS_REST_URL / UPSTASH_REDIS_REST_TOKEN not set")
	}
	b := newRESTBackend(url, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const key = "tack:itest"
	defer func() { _ = b.Del(context.Background(), key) }()

	if err := b.Set(ctx, key, "hello"); err != nil {
		t.Fatalf("SET: %s", err)
	}
	v, ok, err := b.Get(ctx, key)
	if err != nil || !ok || v != "hello" {
		t.Fatalf("GET: v=%q ok=%v err=%v", v, ok, err)
	}

	const listKey = "tack:itest:list"
	defer func() { _ = b.Del(context.Background(), listKey) }()
	if err := b.RPush(ctx, listKey, "a", "b", "c"); err != nil {
		t.Fatalf("RPUSH: %s", err)
	}
	got, err := b.LRange(ctx, listKey, 0, -1)
	if err != nil {
		t.Fatalf("LRANGE: %s", err)
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("LRANGE shape wrong: %+v", got)
	}
}
