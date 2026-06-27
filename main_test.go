package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel(body string) *model {
	m := &model{store: &Store{path: "/dev/null", days: map[string]string{}}, selected: today()}
	if body != "" {
		m.store.days[m.selected.Format(dateLayout)] = body
	}
	return m
}

func runes(s string) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func TestIsTaskLine(t *testing.T) {
	cases := map[string]bool{
		"- a task":       true,
		"* star task":    true,
		"  - nested":     true,
		"- [ ] checkbox": true,
		"- [x] done":     true,
		"---":            false, // horizontal rule
		"-no space":      false,
		"plain text":     false,
		"- ":             false, // empty bullet
		"# header":       false,
	}
	for line, want := range cases {
		if got := isTaskLine(line); got != want {
			t.Errorf("isTaskLine(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestToggleTaskLine(t *testing.T) {
	cases := []struct{ in, out string }{
		{"- milk", "- [x] milk"},     // plain bullet -> done
		{"- [ ] milk", "- [x] milk"}, // open -> done
		{"- [x] milk", "- [ ] milk"}, // done -> open
		{"  - [ ] nested", "  - [x] nested"},
		{"* star", "* [x] star"},
	}
	for _, c := range cases {
		got, ok := toggleTaskLine(c.in)
		if !ok || got != c.out {
			t.Errorf("toggleTaskLine(%q) = %q,%v want %q,true", c.in, got, ok, c.out)
		}
	}
	if _, ok := toggleTaskLine("not a task"); ok {
		t.Errorf("non-task should not toggle")
	}
}

// A capital E must be typable in insert mode (regression: it used to open vim).
func TestInsertTypesCapitalE(t *testing.T) {
	m := newTestModel("")
	m.openBox(modeInsert)
	for _, r := range "EAT" {
		m.updateInsert(runes(string(r)))
	}
	if got := strings.Join(m.lines, "\n"); got != "EAT" {
		t.Fatalf("typed %q, want EAT", got)
	}
}

// Esc steps insert -> normal; a second Esc steps normal -> week, persisting.
func TestModalEscalation(t *testing.T) {
	m := newTestModel("")
	m.openBox(modeInsert)
	for _, r := range "- milk" {
		m.updateInsert(runes(string(r)))
	}
	m.updateInsert(key(tea.KeyEscape))
	if m.mode != modeNormal {
		t.Fatalf("after esc want normal, got %v", m.mode)
	}
	// in normal mode, space toggles the task on the cursor line
	m.updateNormal(runes(" "))
	if got := m.store.Get(m.selected); !strings.Contains(got, "- [x] milk") {
		t.Fatalf("space did not complete task: %q", got)
	}
	m.updateNormal(runes("q"))
	if m.mode != modeWeek {
		t.Fatalf("after q want week, got %v", m.mode)
	}
}

// Normal-mode cursor cannot run off the end of a line.
func TestNormalCursorClamp(t *testing.T) {
	m := newTestModel("hi")
	m.openBox(modeNormal)
	m.curRow, m.curCol = 0, 0
	for i := 0; i < 10; i++ {
		m.updateNormal(runes("l"))
	}
	if m.curCol != 1 { // len("hi")-1
		t.Fatalf("curCol = %d, want 1", m.curCol)
	}
}

func TestNormalDeleteLine(t *testing.T) {
	m := newTestModel("one\ntwo\nthree")
	m.openBox(modeNormal)
	m.curRow, m.curCol = 1, 0 // on "two"
	// dd deletes the current line
	m.updateNormal(runes("d"))
	if m.pending != "d" {
		t.Fatalf("first d should set pending, got %q", m.pending)
	}
	m.updateNormal(runes("d"))
	if got := strings.Join(m.lines, "\n"); got != "one\nthree" {
		t.Fatalf("after dd got %q, want one\\nthree", got)
	}
	if m.pending != "" {
		t.Fatalf("pending should clear after dd")
	}
	// a lone d followed by a non-d cancels (no deletion)
	m.updateNormal(runes("d"))
	m.updateNormal(runes("j")) // cancels, moves down
	if got := strings.Join(m.lines, "\n"); got != "one\nthree" {
		t.Fatalf("d then j should not delete, got %q", got)
	}
	// D deletes from cursor to end of line
	m.curRow, m.curCol = 0, 1 // "one", before "ne"
	m.updateNormal(runes("D"))
	if m.lines[0] != "o" {
		t.Fatalf("D gave %q, want o", m.lines[0])
	}
}

func TestDeleteLineKeepsOne(t *testing.T) {
	m := newTestModel("only line")
	m.openBox(modeNormal)
	m.curRow, m.curCol = 0, 0
	m.updateNormal(runes("d"))
	m.updateNormal(runes("d"))
	if len(m.lines) != 1 || m.lines[0] != "" {
		t.Fatalf("dd on last line should leave one empty line, got %q", m.lines)
	}
}

func TestUndo(t *testing.T) {
	// undo a dd
	m := newTestModel("one\ntwo\nthree")
	m.openBox(modeNormal)
	m.curRow = 1
	m.updateNormal(runes("d"))
	m.updateNormal(runes("d")) // delete "two"
	if got := strings.Join(m.lines, "\n"); got != "one\nthree" {
		t.Fatalf("pre-undo got %q", got)
	}
	m.updateNormal(runes("u"))
	if got := strings.Join(m.lines, "\n"); got != "one\ntwo\nthree" {
		t.Fatalf("after undo got %q, want original", got)
	}

	// undo a task toggle
	m2 := newTestModel("- milk")
	m2.openBox(modeNormal)
	m2.updateNormal(runes(" ")) // -> "- [x] milk"
	m2.updateNormal(runes("u"))
	if m2.lines[0] != "- milk" {
		t.Fatalf("undo toggle got %q", m2.lines[0])
	}

	// a whole insert run undoes at once
	m3 := newTestModel("")
	m3.openBox(modeInsert)
	for _, r := range "hello world" {
		m3.updateInsert(runes(string(r)))
	}
	m3.updateInsert(key(tea.KeyEscape)) // -> normal
	m3.updateNormal(runes("u"))
	if got := strings.Join(m3.lines, "\n"); got != "" {
		t.Fatalf("undo insert run got %q, want empty", got)
	}

	// nothing to undo is harmless
	m4 := newTestModel("x")
	m4.openBox(modeNormal)
	m4.updateNormal(runes("u"))
	if m4.lines[0] != "x" {
		t.Fatalf("empty undo mutated buffer: %q", m4.lines[0])
	}
}

func TestEmptySectionDropped(t *testing.T) {
	m := newTestModel("")
	day := m.selected
	// open a box, type nothing, persist -> day must not be stored
	m.openBox(modeInsert)
	m.persistBuffer()
	if _, ok := m.store.days[day.Format(dateLayout)]; ok {
		t.Fatalf("empty day should not be stored")
	}
	_ = time.Now()
}
