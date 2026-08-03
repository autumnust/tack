package hibana

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Log is the on-disk append-only event store. One file, one event per line.
//
// Concurrency: all public methods acquire an in-process mutex, and the file
// is also held under flock(2) for the duration of any modifying call so a
// second tack process can't race us on append or compact.
type Log struct {
	path      string
	lockPath  string
	mu        sync.Mutex
	afterLock func() // test coordination; nil in production
}

// NewLog returns a Log rooted at path. The parent directory is created on
// demand. The file itself is created lazily on first append.
func NewLog(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Log{path: path, lockPath: path + ".lock"}, nil
}

// Path returns the underlying log file path.
func (l *Log) Path() string { return l.path }

// Append writes one event to the end of the log. fsync's the file on success
// so a power-loss reader sees either nothing or the complete line.
func (l *Log) Append(e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	line, err := e.MarshalLine()
	if err != nil {
		return err
	}
	return l.withFileLock(true, func() error {
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(line); err != nil {
			return err
		}
		return f.Sync()
	})
}

// AppendBatch writes several events through a sibling file and one rename so
// a reader sees either the old log or the old log plus the complete batch.
func (l *Log) AppendBatch(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.withFileLock(true, func() error {
		return l.appendBatchLocked(events)
	})
}

// AppendBatchChecked holds the stable file lock while the caller examines the
// current event stream, decides which events are valid, and appends them.
func (l *Log) AppendBatchChecked(build func(current []Event) ([]Event, error)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.withFileLock(true, func() error {
		current, err := readEventsUnlocked(l.path)
		if err != nil {
			return err
		}
		events, err := build(current)
		if err != nil {
			return err
		}
		return l.appendBatchLocked(events)
	})
}

func (l *Log) appendBatchLocked(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, e := range events {
		line, err := e.MarshalLine()
		if err != nil {
			return err
		}
		buf.Write(line)
	}
	existing, err := os.ReadFile(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := l.path + ".batch.tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	removeTmp := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	if _, err := f.Write(existing); err != nil {
		removeTmp()
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		removeTmp()
		return err
	}
	if err := f.Sync(); err != nil {
		removeTmp()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncParentDirectory(l.path)
}

// Read returns all events currently in the log, in file order. A truncated
// or otherwise corrupt final line is silently dropped — that's the
// signature of a crash mid-Append, and the lost write was never durable
// anyway. Earlier corruption returns an error so it doesn't go unnoticed.
func (l *Log) Read() ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var events []Event
	err := l.withFileLock(false, func() error {
		var err error
		events, err = readEventsUnlocked(l.path)
		return err
	})
	return events, err
}

func readEventsUnlocked(path string) ([]Event, error) {
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
				// A partial last line (no trailing newline) is the crash
				// signature — silently drop it.
				if errors.Is(err, io.EOF) && line[len(line)-1] != '\n' {
					break
				}
				// Blank lines are tolerated anywhere (they can creep in
				// when files are edited externally).
				if errors.Is(perr, errEmptyLine) {
					if errors.Is(err, io.EOF) {
						break
					}
					continue
				}
				return nil, fmt.Errorf("hibana: log line %d: %w", lineNum, perr)
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

// Compact rewrites the log to contain only the latest `add` event per
// surviving NoteID, in original add order. Tombstoned events and any
// previous adds that they superseded are dropped.
//
// EventIDs of surviving events are preserved so the sync layer's
// pushed-set tracking remains valid across compactions.
//
// Atomicity: writes a sibling tmp file, fsyncs, renames. A crash before
// rename leaves the original; a crash after sees the new file.
func (l *Log) Compact() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.withFileLock(true, l.compactLocked)
}

func (l *Log) compactLocked() error {
	events, err := readEventsUnlocked(l.path)
	if err != nil {
		return err
	}

	// Walk events and remember the last surviving add event for each
	// not-yet-deleted NoteID. We keep the *latest* add per NoteID to
	// honor any future intra-id update semantics.
	type entry struct {
		ev    Event
		order int
	}
	live := map[ID]*entry{}
	dead := map[ID]struct{}{}
	order := 0
	for _, ev := range events {
		switch ev.Op {
		case OpAdd:
			if _, gone := dead[ev.NoteID]; gone {
				continue
			}
			e, ok := live[ev.NoteID]
			if !ok {
				e = &entry{order: order}
				order++
				live[ev.NoteID] = e
			}
			e.ev = ev
		case OpDelete:
			dead[ev.NoteID] = struct{}{}
			delete(live, ev.NoteID)
		}
	}
	surviving := make([]*entry, 0, len(live))
	for _, e := range live {
		surviving = append(surviving, e)
	}
	// Sort by first-add order to keep the file deterministic.
	for i := 1; i < len(surviving); i++ {
		for j := i; j > 0 && surviving[j-1].order > surviving[j].order; j-- {
			surviving[j-1], surviving[j] = surviving[j], surviving[j-1]
		}
	}

	tmp := l.path + ".compact.tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	for _, e := range surviving {
		line, err := e.ev.MarshalLine()
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		if _, err := f.Write(line); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, l.path); err != nil {
		return err
	}
	return syncParentDirectory(l.path)
}

// ShouldCompact returns true if the log has accumulated enough tombstones
// (or the matching add events) to justify rewriting. Used by callers that
// want a cheap "compact when worthwhile" trigger.
func (l *Log) ShouldCompact() (bool, error) {
	events, err := l.Read()
	if err != nil {
		return false, err
	}
	if len(events) < 200 {
		return false, nil
	}
	live := Fold(events)
	return len(events) > 2*len(live), nil
}

func (l *Log) withFileLock(exclusive bool, fn func() error) error {
	lock, err := os.OpenFile(l.lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if exclusive {
		err = flockExclusive(lock)
	} else {
		err = flockShared(lock)
	}
	if err != nil {
		return err
	}
	defer flockUnlock(lock)
	if l.afterLock != nil {
		hook := l.afterLock
		l.afterLock = nil
		hook()
	}
	return fn()
}

func syncParentDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// flockExclusive / flockShared / flockUnlock wrap syscall.Flock so the
// rest of the package doesn't need to know about it.

func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}
func flockShared(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_SH)
}
func flockUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
