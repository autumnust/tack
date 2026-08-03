package tui

import (
	"os"
	"os/exec"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIDECommandEditsSelectedHibanaNote(t *testing.T) {
	app, store, _ := newBatchTestApp(t)
	if _, err := store.Add("B selected by visible order"); err != nil {
		t.Fatal(err)
	}
	notes, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	app.plan.Scratch = notesToScratch(notes)
	app.planView.SetData(app.plan, app.project)
	selected := app.planView.currentFlat()
	if selected == nil {
		t.Fatal("Hibana has no selected note")
	}
	selectedIdx := selected.focusIdx
	wantInitial := app.plan.Scratch[selectedIdx].Text

	var openedPath string
	app.ideCommandBuilder = func(path string) (*exec.Cmd, error) {
		openedPath = path
		return exec.Command("true"), nil
	}

	modelAfterCommand, cmd := app.executeCommand(&CommandResult{Action: "ide"})
	app = modelAfterCommand.(AppModel)
	if cmd == nil {
		t.Fatal(":ide did not schedule an IDE process")
	}
	if openedPath == "" {
		t.Fatal(":ide did not prepare a note file")
	}
	data, err := os.ReadFile(openedPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != wantInitial {
		t.Fatalf("IDE file content = %q, want selected note %q", got, wantInitial)
	}

	if err := os.WriteFile(openedPath, []byte("A edited in Cursor"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelAfterIDE, _ := app.Update(editorFinishedMsg{
		tmpPath: openedPath,
		section: sectionHibana,
		idx:     selectedIdx,
		subIdx:  -1,
	})
	app = modelAfterIDE.(AppModel)
	if got, want := app.plan.Scratch[selectedIdx].Text, "A edited in Cursor"; got != want {
		t.Fatalf("note text = %q, want %q", got, want)
	}
	assertStatus(t, app, "Note updated")
}

func TestIDECommandOnlyAppliesToSelectedHibanaNote(t *testing.T) {
	app, _, _ := newBatchTestApp(t)
	opened := false
	app.ideCommandBuilder = func(path string) (*exec.Cmd, error) {
		opened = true
		return exec.Command("true"), nil
	}

	app.view = viewBoard
	app = sendCommand(t, app, "ide")
	assertStatus(t, app, "Hibana")
	if opened {
		t.Fatal(":ide opened outside planning mode")
	}

	app.view = viewPlan
	app.planView.SetSection(sectionToday)
	app = sendCommand(t, app, "ide")
	assertStatus(t, app, "Hibana")
	if opened {
		t.Fatal(":ide opened outside the Hibana section")
	}
}

func TestDefaultIDECommandUsesConfiguredIDEWithWait(t *testing.T) {
	t.Setenv("TACK_IDE", "test-ide")
	oldLookPath := ideLookPath
	ideLookPath = func(file string) (string, error) {
		if file != "test-ide" {
			t.Fatalf("LookPath called with %q", file)
		}
		return "/tmp/test-ide", nil
	}
	t.Cleanup(func() { ideLookPath = oldLookPath })

	cmd, err := defaultIDECommand("/tmp/note.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cmd.Args, []string{"/tmp/test-ide", "--wait", "/tmp/note.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("IDE args = %#v, want %#v", got, want)
	}
}

func TestVimEditDoesNotUseIDECommand(t *testing.T) {
	app, _, _ := newBatchTestApp(t)
	app.ideCommandBuilder = func(path string) (*exec.Cmd, error) {
		t.Fatal("the e key used the IDE command")
		return nil, nil
	}

	_, cmd := app.updatePlan(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if cmd == nil {
		t.Fatal("the existing e key did not schedule its editor")
	}
}
