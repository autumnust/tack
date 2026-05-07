package tui

import (
	"strings"
	"testing"

	"github.com/autumnust/tack/internal/model"
	"github.com/autumnust/tack/internal/planning"
)

// TestBoardNotesIcon_VisibleOnlyForUnresolved exercises the full
// 1v1-notes resolution loop end-to-end: write a note via the store,
// confirm the board's per-person ✎ icon appears, mark every section
// resolved, confirm the icon disappears, then partially reopen and
// confirm it comes back. The test drives the same primitives the TUI
// uses (planning.Store + BoardModel.SetPersonNotes), so a regression
// anywhere along that chain — storage marker format, the heading
// scan, or the renderer — fails the test.
func TestBoardNotesIcon_VisibleOnlyForUnresolved(t *testing.T) {
	store, err := planning.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Two people: alice has a 1v1 note, bob has nothing. A third
	// person (carol) has a note where every section is already
	// resolved — her icon should never appear.
	persons := []model.PersonGroup{
		{Login: "alice", DisplayName: "Alice"},
		{Login: "bob", DisplayName: "Bob"},
		{Login: "carol", DisplayName: "Carol"},
	}

	if err := store.SavePersonNote("alice", "# Alice\n\n## 2026-05-01\n- chat\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePersonNote("carol", "# Carol\n\n## 2026-05-01 ✓\n- closed loop\n"); err != nil {
		t.Fatal(err)
	}

	board := NewBoardModel(persons)
	refresh := func() {
		presence := map[string]bool{}
		for _, p := range persons {
			presence[p.Login] = store.HasUnresolvedPersonNote(p.Login)
		}
		board.SetPersonNotes(presence)
	}

	expectIcon := func(t *testing.T, label string, want map[string]bool) {
		t.Helper()
		view := stripANSI(board.View(120, 30))
		// Tab labels look like "Alice (0) ✎" or "Bob (0)". We just
		// check whether the rune appears between two person tab
		// fragments — any false positive would mean the renderer
		// changed shape, which is itself a regression worth catching.
		for _, p := range persons {
			name := p.DisplayName
			tabHas := tabHasIcon(view, name)
			if tabHas != want[p.Login] {
				t.Errorf("%s: %s tab icon=%v, want %v\nview:\n%s",
					label, name, tabHas, want[p.Login], view)
			}
		}
	}

	refresh()
	expectIcon(t, "initial", map[string]bool{
		"alice": true,  // unresolved
		"bob":   false, // no note
		"carol": false, // all resolved
	})

	// Resolve alice's only section → her icon hides.
	if n, err := store.ResolvePersonNote("alice"); err != nil || n != 1 {
		t.Fatalf("ResolvePersonNote(alice) = (%d,%v), want (1,nil)", n, err)
	}
	refresh()
	expectIcon(t, "after :resolve alice", map[string]bool{
		"alice": false,
		"bob":   false,
		"carol": false,
	})

	// Add a fresh unresolved section to alice — her icon comes back
	// even though an earlier section is still resolved.
	resolved, _ := store.LoadPersonNote("alice")
	updated := resolved + "\n## 2026-05-07\n- new topic\n"
	if err := store.SavePersonNote("alice", updated); err != nil {
		t.Fatal(err)
	}
	refresh()
	expectIcon(t, "after new section", map[string]bool{
		"alice": true,
		"bob":   false,
		"carol": false,
	})

	// :unresolve carol reopens her resolved sections → icon appears.
	if n, err := store.UnresolvePersonNote("carol"); err != nil || n != 1 {
		t.Fatalf("UnresolvePersonNote(carol) = (%d,%v), want (1,nil)", n, err)
	}
	refresh()
	expectIcon(t, "after :unresolve carol", map[string]bool{
		"alice": true,
		"bob":   false,
		"carol": true,
	})
}

// tabHasIcon reports whether the tab whose label starts with name has
// the ✎ icon. The renderer formats each tab as "<Name> (<n>)" with
// " ✎" appended only when the person has unresolved notes — so we
// match the literal "<Name> (<n>) ✎" suffix.
func tabHasIcon(view, name string) bool {
	idx := strings.Index(view, name+" (")
	if idx < 0 {
		return false
	}
	closeParen := strings.Index(view[idx:], ")")
	if closeParen < 0 {
		return false
	}
	// Look at what immediately follows the closing paren of this
	// tab's "(N)" count — if it's " ✎", the icon is on this tab.
	after := view[idx+closeParen+1:]
	return strings.HasPrefix(after, " ✎")
}

// stripANSI strips terminal escape codes so substring assertions work
// regardless of lipgloss styling.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			// Skip until 'm' which terminates SGR sequences.
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
