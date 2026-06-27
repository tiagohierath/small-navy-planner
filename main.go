package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const numDays = 5 // days shown on screen, selected day kept in the middle

func journalPath() string {
	if p := os.Getenv("PLANNER_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".planner", "journal.md")
}

// editorCmd is the external editor used by the optional "open in editor"
// action. We deliberately always use vim (not neovim or $EDITOR) so the
// experience is consistent regardless of the user's environment.
func editorCmd() string {
	return "vim"
}

// today returns the current date truncated to midnight (local).
func today() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location())
}

type editorFinishedMsg struct {
	day time.Time
	tmp string
	err error
}

// fullEditFinishedMsg is sent after editing the whole journal file in vim.
type fullEditFinishedMsg struct {
	err error
}

// editMode is the modal state of the UI. Esc steps "up" a level
// (insert → normal → week); the deeper levels keep you inside a day's box.
type editMode int

const (
	modeWeek   editMode = iota // "ultra normal": select and move between days
	modeNormal                 // inside a box, cursor parked (vim-like normal)
	modeInsert                 // inside a box, typing
)

type model struct {
	store    *Store
	selected time.Time // always rendered in the centre column
	width    int
	height   int
	status   string

	// Box editor state, used in modeNormal and modeInsert. editDay is the day
	// whose box is open; lines/curRow/curCol are the working buffer + cursor.
	mode    editMode
	editDay time.Time
	lines   []string // current buffer, one entry per line
	curRow  int      // cursor line index
	curCol  int      // cursor column (rune offset within the line)

	// taskCursor is the index (within the selected day's task lines) of the
	// task highlighted in week mode for quick toggling.
	taskCursor int

	// pending holds a half-typed normal-mode operator (e.g. "d" awaiting the
	// second key of "dd"). Empty when no operator is in flight.
	pending string

	// undo is a stack of buffer snapshots taken before each change in the
	// current box-editing session. "u" pops the most recent.
	undo []editState
}

// editState is a buffer + cursor snapshot for undo.
type editState struct {
	lines    []string
	row, col int
}

// snapshot pushes the current buffer/cursor onto the undo stack.
func (m *model) snapshot() {
	cp := make([]string, len(m.lines))
	copy(cp, m.lines)
	m.undo = append(m.undo, editState{lines: cp, row: m.curRow, col: m.curCol})
}

// restoreUndo pops the last snapshot back into the buffer, reporting success.
func (m *model) restoreUndo() bool {
	if len(m.undo) == 0 {
		return false
	}
	st := m.undo[len(m.undo)-1]
	m.undo = m.undo[:len(m.undo)-1]
	m.lines, m.curRow, m.curCol = st.lines, st.row, st.col
	return true
}

// inBox reports whether a day's box is open (normal or insert mode).
func (m *model) inBox() bool { return m.mode == modeNormal || m.mode == modeInsert }

// A task is any markdown bullet line ("- something" or "* something"),
// optionally carrying a checkbox. checkboxRe matches the explicit-checkbox
// form; bulletRe matches a plain bullet with content. Horizontal rules ("---")
// and empty bullets don't match because a non-space char after the marker is
// required.
var (
	checkboxRe = regexp.MustCompile(`^(\s*[-*]\s+)\[([ xX])\]\s?(.*)$`)
	bulletRe   = regexp.MustCompile(`^(\s*[-*]\s+)(\S.*)$`)
)

// isTaskLine reports whether a line is a toggleable task.
func isTaskLine(l string) bool {
	return checkboxRe.MatchString(l) || bulletRe.MatchString(l)
}

// taskDone reports whether a task line is checked off.
func taskDone(l string) bool {
	if m := checkboxRe.FindStringSubmatch(l); m != nil {
		return m[2] == "x" || m[2] == "X"
	}
	return false
}

// toggleTaskLine flips a task line's completion. A plain bullet is promoted to
// a checked checkbox; a checkbox flips between [ ] and [x]. The bool reports
// whether the line was a task at all.
func toggleTaskLine(l string) (string, bool) {
	if m := checkboxRe.FindStringSubmatch(l); m != nil {
		mark := "x"
		if m[2] == "x" || m[2] == "X" {
			mark = " "
		}
		return m[1] + "[" + mark + "] " + m[3], true
	}
	if m := bulletRe.FindStringSubmatch(l); m != nil {
		return m[1] + "[x] " + m[2], true
	}
	return l, false
}

// taskIndices returns the indices of the task lines within lines.
func taskIndices(lines []string) []int {
	var idx []int
	for i, l := range lines {
		if isTaskLine(l) {
			idx = append(idx, i)
		}
	}
	return idx
}

// dayTasks returns the body lines of the selected day and the indices of the
// lines that are tasks.
func (m *model) dayTasks() (lines []string, taskIdx []int) {
	body := m.store.Get(m.selected)
	if body == "" {
		return nil, nil
	}
	lines = strings.Split(body, "\n")
	return lines, taskIndices(lines)
}

// moveTask shifts the task cursor by delta within the selected day.
func (m *model) moveTask(delta int) {
	_, idx := m.dayTasks()
	if len(idx) == 0 {
		return
	}
	m.taskCursor = clamp(m.taskCursor+delta, 0, len(idx)-1)
	m.status = ""
}

// toggleTask flips the completion state of the highlighted task (week view).
func (m *model) toggleTask() {
	lines, idx := m.dayTasks()
	if len(idx) == 0 {
		return
	}
	ln := idx[clamp(m.taskCursor, 0, len(idx)-1)]
	if nl, ok := toggleTaskLine(lines[ln]); ok {
		lines[ln] = nl
		m.store.Set(m.selected, strings.Join(lines, "\n"))
		if err := m.store.Save(); err != nil {
			m.status = "save error: " + err.Error()
		}
	}
}

func newModel() (*model, error) {
	store, err := LoadStore(journalPath())
	if err != nil {
		return nil, err
	}
	return &model{store: store, selected: today()}, nil
}

func (m *model) Init() tea.Cmd { return nil }

// openEditor dumps the day's body to a temp file and launches $EDITOR on it.
func (m *model) openEditor(day time.Time) tea.Cmd {
	tmp, err := os.CreateTemp("", "planner-*.md")
	if err != nil {
		return func() tea.Msg { return editorFinishedMsg{day: day, err: err} }
	}
	tmp.WriteString(m.store.Get(day))
	tmp.Close()

	editor := editorCmd()
	parts := strings.Fields(editor)
	args := append(parts[1:], tmp.Name())
	c := exec.Command(parts[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{day: day, tmp: tmp.Name(), err: err}
	})
}

// openFullFile opens the entire journal markdown file in vim, with the cursor
// placed on the selected day's section (created empty if it doesn't exist yet).
func (m *model) openFullFile(day time.Time) tea.Cmd {
	line, err := m.store.WriteEnsuring(day)
	if err != nil {
		return func() tea.Msg { return fullEditFinishedMsg{err: err} }
	}
	editor := editorCmd()
	parts := strings.Fields(editor)
	// "+N" tells vim to jump to line N. Falls back gracefully on editors that
	// don't understand it.
	args := append(parts[1:], fmt.Sprintf("+%d", line), m.store.Path())
	c := exec.Command(parts[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return fullEditFinishedMsg{err: err}
	})
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case editorFinishedMsg:
		if msg.tmp != "" {
			defer os.Remove(msg.tmp)
		}
		if msg.err != nil {
			m.status = "editor error: " + msg.err.Error()
			return m, nil
		}
		data, err := os.ReadFile(msg.tmp)
		if err != nil {
			m.status = "read error: " + err.Error()
			return m, nil
		}
		m.store.Set(msg.day, string(data))
		if err := m.store.Save(); err != nil {
			m.status = "save error: " + err.Error()
		} else {
			m.status = "saved " + msg.day.Format(dateLayout)
		}

	case fullEditFinishedMsg:
		if msg.err != nil {
			m.status = "editor error: " + msg.err.Error()
			return m, nil
		}
		// vim may have added/removed/edited any day; re-read from disk.
		if err := m.store.Reload(); err != nil {
			m.status = "reload error: " + err.Error()
		} else {
			m.status = "synced from " + filepath.Base(m.store.Path())
		}

	case tea.KeyMsg:
		switch m.mode {
		case modeInsert:
			return m.updateInsert(msg)
		case modeNormal:
			return m.updateNormal(msg)
		}
		// modeWeek ("ultra normal"): select and move between days.
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "left", "h", "k":
			m.selected = m.selected.AddDate(0, 0, -1)
			m.taskCursor, m.status = 0, ""
		case "right", "l", "j":
			m.selected = m.selected.AddDate(0, 0, 1)
			m.taskCursor, m.status = 0, ""
		case "ctrl+left", "H":
			m.selected = m.selected.AddDate(0, 0, -7)
			m.taskCursor, m.status = 0, ""
		case "ctrl+right", "L":
			m.selected = m.selected.AddDate(0, 0, 7)
			m.taskCursor, m.status = 0, ""
		case "t":
			m.selected = today()
			m.taskCursor, m.status = 0, ""
		case "up":
			m.moveTask(-1)
		case "down":
			m.moveTask(1)
		case " ", "x":
			m.toggleTask()
		case "enter", "i":
			m.openBox(modeInsert)
		case "e", "E":
			return m, m.openEditor(m.selected)
		case "o", "O":
			return m, m.openFullFile(m.selected)
		}
	}
	return m, nil
}

// openBox loads the selected day's body into the box editor in the given mode.
func (m *model) openBox(mode editMode) {
	m.editDay = m.selected
	m.lines = strings.Split(m.store.Get(m.selected), "\n")
	if len(m.lines) == 0 {
		m.lines = []string{""}
	}
	m.curRow = len(m.lines) - 1
	m.curCol = len([]rune(m.lines[m.curRow]))
	m.mode = mode
	m.status = ""
	// Fresh editing session: clear history. If we're going straight into
	// insert, record the loaded buffer as the baseline so the first typing run
	// is undoable.
	m.undo = nil
	if mode == modeInsert {
		m.snapshot()
	}
}

// persistBuffer folds the working buffer back into the store and saves it.
func (m *model) persistBuffer() {
	m.store.Set(m.editDay, strings.Join(m.lines, "\n"))
	if err := m.store.Save(); err != nil {
		m.status = "save error: " + err.Error()
	}
}

// insertLine inserts s at index at, shifting the rest down.
func (m *model) insertLine(at int, s string) {
	at = clamp(at, 0, len(m.lines))
	m.lines = append(m.lines, "")
	copy(m.lines[at+1:], m.lines[at:])
	m.lines[at] = s
}

// clampCursor keeps the cursor on a valid cell. In normal mode the cursor sits
// on a character (max col = line len-1); in insert mode it may sit just past
// the end so you can append.
func (m *model) clampCursor() {
	m.curRow = clamp(m.curRow, 0, len(m.lines)-1)
	hi := len([]rune(m.lines[m.curRow]))
	if m.mode == modeNormal && hi > 0 {
		hi--
	}
	m.curCol = clamp(m.curCol, 0, hi)
}

// toggleCurrentTask toggles the task on the cursor line, if any.
func (m *model) toggleCurrentTask() {
	if m.curRow < 0 || m.curRow >= len(m.lines) {
		return
	}
	if nl, ok := toggleTaskLine(m.lines[m.curRow]); ok {
		m.snapshot()
		m.lines[m.curRow] = nl
		m.persistBuffer()
	}
}

// updateInsert handles key input while typing inside a day's box.
func (m *model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	row := []rune(m.lines[m.curRow])

	switch msg.String() {
	case "esc":
		// Step up one level: stop typing, park the cursor in normal mode.
		m.persistBuffer()
		m.mode = modeNormal
		m.clampCursor()
	case "ctrl+c":
		m.persistBuffer()
		return m, tea.Quit
	case "enter":
		before := string(row[:m.curCol])
		after := string(row[m.curCol:])
		m.lines[m.curRow] = before
		rest := append([]string{after}, m.lines[m.curRow+1:]...)
		m.lines = append(m.lines[:m.curRow+1], rest...)
		m.curRow++
		m.curCol = 0
	case "backspace":
		if m.curCol > 0 {
			m.lines[m.curRow] = string(row[:m.curCol-1]) + string(row[m.curCol:])
			m.curCol--
		} else if m.curRow > 0 {
			prev := []rune(m.lines[m.curRow-1])
			m.curCol = len(prev)
			m.lines[m.curRow-1] = string(prev) + string(row)
			m.lines = append(m.lines[:m.curRow], m.lines[m.curRow+1:]...)
			m.curRow--
		}
	case "delete":
		if m.curCol < len(row) {
			m.lines[m.curRow] = string(row[:m.curCol]) + string(row[m.curCol+1:])
		} else if m.curRow < len(m.lines)-1 {
			m.lines[m.curRow] = string(row) + m.lines[m.curRow+1]
			m.lines = append(m.lines[:m.curRow+1], m.lines[m.curRow+2:]...)
		}
	case "left":
		if m.curCol > 0 {
			m.curCol--
		} else if m.curRow > 0 {
			m.curRow--
			m.curCol = len([]rune(m.lines[m.curRow]))
		}
	case "right":
		if m.curCol < len(row) {
			m.curCol++
		} else if m.curRow < len(m.lines)-1 {
			m.curRow++
			m.curCol = 0
		}
	case "up":
		if m.curRow > 0 {
			m.curRow--
			m.curCol = clamp(m.curCol, 0, len([]rune(m.lines[m.curRow])))
		}
	case "down":
		if m.curRow < len(m.lines)-1 {
			m.curRow++
			m.curCol = clamp(m.curCol, 0, len([]rune(m.lines[m.curRow])))
		}
	case "home":
		m.curCol = 0
	case "end":
		m.curCol = len(row)
	case "tab":
		m.lines[m.curRow] = string(row[:m.curCol]) + "  " + string(row[m.curCol:])
		m.curCol += 2
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			ins := msg.Runes
			if msg.Type == tea.KeySpace {
				ins = []rune{' '}
			}
			m.lines[m.curRow] = string(row[:m.curCol]) + string(ins) + string(row[m.curCol:])
			m.curCol += len(ins)
		}
	}
	return m, nil
}

// updateNormal handles key input while the cursor is parked in a box (vim-like
// normal mode). Esc/q step up to week mode; i/a/o drop into insert.
func (m *model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()

	// Resolve a pending operator. The only one so far is "d", which forms "dd"
	// (delete line). Any other follow-up key cancels it and is handled as a
	// fresh command below.
	if m.pending == "d" {
		m.pending = ""
		m.status = ""
		if s == "d" {
			m.snapshot()
			m.deleteLine(m.curRow)
			m.persistBuffer()
			m.clampCursor()
			return m, nil
		}
	}

	row := []rune(m.lines[m.curRow])
	lineEnd := len(row) - 1
	if lineEnd < 0 {
		lineEnd = 0
	}

	// Snapshot before any change so "u" can revert it. Entering insert is one
	// change boundary (a whole typing run undoes at once); "D" edits directly.
	switch s {
	case "i", "a", "A", "I", "o", "O", "D":
		m.snapshot()
	}

	switch s {
	case "esc", "q":
		// Step up one level: leave the box, back to day selection.
		m.persistBuffer()
		m.mode = modeWeek
		return m, nil
	case "ctrl+c":
		m.persistBuffer()
		return m, tea.Quit
	case "E":
		m.persistBuffer()
		return m, m.openEditor(m.editDay)

	// enter insert mode
	case "i":
		m.mode = modeInsert
	case "a":
		if m.curCol < len(row) {
			m.curCol++
		}
		m.mode = modeInsert
	case "A":
		m.curCol = len(row)
		m.mode = modeInsert
	case "I":
		m.curCol = 0
		m.mode = modeInsert
	case "o":
		m.insertLine(m.curRow+1, "")
		m.curRow++
		m.curCol = 0
		m.mode = modeInsert
	case "O":
		m.insertLine(m.curRow, "")
		m.curCol = 0
		m.mode = modeInsert

	// movement
	case "h", "left":
		m.curCol--
	case "l", "right":
		m.curCol++
	case "k", "up":
		m.curRow--
	case "j", "down":
		m.curRow++
	case "0", "home":
		m.curCol = 0
	case "$", "end":
		m.curCol = lineEnd
	case "g":
		m.curRow, m.curCol = 0, 0
	case "G":
		m.curRow, m.curCol = len(m.lines)-1, 0

	// delete
	case "d":
		m.pending = "d" // wait for the second "d"
		m.status = "d…"
		return m, nil
	case "D":
		m.lines[m.curRow] = string(row[:m.curCol]) // delete to end of line
		m.persistBuffer()

	// undo
	case "u":
		if m.restoreUndo() {
			m.persistBuffer()
			m.status = "undo"
		} else {
			m.status = "nothing to undo"
		}

	// tasks
	case " ", "x":
		m.toggleCurrentTask()
	}

	m.clampCursor()
	return m, nil
}

// deleteLine removes the given line, keeping at least one (empty) line so the
// buffer is never empty.
func (m *model) deleteLine(row int) {
	if row < 0 || row >= len(m.lines) {
		return
	}
	if len(m.lines) == 1 {
		m.lines[0] = ""
		m.curRow, m.curCol = 0, 0
		return
	}
	m.lines = append(m.lines[:row], m.lines[row+1:]...)
	if m.curRow >= len(m.lines) {
		m.curRow = len(m.lines) - 1
	}
	m.curCol = 0
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	weekStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	selectedBoxStyle = boxStyle.
				BorderForeground(lipgloss.Color("212"))

	// Box border colour signals the mode: green = insert, yellow = normal.
	insertBoxStyle = boxStyle.
			BorderForeground(lipgloss.Color("78")).
			BorderStyle(lipgloss.ThickBorder())
	normalBoxStyle = boxStyle.
			BorderForeground(lipgloss.Color("214")).
			BorderStyle(lipgloss.ThickBorder())

	dayTitleStyle = lipgloss.NewStyle().Bold(true)
	todayDotStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

	insertCursorStyle = lipgloss.NewStyle().Reverse(true)
	normalCursorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("232")).
				Background(lipgloss.Color("214"))

	taskDoneStyle = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	taskSelStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
)

// renderDayBody renders a day's stored text, dimming completed tasks and (for
// the selected day) highlighting the task currently under the task cursor.
func (m *model) renderDayBody(day time.Time, selected bool) string {
	body := m.store.Get(day)
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")

	// Locate the highlighted task line on the selected day.
	cursorLine := -1
	if selected {
		if idx := taskIndices(lines); len(idx) > 0 {
			cursorLine = idx[clamp(m.taskCursor, 0, len(idx)-1)]
		}
	}

	for i, l := range lines {
		switch {
		case i == cursorLine:
			lines[i] = taskSelStyle.Render(l)
		case taskDone(l):
			lines[i] = taskDoneStyle.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

// renderBuffer renders the box editor buffer with the cursor drawn using the
// given style (which encodes the current mode).
func (m *model) renderBuffer(cur lipgloss.Style) string {
	out := make([]string, len(m.lines))
	for r, line := range m.lines {
		runes := []rune(line)
		if r != m.curRow {
			out[r] = line
			continue
		}
		switch {
		case m.curCol >= len(runes):
			out[r] = line + cur.Render(" ")
		default:
			out[r] = string(runes[:m.curCol]) +
				cur.Render(string(runes[m.curCol])) +
				string(runes[m.curCol+1:])
		}
	}
	return strings.Join(out, "\n")
}

func (m *model) View() string {
	if m.width == 0 {
		return "loading…"
	}

	year, week := m.selected.ISOWeek()
	title := headerStyle.Render("📓 planner")
	sub := weekStyle.Render(fmt.Sprintf("  %d · week %d", year, week))
	header := title + sub

	// Layout: slice the full terminal width into numDays adjacent columns
	// (the cake, not slices on a plate). lipgloss Width() covers the content +
	// padding but not the border, so each box also spends 2 cols on its left
	// and right border (chrome). colInner is therefore that Width() value; the
	// usable text area inside is colInner minus the 2 cols of padding. Boxes
	// sit flush with no gaps, and any remainder from the division is handed out
	// one column at a time so the row spans the screen exactly.
	const chrome = 2
	const selWeight = 2 // the centre (selected) column is this many normal columns wide
	innerTotal := m.width - numDays*chrome
	// Weighted split: every column is 1 unit except the selected centre column,
	// which is selWeight units, so the row holds (numDays-1)+selWeight units.
	units := (numDays - 1) + selWeight
	base := innerTotal / units
	rem := innerTotal % units
	if base < 6 {
		base, rem = 6, 0
	}
	selIdx := numDays / 2 // centre column, always the selected day

	boxHeight := m.height - 6 // header + footer + breathing room
	if boxHeight < 5 {
		boxHeight = 5
	}

	start := m.selected.AddDate(0, 0, -(numDays / 2))
	now := today()

	cols := make([]string, numDays)
	for i := 0; i < numDays; i++ {
		// The centre column is selWeight units wide and absorbs any remainder,
		// so the row spans the screen exactly while staying symmetric around it.
		colInner := base
		if i == selIdx {
			colInner = base*selWeight + rem
		}

		day := start.AddDate(0, 0, i)
		isSel := day.Equal(m.selected)

		label := fmt.Sprintf("%s %d/%d", day.Weekday().String()[:3], int(day.Month()), day.Day())
		titleLine := dayTitleStyle.Render(label)
		if day.Equal(now) {
			titleLine += todayDotStyle.Render(" ●")
		}

		boxOpen := m.inBox() && day.Equal(m.editDay)
		var body string
		if boxOpen {
			cur := insertCursorStyle
			if m.mode == modeNormal {
				cur = normalCursorStyle
			}
			body = m.renderBuffer(cur)
		} else {
			body = m.renderDayBody(day, isSel)
		}
		// boxStyle has 1 col of horizontal padding per side, so the usable
		// text width is colInner-2; size the divider to that to avoid wrapping.
		divW := colInner - 2
		if divW < 1 {
			divW = 1
		}
		content := titleLine + "\n" + strings.Repeat("─", divW) + "\n" + body

		style := boxStyle
		switch {
		case boxOpen && m.mode == modeInsert:
			style = insertBoxStyle
		case boxOpen && m.mode == modeNormal:
			style = normalBoxStyle
		case isSel:
			style = selectedBoxStyle
		}
		cols[i] = style.Width(colInner).Height(boxHeight).Render(content)
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	var hint string
	switch m.mode {
	case modeInsert:
		hint = "-- INSERT --  type freely · esc → normal · ctrl+c quit"
	case modeNormal:
		hint = "-- NORMAL --  hjkl · i/a/o insert · dd/D del · u undo · space ✓ · E vim · esc/q → days"
	default:
		hint = "-- DAYS --  j/k day · H/L week · ↑↓/space task · i edit · E vim · o file · q quit"
	}
	footer := footerStyle.Render(hint)
	if m.status != "" {
		footer = statusStyle.Render(m.status) + "  " + footer
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, "", row, "", footer)
}

func main() {
	m, err := newModel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
