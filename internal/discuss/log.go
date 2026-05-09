package discuss

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Log is one discussion's append-only event store. One file, one event
// per line. Each file is written by exactly one server goroutine at a
// time, so a process-local mutex is sufficient — no flock.
type Log struct {
	path string
	mu   sync.Mutex
}

// NewLog returns a Log rooted at path. The parent directory is created
// on demand. The file itself is created lazily on first append.
func NewLog(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Log{path: path}, nil
}

// Path returns the underlying log file path.
func (l *Log) Path() string { return l.path }

// Append writes one event to the end of the log, fsyncing on success.
func (l *Log) Append(e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	line, err := e.MarshalLine()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
	return f.Sync()
}

// Read returns all events in the log in file order. A truncated final
// line (crash mid-Append) is silently dropped — that write was never
// durable. Earlier corruption surfaces as an error.
func (l *Log) Read() ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return readEvents(l.path)
}

func readEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	r := bufio.NewReader(f)
	lineNum := 0
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			lineNum++
			ev, perr := ParseEvent(line)
			if perr != nil {
				if errors.Is(err, io.EOF) && line[len(line)-1] != '\n' {
					break
				}
				if errors.Is(perr, errEmptyLine) {
					if errors.Is(err, io.EOF) {
						break
					}
					continue
				}
				return nil, fmt.Errorf("discuss: log line %d: %w", lineNum, perr)
			}
			out = append(out, ev)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	return out, nil
}

// LogPath returns the canonical on-disk path for a discussion id under
// the given planning dir. Callers should call os.MkdirAll on the parent
// before opening (NewLog does this for them).
func LogPath(planDir string, id ID) string {
	return filepath.Join(planDir, "discussions", string(id)+".jsonl")
}

// ListDiscussions returns the ids of all discussions on disk, sorted
// ascending (which is also chronological for ULID-style ids).
func ListDiscussions(planDir string) ([]ID, error) {
	dir := filepath.Join(planDir, "discussions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []ID
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		const ext = ".jsonl"
		if !strings.HasSuffix(name, ext) {
			continue
		}
		stem := name[:len(name)-len(ext)]
		if ValidID(stem) {
			out = append(out, ID(stem))
		}
	}
	return out, nil
}
