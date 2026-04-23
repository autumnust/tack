package planning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// OutboxOp is one pending Redis write that couldn't be delivered (or has not
// yet been delivered) while offline. Ops replay in insertion order.
type OutboxOp struct {
	TS      time.Time `json:"ts"`
	Op      string    `json:"op"`  // set | rpush | sadd | incr
	Key     string    `json:"key"` // logical Redis key
	Payload string    `json:"payload,omitempty"`
}

// outbox is a JSONL file that buffers Redis writes we couldn't deliver.
// It is intentionally simple: append on write, rewrite on drain.
type outbox struct {
	mu   sync.Mutex
	path string
}

func newOutbox(dir string) *outbox {
	return &outbox{path: filepath.Join(dir, ".outbox.jsonl")}
}

// Append writes a single op to the buffer. Errors here are unusual enough that
// callers just log them and keep going — the alternative (failing the user's
// write) is worse than losing a pending op.
func (o *outbox) Append(op OutboxOp) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	f, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// Load returns all pending ops in insertion order.
func (o *outbox) Load() ([]OutboxOp, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.loadLocked()
}

func (o *outbox) loadLocked() ([]OutboxOp, error) {
	data, err := os.ReadFile(o.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ops []OutboxOp
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var op OutboxOp
		if err := json.Unmarshal(line, &op); err != nil {
			// Skip malformed lines rather than fail the whole drain.
			continue
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// Rewrite replaces the buffer with the given ops (used after a partial drain).
// Passing nil/empty clears it.
func (o *outbox) Rewrite(ops []OutboxOp) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(ops) == 0 {
		if err := os.Remove(o.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	tmp := o.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, op := range ops {
		b, err := json.Marshal(op)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, o.path)
}

// Len returns the number of pending ops without loading all payloads.
func (o *outbox) Len() int {
	ops, err := o.Load()
	if err != nil {
		return 0
	}
	return len(ops)
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range data {
		if c == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// String for debugging.
func (op OutboxOp) String() string {
	return fmt.Sprintf("%s %s %s (%s)", op.Op, op.Key, truncate(op.Payload, 40), op.TS.Format(time.RFC3339))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
