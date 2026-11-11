package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/morks/stackit_mock/internal/store"
)

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

// Regression: with no rows, moving down used to set cursor=-1 and View() panicked.
func TestNavigationWithoutRowsDoesNotPanic(t *testing.T) {
	m := New("http://localhost:0/_admin")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	for _, k := range []string{"down", "down", "up", "up", "esc", "j", "k", "down"} {
		m = press(m, k)
		_ = m.View() // must not panic after any single key
		if m.cursor != 0 {
			t.Fatalf("after %q cursor = %d, want 0", k, m.cursor)
		}
	}
}

func TestCursorStaysInRangeWhenRowsShrink(t *testing.T) {
	m := New("http://localhost:0/_admin")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	res := []store.Resource{{Type: "iaas/network", Service: "iaas", Project: "p", ID: "1", Name: "n", Status: "CREATED"}}
	next, _ = m.Update(dataMsg{res: res})
	m = next.(Model)
	m = press(m, "down", "down", "down", "down")
	_ = m.View()
	next, _ = m.Update(dataMsg{res: nil}) // everything deleted
	m = next.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor = %d after rows vanished, want 0", m.cursor)
	}
	_ = m.View()
}

func TestViewFitsTerminalHeight(t *testing.T) {
	for _, h := range []int{24, 40, 60} {
		m := New("http://x")
		next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: h})
		if got := strings.Count(next.(Model).View(), "\n") + 1; got > h {
			t.Errorf("height %d: view has %d lines", h, got)
		}
	}
}
