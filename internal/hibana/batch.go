package hibana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const batchManifestVersion = 1

var ErrNoBatchSession = errors.New("hibana: no active batch session")

type BatchChangeKind string

const (
	BatchAdd    BatchChangeKind = "add"
	BatchEdit   BatchChangeKind = "edit"
	BatchDelete BatchChangeKind = "delete"
)

type BatchDocument struct {
	Path string
	ID   ID
	Text string
}

type BatchChange struct {
	Kind     BatchChangeKind
	ID       ID
	Text     string
	BaseText string
	Path     string
}

type BatchConflict struct {
	ID          ID
	Path        string
	BaseText    string
	LocalText   string
	CurrentText string
	Reason      string
}

type BatchPlan struct {
	Changes   []BatchChange
	Conflicts []BatchConflict
}

type BatchApplyReport struct {
	Added      int
	Edited     int
	Deleted    int
	AddedNotes []Note
}

type BatchSession struct {
	Workspace string
	Manifest  string
}

// BatchDisplayNote identifies one note's visible TUI number and position.
// The manager uses this only when creating a new workspace; reopening keeps
// the existing filenames unchanged.
type BatchDisplayNote struct {
	ID     ID
	Number int
}

type BatchCommitReport struct {
	Added          int
	Edited         int
	Deleted        int
	Conflicts      []BatchConflict
	LocalCommitted bool
	Pending        int
	PendingErr     error
	SyncErr        error
	RemoteEnabled  bool
}

type batchManifest struct {
	Version   int           `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	Workspace string        `json:"workspace"`
	Base      []Note        `json:"base"`
	Files     map[ID]string `json:"files,omitempty"`
}

type WorkspaceOpener func(path string) error

type BatchManager struct {
	store           *Store
	opener          WorkspaceOpener
	removeSessionFn func(batchManifest) error
	pendingPushFn   func() (int, error)
	beforeApply     func()
}

func NewBatchManager(store *Store, opener WorkspaceOpener) *BatchManager {
	if opener == nil {
		opener = OpenBatchWorkspaceInCursor
	}
	m := &BatchManager{store: store, opener: opener}
	m.removeSessionFn = m.removeSession
	m.pendingPushFn = store.PendingPush
	return m
}

func OpenBatchWorkspaceInCursor(path string) error {
	return exec.Command("open", "-a", "Cursor", path).Start()
}

func (m *BatchManager) manifestPath() string {
	return filepath.Join(m.store.Dir(), ".hibana-batch", "session.json")
}

func (m *BatchManager) workspacePath() string {
	return filepath.Join(m.store.Dir(), "hibana-batch-workspace")
}

func (m *BatchManager) Start() (BatchSession, bool, error) {
	return m.start(nil)
}

// StartWithDisplayOrder creates a new workspace in the same order and with
// the same visible numbers as the Hibana TUI. An existing session is simply
// reopened, so in-progress filenames never change.
func (m *BatchManager) StartWithDisplayOrder(order []BatchDisplayNote) (BatchSession, bool, error) {
	return m.start(order)
}

func (m *BatchManager) start(order []BatchDisplayNote) (BatchSession, bool, error) {
	manifest, err := m.loadManifestMetadata()
	if err == nil {
		info, statErr := os.Lstat(manifest.Workspace)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Remove(m.manifestPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
				return BatchSession{}, false, err
			}
			err = ErrNoBatchSession
		} else if statErr != nil {
			return BatchSession{}, false, statErr
		} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return BatchSession{}, false, fmt.Errorf("hibana: unsafe batch workspace entry")
		} else {
			session := m.session(manifest)
			if err := m.openWorkspace(session.Workspace); err != nil {
				return session, true, err
			}
			return session, true, nil
		}
	}
	if !errors.Is(err, ErrNoBatchSession) {
		return BatchSession{}, false, err
	}

	base, err := m.store.List()
	if err != nil {
		return BatchSession{}, false, err
	}
	base, displayNumbers, err := orderBatchNotes(base, order)
	if err != nil {
		return BatchSession{}, false, err
	}
	workspace := m.workspacePath()
	if _, err := os.Lstat(workspace); err == nil {
		if err := os.RemoveAll(workspace); err != nil {
			return BatchSession{}, false, fmt.Errorf("hibana: remove interrupted batch workspace: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return BatchSession{}, false, err
	}
	files, err := m.exportWorkspaceWithDisplayNumbers(workspace, base, displayNumbers)
	if err != nil {
		return BatchSession{}, false, err
	}
	manifest = batchManifest{
		Version:   batchManifestVersion,
		CreatedAt: time.Now().UTC(),
		Workspace: workspace,
		Base:      base,
		Files:     files,
	}
	if err := m.writeManifest(manifest); err != nil {
		_ = os.RemoveAll(workspace)
		return BatchSession{}, false, err
	}
	session := m.session(manifest)
	if err := m.openWorkspace(workspace); err != nil {
		return session, false, err
	}
	return session, false, nil
}

func (m *BatchManager) Reopen() (BatchSession, error) {
	_, err := m.loadManifestMetadata()
	if errors.Is(err, ErrNoBatchSession) {
		if _, statErr := os.Lstat(m.workspacePath()); errors.Is(statErr, os.ErrNotExist) {
			return BatchSession{}, ErrNoBatchSession
		} else if statErr != nil {
			return BatchSession{}, statErr
		}
	} else if err != nil {
		return BatchSession{}, err
	}
	session, _, err := m.Start()
	return session, err
}

func (m *BatchManager) Diff() (BatchPlan, error) {
	manifest, err := m.loadManifest()
	if err != nil {
		return BatchPlan{}, err
	}
	docs, err := readBatchDocuments(manifest.Workspace, manifest.Base)
	if err != nil {
		return BatchPlan{}, err
	}
	current, err := m.store.List()
	if err != nil {
		return BatchPlan{}, err
	}
	plan, err := PlanBatch(manifest.Base, docs, current)
	if err != nil {
		return BatchPlan{}, err
	}
	applyManifestPaths(&plan, manifest.Files)
	return plan, nil
}

func (m *BatchManager) Commit(ctx context.Context) (BatchCommitReport, error) {
	manifest, err := m.loadManifest()
	if err != nil {
		return BatchCommitReport{}, err
	}
	docs, err := readBatchDocuments(manifest.Workspace, manifest.Base)
	if err != nil {
		return BatchCommitReport{}, err
	}

	pullReport := m.store.Pull(ctx)
	current, err := m.store.List()
	if err != nil {
		return BatchCommitReport{}, err
	}
	plan, err := PlanBatch(manifest.Base, docs, current)
	if err != nil {
		return BatchCommitReport{}, err
	}
	applyManifestPaths(&plan, manifest.Files)
	report := BatchCommitReport{Conflicts: plan.Conflicts, RemoteEnabled: m.store.backend.Enabled()}
	if len(plan.Conflicts) > 0 {
		return report, fmt.Errorf("hibana: batch has %d conflict(s)", len(plan.Conflicts))
	}
	if m.beforeApply != nil {
		m.beforeApply()
	}
	local, err := m.store.ApplyBatchIfCurrent(current, plan.Changes)
	if err != nil {
		var changed *BatchStateChangedError
		if errors.As(err, &changed) {
			report.Conflicts = changed.Conflicts
		}
		return report, err
	}
	report.Added = local.Added
	report.Edited = local.Edited
	report.Deleted = local.Deleted
	report.LocalCommitted = true

	syncReport := m.store.Sync(ctx)
	if syncReport.Err != nil {
		report.SyncErr = syncReport.Err
	} else if pullReport.Err != nil {
		report.SyncErr = pullReport.Err
	}
	if report.RemoteEnabled {
		report.Pending, report.PendingErr = m.pendingPushFn()
	}
	if err := m.removeSessionFn(manifest); err != nil {
		return report, fmt.Errorf("hibana: local batch committed but session cleanup failed: %w", err)
	}
	return report, nil
}

func (m *BatchManager) Abort() error {
	manifest, err := m.loadManifestMetadata()
	if errors.Is(err, ErrNoBatchSession) {
		if _, statErr := os.Lstat(m.workspacePath()); statErr == nil {
			return os.RemoveAll(m.workspacePath())
		} else if errors.Is(statErr, os.ErrNotExist) {
			return ErrNoBatchSession
		} else {
			return statErr
		}
	}
	if err != nil {
		return err
	}
	if _, statErr := os.Lstat(manifest.Workspace); errors.Is(statErr, os.ErrNotExist) {
		return os.Remove(m.manifestPath())
	} else if statErr != nil {
		return statErr
	}
	return m.removeSessionFn(manifest)
}

func (m *BatchManager) openWorkspace(path string) error {
	if m.opener == nil {
		return fmt.Errorf("hibana: workspace opener is unavailable")
	}
	return m.opener(path)
}

func (m *BatchManager) session(manifest batchManifest) BatchSession {
	return BatchSession{Workspace: manifest.Workspace, Manifest: m.manifestPath()}
}

func (m *BatchManager) exportWorkspace(workspace string, notes []Note) error {
	displayNumbers := make(map[ID]int, len(notes))
	for i, note := range notes {
		displayNumbers[note.ID] = i + 1
	}
	_, err := m.exportWorkspaceWithDisplayNumbers(workspace, notes, displayNumbers)
	return err
}

func (m *BatchManager) exportWorkspaceWithDisplayNumbers(workspace string, notes []Note, displayNumbers map[ID]int) (map[ID]string, error) {
	tmp := workspace + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(tmp, "notes"), 0o755); err != nil {
		return nil, err
	}
	cleanup := func(err error) error {
		_ = os.RemoveAll(tmp)
		return err
	}
	readme := "# Hibana batch workspace\n\nEdit Markdown files in `notes/`, then return to Tack and run `:diff` followed by `:commit`. Keep the stable ID in existing filenames when renaming. New Markdown files do not need an ID. Deleting a file deletes that note. `:abort` discards this workspace. Closing Cursor does not commit.\n\nBefore editing notes through a shared remote, every device must run a Tack version that supports stable IDs. An offline commit remains durable locally and reports its remote error or pending state for retry. Foreign files inside `notes/` are rejected. If an existing file loses its ID and its content is fully rewritten, Tack treats that as a delete plus add because the original identity cannot be inferred.\n"
	agents := "# Batch editing guidance\n\n- One Markdown file is one Hibana note.\n- Preserve the 26-character stable note ID in filenames for existing notes.\n- New notes must be non-empty Markdown files without an existing note ID.\n- Do not create links, nested folders, foreign files, or non-Markdown files in `notes/`.\n- All devices sharing the remote must support stable IDs before edits are made.\n"
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte(readme), 0o644); err != nil {
		return nil, cleanup(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(agents), 0o644); err != nil {
		return nil, cleanup(err)
	}
	files := make(map[ID]string, len(notes))
	for i, note := range notes {
		displayNumber := displayNumbers[note.ID]
		if displayNumber <= 0 {
			displayNumber = i + 1
		}
		name := batchWorkspaceFilename(i+1, displayNumber, note)
		if err := os.WriteFile(filepath.Join(tmp, "notes", name), []byte(note.Text), 0o644); err != nil {
			return nil, cleanup(err)
		}
		files[note.ID] = name
	}
	if err := os.Rename(tmp, workspace); err != nil {
		return nil, cleanup(err)
	}
	return files, nil
}

func orderBatchNotes(notes []Note, order []BatchDisplayNote) ([]Note, map[ID]int, error) {
	byID := make(map[ID]Note, len(notes))
	originalNumber := make(map[ID]int, len(notes))
	for i, note := range notes {
		byID[note.ID] = note
		originalNumber[note.ID] = i + 1
	}
	if len(order) == 0 {
		return append([]Note(nil), notes...), originalNumber, nil
	}

	ordered := make([]Note, 0, len(notes))
	displayNumbers := make(map[ID]int, len(notes))
	seenIDs := make(map[ID]struct{}, len(order))
	seenNumbers := make(map[int]struct{}, len(order))
	for _, visible := range order {
		note, exists := byID[visible.ID]
		if !exists {
			return nil, nil, fmt.Errorf("hibana: display order contains unknown note ID %s", visible.ID)
		}
		if visible.Number <= 0 {
			return nil, nil, fmt.Errorf("hibana: invalid display number %d for %s", visible.Number, visible.ID)
		}
		if _, duplicate := seenIDs[visible.ID]; duplicate {
			return nil, nil, fmt.Errorf("hibana: duplicate note ID %s in display order", visible.ID)
		}
		if _, duplicate := seenNumbers[visible.Number]; duplicate {
			return nil, nil, fmt.Errorf("hibana: duplicate display number %d", visible.Number)
		}
		seenIDs[visible.ID] = struct{}{}
		seenNumbers[visible.Number] = struct{}{}
		displayNumbers[visible.ID] = visible.Number
		ordered = append(ordered, note)
	}
	for _, note := range notes {
		if _, exists := seenIDs[note.ID]; exists {
			continue
		}
		number := originalNumber[note.ID]
		for {
			if _, used := seenNumbers[number]; !used {
				break
			}
			number++
		}
		seenNumbers[number] = struct{}{}
		displayNumbers[note.ID] = number
		ordered = append(ordered, note)
	}
	return ordered, displayNumbers, nil
}

func batchWorkspaceFilename(position, displayNumber int, note Note) string {
	return fmt.Sprintf("%03d - [%d] %s -- %s.md", position, displayNumber, batchFilenameTitle(note.Text), note.ID)
}

func batchFilenameTitle(text string) string {
	title := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	var cleaned strings.Builder
	for _, r := range title {
		if unicode.IsControl(r) || strings.ContainsRune("/\\:", r) {
			r = '-'
		}
		if cleaned.Len()+utf8.RuneLen(r) > 160 {
			break
		}
		cleaned.WriteRune(r)
	}
	title = strings.Join(strings.Fields(cleaned.String()), " ")
	title = strings.Trim(title, " .")
	if title == "" {
		return "Untitled"
	}
	return title
}

func applyManifestPaths(plan *BatchPlan, files map[ID]string) {
	if len(files) == 0 {
		return
	}
	for i := range plan.Changes {
		if path := files[plan.Changes[i].ID]; path != "" && plan.Changes[i].Kind == BatchDelete {
			plan.Changes[i].Path = path
		}
	}
	for i := range plan.Conflicts {
		if path := files[plan.Conflicts[i].ID]; path != "" {
			plan.Conflicts[i].Path = path
		}
	}
}

func (m *BatchManager) writeManifest(manifest batchManifest) error {
	path := m.manifestPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (m *BatchManager) loadManifest() (batchManifest, error) {
	manifest, err := m.loadManifestMetadata()
	if err != nil {
		return batchManifest{}, err
	}
	info, err := os.Lstat(manifest.Workspace)
	if err != nil {
		return batchManifest{}, fmt.Errorf("hibana: batch workspace unavailable: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return batchManifest{}, fmt.Errorf("hibana: unsafe batch workspace entry")
	}
	return manifest, nil
}

func (m *BatchManager) loadManifestMetadata() (batchManifest, error) {
	path := m.manifestPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return batchManifest{}, ErrNoBatchSession
	}
	if err != nil {
		return batchManifest{}, err
	}
	var manifest batchManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return batchManifest{}, fmt.Errorf("hibana: invalid batch manifest: %w", err)
	}
	if manifest.Version != batchManifestVersion {
		return batchManifest{}, fmt.Errorf("hibana: unsupported batch manifest version %d", manifest.Version)
	}
	if filepath.Clean(manifest.Workspace) != filepath.Clean(m.workspacePath()) {
		return batchManifest{}, fmt.Errorf("hibana: unsafe workspace path in batch manifest")
	}
	seen := map[ID]struct{}{}
	for _, note := range manifest.Base {
		if !isBatchIDText(string(note.ID)) {
			return batchManifest{}, fmt.Errorf("hibana: invalid note ID in batch manifest")
		}
		if _, exists := seen[note.ID]; exists {
			return batchManifest{}, fmt.Errorf("hibana: duplicate note ID in batch manifest")
		}
		seen[note.ID] = struct{}{}
	}
	for id, name := range manifest.Files {
		if _, exists := seen[id]; !exists {
			return batchManifest{}, fmt.Errorf("hibana: file mapping references unknown note ID %s", id)
		}
		if filepath.Base(name) != name || filepath.Ext(name) != ".md" {
			return batchManifest{}, fmt.Errorf("hibana: unsafe batch filename %q", name)
		}
		matched, err := batchIDFromFilename(name, []ID{id})
		if err != nil || matched != id {
			return batchManifest{}, fmt.Errorf("hibana: batch filename %q does not retain note ID %s", name, id)
		}
	}
	return manifest, nil
}

func (m *BatchManager) removeSession(manifest batchManifest) error {
	if filepath.Clean(manifest.Workspace) != filepath.Clean(m.workspacePath()) {
		return fmt.Errorf("hibana: refusing to remove unexpected workspace path")
	}
	if err := os.Remove(m.manifestPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(manifest.Workspace)
}

func readBatchDocuments(workspace string, base []Note) ([]BatchDocument, error) {
	notesDir := filepath.Join(workspace, "notes")
	notesInfo, err := os.Lstat(notesDir)
	if err != nil {
		return nil, err
	}
	if !notesInfo.IsDir() || notesInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("hibana: unsafe notes directory")
	}
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		return nil, err
	}
	knownIDs := make([]ID, 0, len(base))
	for _, note := range base {
		knownIDs = append(knownIDs, note.ID)
	}
	docs := make([]BatchDocument, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			return nil, fmt.Errorf("hibana: unsafe notes entry %q: only Markdown files are allowed", name)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("hibana: unsafe notes entry %q", name)
		}
		path := filepath.Join(notesDir, name)
		pathInfo, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("hibana: unsafe notes entry %q", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("hibana: %s is not valid UTF-8", name)
		}
		id, err := batchIDFromFilename(name, knownIDs)
		if err != nil {
			return nil, err
		}
		docs = append(docs, BatchDocument{Path: name, ID: id, Text: string(data)})
	}
	return docs, nil
}

func batchIDFromFilename(name string, known []ID) (ID, error) {
	upper := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	var found ID
	for _, id := range known {
		candidate := ID(strings.ToUpper(string(id)))
		if !strings.Contains(upper, string(candidate)) {
			continue
		}
		if found != "" && found != candidate {
			return "", fmt.Errorf("hibana: filename %q contains multiple note IDs", name)
		}
		found = candidate
	}
	return found, nil
}

func isBatchIDText(value string) bool {
	if len(value) != 26 {
		return false
	}
	for i := range value {
		if !isBatchIDChar(value[i]) {
			return false
		}
	}
	return true
}

func isBatchIDChar(ch byte) bool {
	return strings.ContainsRune(idAlphabet, rune(ch))
}

func PlanBatch(base []Note, docs []BatchDocument, current []Note) (BatchPlan, error) {
	baseByID := make(map[ID]Note, len(base))
	for _, note := range base {
		if _, exists := baseByID[note.ID]; exists {
			return BatchPlan{}, fmt.Errorf("hibana: duplicate base note ID %s", note.ID)
		}
		baseByID[note.ID] = note
	}
	currentByID := make(map[ID]Note, len(current))
	for _, note := range current {
		currentByID[note.ID] = note
	}
	docByID := map[ID]BatchDocument{}
	var additions []BatchDocument
	for _, doc := range docs {
		if filepath.Base(doc.Path) != doc.Path || doc.Path == "." || doc.Path == "" {
			return BatchPlan{}, fmt.Errorf("hibana: unsafe document path %q", doc.Path)
		}
		if !utf8.ValidString(doc.Text) {
			return BatchPlan{}, fmt.Errorf("hibana: %s is not valid UTF-8", doc.Path)
		}
		if strings.TrimSpace(doc.Text) == "" {
			return BatchPlan{}, fmt.Errorf("hibana: empty note file %q", doc.Path)
		}
		if doc.ID == "" {
			additions = append(additions, doc)
			continue
		}
		if _, exists := baseByID[doc.ID]; !exists {
			return BatchPlan{}, fmt.Errorf("hibana: unknown note ID %s in %q", doc.ID, doc.Path)
		}
		if prior, exists := docByID[doc.ID]; exists {
			return BatchPlan{}, fmt.Errorf("hibana: duplicate note ID %s in %q and %q", doc.ID, prior.Path, doc.Path)
		}
		docByID[doc.ID] = doc
	}
	for _, doc := range additions {
		for _, note := range base {
			if _, kept := docByID[note.ID]; !kept && doc.Text == note.Text {
				return BatchPlan{}, fmt.Errorf("hibana: lost note ID %s in renamed file %q", note.ID, doc.Path)
			}
		}
	}

	plan := BatchPlan{}
	for baseIndex, original := range base {
		doc, localPresent := docByID[original.ID]
		remote, remotePresent := currentByID[original.ID]
		path := fmt.Sprintf("%03d-%s.md", baseIndex+1, original.ID)
		if localPresent {
			path = doc.Path
		}
		localChanged := !localPresent || doc.Text != original.Text
		if !localChanged {
			continue
		}
		if !localPresent {
			switch {
			case !remotePresent:
				continue
			case remote.Text != original.Text:
				plan.Conflicts = append(plan.Conflicts, BatchConflict{ID: original.ID, Path: path, BaseText: original.Text, CurrentText: remote.Text, Reason: "local delete conflicts with current edit"})
			default:
				plan.Changes = append(plan.Changes, BatchChange{Kind: BatchDelete, ID: original.ID, Text: original.Text, BaseText: original.Text, Path: path})
			}
			continue
		}
		switch {
		case !remotePresent:
			plan.Conflicts = append(plan.Conflicts, BatchConflict{ID: original.ID, Path: path, BaseText: original.Text, LocalText: doc.Text, Reason: "local edit conflicts with current delete"})
		case remote.Text == original.Text:
			plan.Changes = append(plan.Changes, BatchChange{Kind: BatchEdit, ID: original.ID, Text: doc.Text, BaseText: original.Text, Path: doc.Path})
		case remote.Text == doc.Text:
			continue
		default:
			plan.Conflicts = append(plan.Conflicts, BatchConflict{ID: original.ID, Path: path, BaseText: original.Text, LocalText: doc.Text, CurrentText: remote.Text, Reason: "local and current text differ"})
		}
	}
	for _, doc := range additions {
		plan.Changes = append(plan.Changes, BatchChange{Kind: BatchAdd, Text: doc.Text, Path: doc.Path})
	}
	return plan, nil
}

type BatchStateChangedError struct {
	Conflicts []BatchConflict
}

func (e *BatchStateChangedError) Error() string {
	return fmt.Sprintf("hibana: local note state changed after planning (%d conflict(s))", len(e.Conflicts))
}

func (s *Store) ApplyBatch(changes []BatchChange) (BatchApplyReport, error) {
	expected, err := s.List()
	if err != nil {
		return BatchApplyReport{}, err
	}
	return s.ApplyBatchIfCurrent(expected, changes)
}

// ApplyBatchIfCurrent compares the exact folded note state and appends the
// accepted operations while the log's stable cross-process lock remains held.
func (s *Store) ApplyBatchIfCurrent(expected []Note, changes []BatchChange) (BatchApplyReport, error) {
	var report BatchApplyReport
	err := s.log.AppendBatchChecked(func(events []Event) ([]Event, error) {
		current := Fold(events)
		if !sameBatchNotes(expected, current) {
			return nil, &BatchStateChangedError{Conflicts: changedBatchNotes(expected, current)}
		}
		var err error
		report, events, err = buildBatchEvents(current, changes, nowFunc())
		return events, err
	})
	if err != nil {
		return BatchApplyReport{}, err
	}
	return report, nil
}

func buildBatchEvents(current []Note, changes []BatchChange, now time.Time) (BatchApplyReport, []Event, error) {
	byID := make(map[ID]Note, len(current))
	for _, note := range current {
		byID[note.ID] = note
	}
	seen := map[ID]struct{}{}
	events := make([]Event, 0, len(changes))
	report := BatchApplyReport{}
	for _, change := range changes {
		switch change.Kind {
		case BatchAdd:
			if change.ID != "" {
				return BatchApplyReport{}, nil, fmt.Errorf("hibana: batch add must not supply an ID")
			}
			if strings.TrimSpace(change.Text) == "" || !utf8.ValidString(change.Text) {
				return BatchApplyReport{}, nil, fmt.Errorf("hibana: invalid text for batch add")
			}
			id := NewID()
			events = append(events, Event{EventID: NewID(), NoteID: id, Op: OpAdd, TS: now, Text: change.Text, CreatedAt: now})
			report.Added++
			report.AddedNotes = append(report.AddedNotes, Note{ID: id, Text: change.Text, CreatedAt: now, UpdatedAt: now})
		case BatchEdit:
			note, exists := byID[change.ID]
			if !exists {
				return BatchApplyReport{}, nil, fmt.Errorf("hibana: batch edit id %s not found", change.ID)
			}
			if _, duplicate := seen[change.ID]; duplicate {
				return BatchApplyReport{}, nil, fmt.Errorf("hibana: duplicate batch operation for %s", change.ID)
			}
			if strings.TrimSpace(change.Text) == "" || !utf8.ValidString(change.Text) {
				return BatchApplyReport{}, nil, fmt.Errorf("hibana: invalid text for batch edit")
			}
			seen[change.ID] = struct{}{}
			events = append(events, Event{EventID: NewID(), NoteID: change.ID, Op: OpAdd, TS: now, Text: change.Text, CreatedAt: note.CreatedAt})
			report.Edited++
		case BatchDelete:
			if _, exists := byID[change.ID]; !exists {
				return BatchApplyReport{}, nil, fmt.Errorf("hibana: batch delete id %s not found", change.ID)
			}
			if _, duplicate := seen[change.ID]; duplicate {
				return BatchApplyReport{}, nil, fmt.Errorf("hibana: duplicate batch operation for %s", change.ID)
			}
			seen[change.ID] = struct{}{}
			events = append(events, Event{EventID: NewID(), NoteID: change.ID, Op: OpDelete, TS: now})
			report.Deleted++
		default:
			return BatchApplyReport{}, nil, fmt.Errorf("hibana: unknown batch operation %q", change.Kind)
		}
	}
	return report, events, nil
}

func sameBatchNotes(expected, current []Note) bool {
	if len(expected) != len(current) {
		return false
	}
	for i := range expected {
		if expected[i].ID != current[i].ID || expected[i].Text != current[i].Text ||
			!expected[i].CreatedAt.Equal(current[i].CreatedAt) || !expected[i].UpdatedAt.Equal(current[i].UpdatedAt) {
			return false
		}
	}
	return true
}

func changedBatchNotes(expected, current []Note) []BatchConflict {
	expectedByID := make(map[ID]Note, len(expected))
	currentByID := make(map[ID]Note, len(current))
	for _, note := range expected {
		expectedByID[note.ID] = note
	}
	for _, note := range current {
		currentByID[note.ID] = note
	}
	var conflicts []BatchConflict
	for _, note := range expected {
		actual, exists := currentByID[note.ID]
		if !exists || note.Text != actual.Text || !note.CreatedAt.Equal(actual.CreatedAt) || !note.UpdatedAt.Equal(actual.UpdatedAt) {
			conflict := BatchConflict{ID: note.ID, BaseText: note.Text, Reason: "local note changed after batch planning"}
			if exists {
				conflict.CurrentText = actual.Text
			}
			conflicts = append(conflicts, conflict)
		}
	}
	for _, note := range current {
		if _, exists := expectedByID[note.ID]; !exists {
			conflicts = append(conflicts, BatchConflict{ID: note.ID, CurrentText: note.Text, Reason: "local note was added after batch planning"})
		}
	}
	return conflicts
}

func (s *Store) Pull(ctx context.Context) SyncReport {
	report := SyncReport{}
	if !s.backend.Enabled() {
		return report
	}
	if err := pullOnce(ctx, s.log, s.pushed, s.backend, &report); err != nil {
		report.Err = err
	}
	return report
}
