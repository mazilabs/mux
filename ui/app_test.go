package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lunemis/mux/tmux"
)

// Fork patch tests (OQ9, 2026-09-15): Escape closes the popup from the list,
// but still only clears an active filter first.

func escKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEsc} }

func TestEscFromListQuits(t *testing.T) {
	m := Model{}
	_, cmd := m.updateList(escKey())
	if cmd == nil {
		t.Fatal("expected quit command from list with empty filter, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd())
	}
}

func TestEscClearsFilterBeforeQuitting(t *testing.T) {
	m := Model{
		sessions:   []tmux.Session{{Name: "alpha"}, {Name: "beta"}},
		filterText: "bet",
	}
	m.tree = newTreeState()
	m.applyFilter()
	if len(m.filtered) != 1 {
		t.Fatalf("precondition: expected 1 filtered session, got %d", len(m.filtered))
	}

	updated, cmd := m.updateList(escKey())
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("esc with active filter must clear the filter, not quit")
		}
	}
	if got := updated.(Model).filterText; got != "" {
		t.Fatalf("expected filter cleared, got %q", got)
	}
}

func TestQStillQuits(t *testing.T) {
	m := Model{}
	_, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q must still quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd())
	}
}

func TestEscInRenameReturnsToList(t *testing.T) {
	m := Model{mode: modeRename, renameModel: newRenameModel("alpha")}
	updated, cmd := m.updateRename(escKey())
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("esc in rename input must not quit the popup")
		}
	}
	if got := updated.(Model).mode; got != modeList {
		t.Fatalf("expected modeList after esc in rename, got %d", got)
	}
}

// --- Quick-cycle mode tests (OQ8, fork patch 2026-09-16) ---
// docs/changelog/2026-09-16-b-option-tab-quick-switch.md §4

func tabKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyTab} }

func quickLoaded(t *testing.T, n int) Model {
	t.Helper()
	sessions := make([]tmux.Session, n)
	for i := range sessions {
		sessions[i] = tmux.Session{Name: fmt.Sprintf("s%d", i)}
	}
	m := NewModelWithCycle(true)
	updated, _ := m.Update(sessionsLoadedMsg{sessions: sessions})
	return updated.(Model)
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// ① quick mode: first load places cursor on row 2 (previous session).
func TestQuickFirstLoadCursorOnRow2(t *testing.T) {
	m := quickLoaded(t, 3)
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1 after first load, got %d", m.cursor)
	}
	// refresh (second load) must NOT re-position the cursor
	updated, _ := m.Update(sessionsLoadedMsg{sessions: m.sessions})
	if updated.(Model).cursor != 1 {
		t.Fatal("second load must keep the cursor")
	}
}

// ②a combined ESC+Tab (the real wire form: Bubbletea delivers ONE alt+tab
// message, key_sequences.go extSequences) advances the cursor and wraps.
func TestQuickAltTabAdvancesAndWraps(t *testing.T) {
	m := quickLoaded(t, 3) // cursor 1
	altTab := tea.KeyMsg{Type: tea.KeyTab, Alt: true}
	for _, want := range []int{2, 0, 1} {
		updated, _ := m.updateList(altTab)
		m = updated.(Model)
		if m.cursor != want {
			t.Fatalf("after alt+tab expected cursor %d, got %d", want, m.cursor)
		}
	}
}

// ②b split esc-then-tab (fallback if bytes ever arrive in separate reads).
func TestQuickEscTabAdvancesAndWraps(t *testing.T) {
	m := quickLoaded(t, 3) // cursor 1
	for _, want := range []int{2, 0, 1} {
		updated, _ := m.Update(escKey())
		updated, _ = updated.(Model).updateList(tabKey())
		m = updated.(Model)
		if m.cursor != want {
			t.Fatalf("after esc+tab expected cursor %d, got %d", want, m.cursor)
		}
		if m.pendingEsc {
			t.Fatal("pendingEsc must be consumed by tab")
		}
	}
}

// ③ esc alone closes (delta-1 regression): timeout after arm must quit.
func TestQuickEscAloneCloses(t *testing.T) {
	m := quickLoaded(t, 3)
	updated, cmd := m.updateList(escKey())
	if isQuit(cmd) {
		t.Fatal("esc in quick mode must arm, not quit immediately")
	}
	m2 := updated.(Model)
	if !m2.pendingEsc {
		t.Fatal("esc must arm pendingEsc")
	}
	updated, cmd = m2.updateList(escArmTimeoutMsg{})
	if !isQuit(cmd) {
		t.Fatal("esc timeout without tab must quit (delta-1)")
	}
	// any other key while armed also applies esc semantics first
	_, cmd = m2.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if !isQuit(cmd) {
		t.Fatal("other key while esc-armed must quit")
	}
}

// ④ normal mode: esc+tab closes right away (delta-1, decision 2 — old popup
// unchanged; tab after esc never cycles).
func TestNormalModeEscTabCloses(t *testing.T) {
	m := NewModel()
	updated, cmd := m.updateList(escKey())
	if !isQuit(cmd) {
		t.Fatal("normal mode esc must quit immediately (no arm)")
	}
	if updated.(Model).pendingEsc {
		t.Fatal("normal mode must not arm the cycle")
	}
}

// ⑤ commit attaches the current selection (same path as Enter).
func TestQuickCommitAttachesSelection(t *testing.T) {
	m := quickLoaded(t, 3) // cursor 1 → s1
	updated, cmd := m.Update(CycleCommitMsg{})
	if !isQuit(cmd) {
		t.Fatal("commit must quit")
	}
	mm := updated.(Model)
	if mm.AttachName() != "s1" {
		t.Fatalf("expected attach s1, got %q", mm.AttachName())
	}
}

// ⑤b commit is ignored outside quick mode (stray SIGUSR1 cannot hijack the
// normal Option+E popup).
func TestCommitIgnoredInNormalMode(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(CycleCommitMsg{})
	if cmd != nil {
		t.Fatal("normal mode must ignore CycleCommitMsg")
	}
	if updated.(Model).AttachName() != "" {
		t.Fatal("normal mode commit must not set an attach target")
	}
}

// ⑥ single session: cursor stays 0, commit = attach current (no-op switch).
func TestQuickSingleSession(t *testing.T) {
	m := quickLoaded(t, 1)
	if m.cursor != 0 {
		t.Fatalf("single session cursor must stay 0, got %d", m.cursor)
	}
	updated, _ := m.updateList(escKey())
	updated, _ = updated.(Model).updateList(tabKey())
	if updated.(Model).cursor != 0 {
		t.Fatal("wrap on single-row list must keep cursor 0")
	}
	updated, cmd := updated.(Model).Update(CycleCommitMsg{})
	if !isQuit(cmd) {
		t.Fatal("commit must quit")
	}
	if updated.(Model).AttachName() != "s0" {
		t.Fatalf("expected attach s0, got %q", updated.(Model).AttachName())
	}
}

// --- Reverse cycle tests (OQ11, fork patch 2026-09-17) ---
// docs/changelog/2026-09-17-a-option-tab-reverse-cycle.md §4

// revKey is the Gate ① winner: ESC+GS arrives as ONE alt+ctrl+] message
// (pty-probe 2026-09-17; keyGS = 29 = 0x1d).
func revKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlCloseBracket, Alt: true} }

// ⑦ reverse decrements from the start row (row 2 → row 1).
func TestQuickReverseDecrementsFromStart(t *testing.T) {
	m := quickLoaded(t, 3) // cursor 1
	updated, _ := m.updateList(revKey())
	if got := updated.(Model).cursor; got != 0 {
		t.Fatalf("reverse from start row expected cursor 0, got %d", got)
	}
}

// ⑧ reverse wraps row 1 → last row.
func TestQuickReverseWrapsTopToBottom(t *testing.T) {
	m := quickLoaded(t, 3) // cursor 1
	updated, _ := m.updateList(revKey())
	m = updated.(Model) // cursor 0
	updated, _ = m.updateList(revKey())
	if got := updated.(Model).cursor; got != 2 {
		t.Fatalf("reverse wrap from row 1 expected last row 2, got %d", got)
	}
}

// ⑨ forward and reverse mix freely in one hold (Context behavior).
func TestQuickMixedDirections(t *testing.T) {
	m := quickLoaded(t, 3) // cursor 1
	for _, step := range []struct {
		key  tea.KeyMsg
		want int
	}{
		{revKey(), 0},        // up
		{revKey(), 2},        // up, wrap
		{tea.KeyMsg{Type: tea.KeyTab, Alt: true}, 0}, // forward combined (alt+tab)
		{revKey(), 2},        // up again from row 1
	} {
		updated, _ := m.updateList(step.key)
		m = updated.(Model)
		if m.cursor != step.want {
			t.Fatalf("mixed sequence key %q expected cursor %d, got %d", step.key.String(), step.want, m.cursor)
		}
	}
}

// ⑩ normal mode ignores the reverse message (stray byte must not hijack the
// Option+E popup — same guard as the SIGUSR1 commit).
func TestNormalModeIgnoresReverseKey(t *testing.T) {
	m := NewModel()
	updated, cmd := m.updateList(revKey())
	if isQuit(cmd) {
		t.Fatal("reverse key must not quit the normal popup")
	}
	if updated.(Model).cursor != m.cursor {
		t.Fatal("reverse key must not move the cursor in normal mode")
	}
}

// ⑪ single session: reverse is a no-op on the only row, commit still attaches.
func TestQuickSingleSessionReverse(t *testing.T) {
	m := quickLoaded(t, 1)
	updated, _ := m.updateList(revKey())
	if updated.(Model).cursor != 0 {
		t.Fatal("reverse on single-row list must keep cursor 0")
	}
	updated, cmd := updated.(Model).Update(CycleCommitMsg{})
	if !isQuit(cmd) || updated.(Model).AttachName() != "s0" {
		t.Fatal("commit after reverse must attach the single session")
	}
}

// quickLoadedReverse loads n sessions with --quick-cycle --reverse-start (OQ11).
func quickLoadedReverse(t *testing.T, n int) Model {
	t.Helper()
	sessions := make([]tmux.Session, n)
	for i := range sessions {
		sessions[i] = tmux.Session{Name: fmt.Sprintf("s%d", i)}
	}
	m := NewModelQuickCycleStart(true, true)
	updated, _ := m.Update(sessionsLoadedMsg{sessions: sessions})
	return updated.(Model)
}

// ⑫ reverse-start open lands on the LAST row (opening tap = first reverse step).
func TestQuickReverseStartOpensOnLastRow(t *testing.T) {
	m := quickLoadedReverse(t, 3)
	if m.cursor != 2 {
		t.Fatalf("reverse-start open expected last row 2, got %d", m.cursor)
	}
}

// ⑬ reverse-start + commit attaches the last session; forward tap from last
// row wraps to row 1 (modular mixing stays consistent).
func TestQuickReverseStartCommitAndWrap(t *testing.T) {
	m := quickLoadedReverse(t, 3) // cursor 2
	updated, cmd := m.Update(CycleCommitMsg{})
	if !isQuit(cmd) || updated.(Model).AttachName() != "s2" {
		t.Fatal("reverse-start commit expected attach s2 (last row)")
	}
	updated, _ = m.updateList(tea.KeyMsg{Type: tea.KeyTab, Alt: true}) // forward from last row
	if got := updated.(Model).cursor; got != 0 {
		t.Fatalf("forward from last row expected wrap to 0, got %d", got)
	}
	updated, _ = m.updateList(revKey()) // reverse from last row
	if got := updated.(Model).cursor; got != 1 {
		t.Fatalf("reverse from last row expected 1, got %d", got)
	}
}

// --- j/k wrap-around tests (OQ12, fork patch 2026-09-17) ---
// docs/changelog/2026-09-17-b-jk-wrap-navigation.md §2

// loadedNormal loads n sessions in normal (Option+E) mode: cursor 0.
func loadedNormal(t *testing.T, n int) Model {
	t.Helper()
	sessions := make([]tmux.Session, n)
	for i := range sessions {
		sessions[i] = tmux.Session{Name: fmt.Sprintf("s%d", i)}
	}
	m := NewModel()
	updated, _ := m.Update(sessionsLoadedMsg{sessions: sessions})
	return updated.(Model)
}

func runesKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// ① wrap down: j on the last row lands on row 1.
func TestListWrapDownFromLastRow(t *testing.T) {
	m := loadedNormal(t, 3)
	m.cursor = len(m.items) - 1
	updated, _ := m.updateList(runesKey('j'))
	if got := updated.(Model).cursor; got != 0 {
		t.Fatalf("j on last row expected wrap to 0, got %d", got)
	}
}

// ② wrap up: k on row 1 lands on the last row.
func TestListWrapUpFromFirstRow(t *testing.T) {
	m := loadedNormal(t, 3)
	updated, _ := m.updateList(runesKey('k'))
	if got := updated.(Model).cursor; got != 2 {
		t.Fatalf("k on first row expected wrap to 2, got %d", got)
	}
}

// ③ mid-list stepping unchanged (regression).
func TestListMidSteppingUnchanged(t *testing.T) {
	m := loadedNormal(t, 3)
	m.cursor = 1
	updated, _ := m.updateList(runesKey('j'))
	m = updated.(Model)
	if m.cursor != 2 {
		t.Fatalf("j mid-list expected 1→2, got %d", m.cursor)
	}
	updated, _ = m.updateList(runesKey('k'))
	if got := updated.(Model).cursor; got != 1 {
		t.Fatalf("k mid-list expected 2→1, got %d", got)
	}
}

// ④ single row: j and k keep cursor 0 (len guard + modulo on 1).
func TestListSingleRowStays(t *testing.T) {
	m := loadedNormal(t, 1)
	for _, key := range []tea.KeyMsg{runesKey('j'), runesKey('k')} {
		updated, _ := m.updateList(key)
		if got := updated.(Model).cursor; got != 0 {
			t.Fatalf("single-row %v must keep cursor 0, got %d", key.Runes, got)
		}
	}
}

// ⑤ arrow keys share the wrap branches (same case).
func TestListArrowsWrap(t *testing.T) {
	m := loadedNormal(t, 3)
	m.cursor = 2
	updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("down on last row expected wrap to 0, got %d", m.cursor)
	}
	updated, _ = m.updateList(tea.KeyMsg{Type: tea.KeyUp})
	if got := updated.(Model).cursor; got != 2 {
		t.Fatalf("up on first row expected wrap to 2, got %d", got)
	}
}

// ⑥ empty list: j/k and arrows are inert, no panic (len guard).
func TestListEmptyKeysInert(t *testing.T) {
	m := NewModel()
	for _, key := range []tea.KeyMsg{runesKey('j'), runesKey('k'), {Type: tea.KeyDown}, {Type: tea.KeyUp}} {
		updated, _ := m.updateList(key)
		if got := updated.(Model).cursor; got != 0 {
			t.Fatalf("empty list must keep cursor 0, got %d", got)
		}
	}
}
