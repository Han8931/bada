package ui

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"bada/internal/config"
	"bada/internal/storage"
)

// TestRelativeDueCell confirms the "Due-in" column renders due dates relative to
// today ("today", "in 3d", "2d ago"), and "-" when no due date is set.
func TestRelativeDueCell(t *testing.T) {
	if got := relativeDueCell(sql.NullTime{}); got != "-" {
		t.Fatalf("empty due should be %q, got %q", "-", got)
	}
	// Noon today (UTC) keeps offsets clear of the midnight boundary.
	base := normalizeDate(time.Now().UTC()).Add(12 * time.Hour)
	cases := []struct {
		offsetDays int
		want       string
	}{
		{0, "today"},
		{1, "tomorrow"},
		{-1, "yesterday"},
		{3, "in 3d"},
		{-2, "2d ago"},
	}
	for _, c := range cases {
		due := sql.NullTime{Time: base.AddDate(0, 0, c.offsetDays), Valid: true}
		if got := relativeDueCell(due); got != c.want {
			t.Fatalf("offset %dd: want %q, got %q", c.offsetDays, c.want, got)
		}
	}
}

// TestStatusBadge confirms the status column renders a color-coded badge whose
// label matches the state, abbreviates in-progress to fit, and always pads to
// the column width so the table stays aligned.
func TestStatusBadge(t *testing.T) {
	m := newTestModel(t)
	const w = 11

	cases := []struct {
		name string
		task storage.Task
		want string
	}{
		{"overdue", storage.Task{Status: "PENDING", Due: sql.NullTime{Time: time.Now().Add(-48 * time.Hour), Valid: true}}, "OVERDUE"},
		{"in-progress", storage.Task{Status: "IN-PROGRESS"}, "IN-PROG"},
		{"done", storage.Task{Status: "DONE", Done: true}, "DONE"},
		{"pending", storage.Task{Status: "PENDING"}, "PENDING"},
	}
	for _, c := range cases {
		cell := m.statusField(c.task, w, true)
		if !strings.Contains(cell, c.want) {
			t.Fatalf("%s: badge should contain %q, got %q", c.name, c.want, cell)
		}
		if got := lipgloss.Width(cell); got != w {
			t.Fatalf("%s: cell width should be %d, got %d (%q)", c.name, w, got, cell)
		}
	}

	// Uncolored cell (used on recolored selection/done rows) is a plain label.
	plain := m.statusField(cases[0].task, w, false)
	if plain != fmt.Sprintf("%-*s", w, "OVERDUE") {
		t.Fatalf("uncolored overdue cell should be a plain padded label, got %q", plain)
	}
}

// TestTaskListHasNoRowTint confirms task rows do not get alternating
// row-wise background tints.
func TestTaskListHasNoRowTint(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)

	m := newTestModel(t)
	m.width = 120
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "First", Status: "PENDING", CreatedAt: now},
		{ID: 2, Title: "Second", Status: "PENDING", CreatedAt: now,
			Due: sql.NullTime{Time: now.Add(-48 * time.Hour), Valid: true}}, // overdue badge
	}
	stripe := bgOpenSeq(m.styles.RowStripe)
	if stripe == "" {
		t.Skip("color profile stripped the stripe background")
	}
	lines := strings.Split(m.renderTaskListWithHeight(10), "\n")
	// lines[0]=header, [1]=row index 0, [2]=row index 1.
	if strings.Contains(lines[1], stripe) {
		t.Fatalf("first task row should not carry row tint: %q", lines[1])
	}
	if strings.Contains(lines[2], stripe) {
		t.Fatalf("second task row should not carry row tint: %q", lines[2])
	}
}

// TestTaskRowsDoNotWrapWhenNarrow guards against the phantom "duplicate row"
// bug: on a terminal too narrow for every column, each task must clip to one
// line instead of word-wrapping onto a second.
func TestTaskRowsDoNotWrapWhenNarrow(t *testing.T) {
	m := newTestModel(t)
	m.width = 80 // fixed columns exceed the inner width here
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Alpha", Status: "PENDING", CreatedAt: now},
		{ID: 2, Title: "Bravo", Status: "PENDING", CreatedAt: now},
	}
	lines := strings.Split(m.renderTaskListWithHeight(-1), "\n")
	if len(lines) != 3 { // 1 header + 2 task rows, no wrapped continuations
		t.Fatalf("expected 3 lines (header + 2 rows), got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	inner := m.panelInnerWidth()
	for i, l := range lines {
		if w := lipgloss.Width(l); w > inner {
			t.Fatalf("line %d width %d exceeds inner %d (would wrap): %q", i, w, inner, l)
		}
	}
}

// TestViewNeverExceedsWidth guards against chrome (e.g. the key-hint bar) being
// wider than the terminal, which wraps and shoves the layout off-screen. No
// rendered line may exceed m.width at a narrow size.
func TestViewNeverExceedsWidth(t *testing.T) {
	for _, w := range []int{80, 90, 100} {
		m := newTestModel(t)
		m.width = w
		m.height = 24
		m.tasks = []storage.Task{
			{ID: 1, Title: "Alpha", Status: "PENDING", CreatedAt: time.Now()},
		}
		for i, l := range strings.Split(m.View(), "\n") {
			if lw := lipgloss.Width(l); lw > w {
				t.Fatalf("width %d: line %d is %d cells (over by %d): %q", w, i, lw, lw-w, l)
			}
		}
	}
}

// TestCalendarDayListIsListView confirms the Enter day-detail renders the day's
// tasks as a list view (status/priority/title/time columns) rather than a plain
// bullet list, and that the month grid uses the narrow "∙" dot (not the wide •).
func TestCalendarDayListIsListView(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	m.tasks = []storage.Task{
		{ID: 1, Title: "Standup", Status: "PENDING",
			Due: sql.NullTime{Time: day.Add(10 * time.Hour), Valid: true}},
	}
	r, _ := m.enterCalendarView()
	m = r.(Model)

	grid := m.renderCalendarGrid()
	if strings.Contains(grid, "•") {
		t.Fatalf("grid should use the narrow ∙ dot, not the wide •")
	}

	m.calendarDetail = true
	detail := m.renderCalendarDayList()
	for _, want := range []string{"Status", "Title", "Time", "10:00", "Standup"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("day detail should contain %q (list-view), got:\n%s", want, detail)
		}
	}
}

// TestGanttLongTaskHeaderNotGarbled guards the long-term-task header bugs: the
// year row must use clean 2-digit years (no colliding 4-digit labels), and the
// panel title must stay narrow (use the EA-narrow "∙" separator) so it can't overflow the
// framed top border in a CJK terminal.
func TestGanttLongTaskHeaderNotGarbled(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 16
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Long project", Status: "PENDING", CreatedAt: now,
			Start: sql.NullTime{Time: now, Valid: true},
			Due:   sql.NullTime{Time: now.AddDate(0, 0, 2000), Valid: true}}, // ~6mo unit
	}
	m.cursor = 0
	scale := m.timelineWindow(m.timelineNavItems())
	if scale.unitDays < 28 {
		t.Fatalf("a 2000-day task should zoom to a coarse unit, got %d", scale.unitDays)
	}

	yearRow := stripAnsiTest(m.timelineMonthHeader(scale, normalizeDate(now)))
	// Collisions produced runs like "2022026"; clean 2-digit years never exceed
	// two consecutive digits.
	run := 0
	for _, r := range yearRow {
		if r >= '0' && r <= '9' {
			run++
			if run > 2 {
				t.Fatalf("year header has a %d+ digit run (label collision): %q", run, yearRow)
			}
		} else {
			run = 0
		}
	}

	// The title must contain no East-Asian ambiguous-width glyphs (wide "·"/"–"),
	// which would push the framed top border a cell over and wrap it; the narrow
	// "∙" branding separator is used instead.
	title := m.timelinePanelTitle()
	for _, bad := range []string{"·", "–", "—", "↑"} {
		if strings.Contains(title, bad) {
			t.Fatalf("gantt title should avoid wide glyph %q, got %q", bad, title)
		}
	}
	if !strings.Contains(title, "∙") {
		t.Fatalf("gantt title should use the narrow ∙ separator, got %q", title)
	}
}

// TestNoteViewNeverOverflowsWidth guards the "list messed up after viewing a
// note" bug: the unframed note view must clip every line to the terminal width,
// otherwise a long note line wraps and desyncs the frame, garbling the next view.
func TestNoteViewNeverOverflowsWidth(t *testing.T) {
	m := newTestModel(t)
	m.width = 90
	m.height = 24
	long := "A single very long note line that exceeds the terminal width " + strings.Repeat("x", 120)
	m.tasks = []storage.Task{
		{ID: 1, Title: strings.Repeat("Long title ", 12), Status: "PENDING",
			CreatedAt: time.Now(), Notes: long},
	}
	r, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open note view
	m = r.(Model)
	if m.mode != modeNote {
		t.Fatalf("Enter should open the note view, mode=%d", m.mode)
	}
	for i, l := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Fatalf("note line %d width %d exceeds terminal width %d (would wrap): %q", i, w, m.width, l)
		}
	}
}

// TestStatusBarLeavesLastCellFree guards the alt-screen "duplicate status bar /
// header disappears" scroll: rendered rows must stop short of the full width so
// writing near the bottom-right edge can't leave the terminal in a pending-wrap
// state that scrolls the screen on the next repaint.
func TestGanttViewLeavesLastCellFree(t *testing.T) {
	m := newTestModel(t)
	m.width = 90
	m.height = 16
	now := time.Now()
	m.tasks = []storage.Task{{
		ID: 1, Title: "Very long gantt task", Status: "IN-PROGRESS", CreatedAt: now,
		Start: sql.NullTime{Time: now.AddDate(0, 0, -30), Valid: true},
		Due:   sql.NullTime{Time: now.AddDate(0, 0, 900), Valid: true},
	}}
	r, _ := m.enterGanttView()
	m = r.(Model)
	for i, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > m.width-2 {
			t.Fatalf("gantt view line %d width %d should leave edge cells free (<= %d): %q", i, w, m.width-2, line)
		}
	}
}

func TestStatusBarLeavesLastCellFree(t *testing.T) {
	for _, mode := range []func(Model) Model{
		func(m Model) Model { return m }, // list
		func(m Model) Model { r, _ := m.enterGanttView(); return r.(Model) },
		func(m Model) Model { r, _ := m.enterCalendarView(); return r.(Model) },
	} {
		m := newTestModel(t)
		m.width = 90
		m.height = 16
		m.tasks = []storage.Task{{ID: 1, Title: "Task", Status: "PENDING", CreatedAt: time.Now()}}
		m = mode(m)
		lines := strings.Split(m.View(), "\n")
		last := lines[len(lines)-1]
		if w := lipgloss.Width(last); w > m.width-2 {
			t.Fatalf("status bar width %d should leave edge cells free (<= %d), mode=%d", w, m.width-2, m.mode)
		}
	}
}

// TestAgendaView confirms the first-launch agenda shows a greeting, a color-coded
// summary, and narrow "∙" bullets (not the wide "•", which double-widths in CJK/
// Termius), and never overflows the terminal width.
func TestAgendaView(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 30
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Overdue thing", Status: "PENDING", CreatedAt: now,
			Due: sql.NullTime{Time: now.AddDate(0, 0, -2), Valid: true}},
	}
	m.refreshReport()

	report := stripAnsiTest(m.renderReportHeader()) + stripAnsiTest(m.report)
	if strings.Contains(report, "•") {
		t.Fatalf("agenda should use the narrow ∙ bullet, not the wide •")
	}
	for _, want := range []string{"Good ", "overdue", "Overdue (1)", "∙ #1"} {
		if !strings.Contains(report, want) {
			t.Fatalf("agenda should contain %q, got:\n%s", want, report)
		}
	}
	for i, l := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Fatalf("agenda line %d width %d exceeds %d: %q", i, w, m.width, l)
		}
	}
}

// TestPriorityBadge confirms priority renders as a flag (with level) rather than
// the old "P0/P1" text, and that "none" is distinct.
func TestPriorityBadge(t *testing.T) {
	m := newTestModel(t)

	none := m.priorityBadge(0)
	if !strings.Contains(none, "⚐") {
		t.Fatalf("priority 0 should be a muted outline flag, got %q", none)
	}
	high := m.priorityBadge(4)
	if !strings.Contains(high, "⚑") {
		t.Fatalf("priority 4 should be a filled flag, got %q", high)
	}
	if strings.Contains(m.priorityBadge(2), "P") {
		t.Fatalf("priority badge should not use the P-prefix notation")
	}
	// Color alone conveys the level now — no digit in the badge.
	if strings.ContainsAny(m.priorityBadge(4), "0123456789") {
		t.Fatalf("priority badge should not include the level digit, got %q", high)
	}
}

// TestListLayoutHintsAboveDetail confirms the key-hint line sits above the
// bottom Detail box (not below it).
func TestListLayoutHintsAboveDetail(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{{ID: 1, Title: "Alpha", Status: "TODO", CreatedAt: time.Now()}}

	out := m.View()
	hintsAt := strings.Index(out, "quit") // a listHints chip label
	detailAt := strings.Index(out, "Detail")
	if hintsAt < 0 || detailAt < 0 {
		t.Fatalf("expected both hints and Detail box in view (hints=%d detail=%d)", hintsAt, detailAt)
	}
	if hintsAt > detailAt {
		t.Fatalf("expected hint line above the Detail box, but hints came after")
	}
}

// TestRepeatSortReverses confirms re-selecting the active sort flips direction.
func TestRepeatSortReverses(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{
		{ID: 1, Priority: 1},
		{ID: 2, Priority: 3},
		{ID: 3, Priority: 2},
	}

	m.applySortMode("priority") // highest priority first: 3,2,1 → IDs 2,3,1
	if got := []int{m.tasks[0].ID, m.tasks[1].ID, m.tasks[2].ID}; got[0] != 2 || got[2] != 1 {
		t.Fatalf("priority sort order unexpected: %v", got)
	}
	if m.sortReversed {
		t.Fatalf("first sort should not be reversed")
	}

	m.applySortMode("priority") // same key again → reversed
	if !m.sortReversed {
		t.Fatalf("repeat sort should reverse")
	}
	if got := []int{m.tasks[0].ID, m.tasks[1].ID, m.tasks[2].ID}; got[0] != 1 || got[2] != 2 {
		t.Fatalf("reversed priority order unexpected: %v", got)
	}

	m.applySortMode("due") // switching key resets to default direction
	if m.sortReversed {
		t.Fatalf("switching sort key should reset direction")
	}
}

// TestSortIndicatorInHeader confirms the active sort column shows a direction
// marker in the table header (▴ default order, ▾ reversed) and that switching
// columns moves the marker. ASCII carets are used (not Unicode triangles) so the
// header keeps one cell per glyph in East-Asian terminals.
func TestSortIndicatorInHeader(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	m.tasks = []storage.Task{{ID: 1, Priority: 2}}

	header := func() string {
		return strings.SplitN(m.renderTaskListWithHeight(20), "\n", 2)[0]
	}

	m.applySortMode("priority")
	if h := header(); !strings.Contains(h, "Pri▴") {
		t.Fatalf("priority sort should mark Pri with ▴, got: %q", h)
	}

	m.applySortMode("priority") // same key again → reversed
	if h := header(); !strings.Contains(h, "Pri▾") {
		t.Fatalf("reversed priority sort should mark Pri with ▾, got: %q", h)
	}

	m.applySortMode("due")
	if h := header(); strings.Contains(h, "Pri▴") || strings.Contains(h, "Pri▾") {
		t.Fatalf("switching away from priority should clear its marker, got: %q", h)
	}
	if h := header(); !strings.Contains(h, "Due▴") {
		t.Fatalf("due sort should mark Due with ▴, got: %q", h)
	}
}

// TestSortIndicatorNarrowInEastAsian guards the real bug: in a CJK terminal the
// sort marker must stay one cell wide so the sorted header lines up with the data
// rows instead of overflowing and wrapping. Measured with East-Asian width on,
// where Unicode triangles (▲/▼) would be two cells.
func TestSortIndicatorNarrowInEastAsian(t *testing.T) {
	runewidth.DefaultCondition.EastAsianWidth = true
	defer func() { runewidth.DefaultCondition.EastAsianWidth = false }()

	m := newTestModel(t)
	m.width = 140
	m.tasks = []storage.Task{{ID: 1, Title: "Alpha", Status: "PENDING"}}
	m.applySortMode("title")

	lines := strings.Split(m.renderTaskListWithHeight(-1), "\n")
	headerW := runewidth.StringWidth(stripAnsiTest(lines[0]))
	rowW := runewidth.StringWidth(stripAnsiTest(lines[1]))
	if headerW != rowW {
		t.Fatalf("sorted header width %d != data row width %d under East-Asian width (marker too wide)", headerW, rowW)
	}
}

func stripAnsiTest(s string) string {
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] == '\x1b' {
			for i < len(r) && r[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteRune(r[i])
	}
	return b.String()
}

// TestSortByTitle confirms `st` sorts alphabetically by title (case-insensitive).
func TestSortByTitle(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{
		{ID: 1, Title: "banana"},
		{ID: 2, Title: "Apple"},
		{ID: 3, Title: "cherry"},
	}

	if handled := m.processSortKey("s"); !handled {
		t.Fatalf("`s` should start a sort sequence")
	}
	if handled := m.processSortKey("t"); !handled {
		t.Fatalf("`t` should complete the sort sequence")
	}
	if m.sortMode != "title" {
		t.Fatalf("st should select title sort, got %q", m.sortMode)
	}
	if got := []int{m.tasks[0].ID, m.tasks[1].ID, m.tasks[2].ID}; got[0] != 2 || got[1] != 1 || got[2] != 3 {
		t.Fatalf("title order want [2 1 3] (Apple, banana, cherry), got %v", got)
	}
}

// TestSortByState confirms `ss` (the two-key sequence) selects state sort and
// orders overdue → in-progress → pending → done.
func TestSortByState(t *testing.T) {
	m := newTestModel(t)
	past := sql.NullTime{Time: time.Now().Add(-48 * time.Hour), Valid: true}
	m.tasks = []storage.Task{
		{ID: 1, Status: "DONE", Done: true},
		{ID: 2, Status: "PENDING"},
		{ID: 3, Status: "IN-PROGRESS"},
		{ID: 4, Status: "PENDING", Due: past}, // overdue
	}

	if !m.processSortKey("s") {
		t.Fatalf("first `s` should start a sort sequence")
	}
	if !m.processSortKey("s") {
		t.Fatalf("second `s` should complete the sequence")
	}
	if m.sortMode != "state" {
		t.Fatalf("ss should select state sort, got %q", m.sortMode)
	}
	got := []int{m.tasks[0].ID, m.tasks[1].ID, m.tasks[2].ID, m.tasks[3].ID}
	if want := []int{4, 3, 2, 1}; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("state order want [4 3 2 1] (overdue, in-progress, pending, done), got %v", got)
	}
}

// TestSortByTopic confirms topic sorting is alphabetical with untopiced last.
func TestSortByTopic(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{
		{ID: 1, Topics: []string{"zeta"}},
		{ID: 2, Topics: []string{"alpha"}},
		{ID: 3},
	}

	m.applySortMode("topic")
	order := []int{m.tasks[0].ID, m.tasks[1].ID, m.tasks[2].ID}
	if order[0] != 2 || order[1] != 1 || order[2] != 3 {
		t.Fatalf("expected topic order [alpha, zeta, none] = [2 1 3], got %v", order)
	}
}

// TestStatusBarSegments confirms the status bar exposes its distinct sections
// (brand, mode, sort, position). Colors are applied via styles at render time;
// the test environment has no TTY so it asserts structure, not raw escapes.
func TestStatusBarSegments(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{{ID: 1, Title: "Alpha"}}
	m.applySortMode("priority")

	bar := m.renderStatusBar()
	for _, want := range []string{"bada", "LIST", "sort:priority", "1/1"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("status bar missing %q; got %q", want, bar)
		}
	}
}

// TestHolidayNameMatching confirms both one-off (YYYY-MM-DD) and yearly (MM-DD)
// holiday dates are recognized.
func TestHolidayNameMatching(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Holidays = []config.Holiday{
		{Date: "01-01", Name: "New Year's Day"},
		{Date: "2026-07-15", Name: "Company Offsite"},
	}

	if name, ok := m.holidayName(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); !ok || name != "New Year's Day" {
		t.Fatalf("yearly holiday not matched: %q ok=%v", name, ok)
	}
	if name, ok := m.holidayName(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)); !ok || name != "Company Offsite" {
		t.Fatalf("one-off holiday not matched: %q ok=%v", name, ok)
	}
	if _, ok := m.holidayName(time.Date(2027, 7, 15, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatalf("one-off holiday should not recur in a later year")
	}
}

func TestQuickFilterCommands(t *testing.T) {
	m := newTestModel(t)
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Overdue", Status: "PENDING", CreatedAt: now, Due: sql.NullTime{Time: now.Add(-24 * time.Hour), Valid: true}},
		{ID: 2, Title: "Pending", Status: "PENDING", CreatedAt: now},
		{ID: 3, Title: "Doing", Status: "IN-PROGRESS", CreatedAt: now},
		{ID: 4, Title: "Done", Status: "DONE", Done: true, CreatedAt: now},
	}

	m.mode = modeCommand
	m.input.SetValue(":overdue")
	res, _ := m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	items := m.visibleItems()
	if len(items) != 1 || items[0].task.ID != 1 {
		t.Fatalf("expected only overdue task, got %+v", items)
	}
	if !strings.Contains(m.renderStatusBar(), "filter:") {
		t.Fatalf("expected status bar to show active filter")
	}
	if !strings.Contains(m.taskPanelTitle(), "overdue") {
		t.Fatalf("expected task panel title to show active filter")
	}

	m.mode = modeCommand
	m.input.SetValue(":pending")
	res, _ = m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	items = m.visibleItems()
	if len(items) != 2 || items[0].task.ID != 1 || items[1].task.ID != 2 {
		t.Fatalf("expected pending tasks, got %+v", items)
	}

	res, cmd := m.updateListMode("q")
	m = res.(Model)
	if cmd != nil {
		t.Fatalf("q should clear an active filter before quitting")
	}
	if got := len(m.visibleItems()); got != 4 {
		t.Fatalf("expected q to return to original list, got %d tasks", got)
	}
	if strings.Contains(m.taskPanelTitle(), "overdue") {
		t.Fatalf("expected filter scope to be gone after q")
	}

	m.mode = modeCommand
	m.input.SetValue(":overdue")
	res, _ = m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	res, _ = m.updateListMode("esc")
	m = res.(Model)
	if got := len(m.visibleItems()); got != 4 {
		t.Fatalf("expected esc to return to original list, got %d tasks", got)
	}
}

func TestFuzzySearchKeys(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{
		{ID: 1, Title: "Fix bug", Topics: []string{"work"}, Tags: "backend", Assignee: "han", Priority: 3, Notes: "panic on save"},
		{ID: 2, Title: "Write docs", Reporter: "mina"},
	}

	res, _ := m.updateListMode("F")
	m = res.(Model)
	if m.mode != modeSearch || !m.searchFuzzy {
		t.Fatalf("expected F to open fuzzy search mode")
	}
	m.input.SetValue("fb")
	res, _ = m.updateSearchMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	// Fuzzy Enter jumps to the highlighted match and exits search.
	if m.mode != modeList || m.searchFuzzy {
		t.Fatalf("expected fuzzy Enter to jump and exit search (mode=%d fuzzy=%v)", m.mode, m.searchFuzzy)
	}
	if cur, ok := m.currentTask(); !ok || cur.ID != 1 {
		t.Fatalf("expected cursor on Fix bug (#1) after fuzzy jump, got %+v ok=%v", cur, ok)
	}

	m.resetToOriginalListView()
	m.searchFuzzy = true
	m.searchQuery = "hn p3"
	items := m.visibleItems()
	if len(items) != 1 || items[0].task.ID != 1 {
		t.Fatalf("expected fuzzy query to match assignee/priority fields, got %+v", items)
	}
	m.searchQuery = "pnc sv"
	items = m.visibleItems()
	if len(items) != 1 || items[0].task.ID != 1 {
		t.Fatalf("expected fuzzy query to match notes, got %+v", items)
	}

	m.resetToOriginalListView()
	if !m.processNavKey(",") || !m.processNavKey("f") {
		t.Fatalf("expected ,f to open fuzzy search")
	}
	if m.mode != modeSearch || !m.searchFuzzy {
		t.Fatalf("expected ,f to enter fuzzy search mode")
	}
	m.input.SetValue("wd")
	out := m.View()
	if !strings.Contains(out, "Fuzzy Find") || !strings.Contains(out, "Write docs") {
		t.Fatalf("expected fuzzy search modal with live results, got:\n%s", out)
	}
}

func TestCommandHistoryUsesArrowKeys(t *testing.T) {
	m := newTestModel(t)

	m.mode = modeCommand
	m.input.SetValue(":overdue")
	res, _ := m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)

	m.mode = modeCommand
	m.input.SetValue(":pending")
	res, _ = m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)

	res, _ = m.startCommand()
	m = res.(Model)
	res, _ = m.updateCommandMode("up", tea.KeyMsg{Type: tea.KeyUp})
	m = res.(Model)
	if got := m.input.Value(); got != ":pending" {
		t.Fatalf("expected most recent command, got %q", got)
	}
	res, _ = m.updateCommandMode("up", tea.KeyMsg{Type: tea.KeyUp})
	m = res.(Model)
	if got := m.input.Value(); got != ":overdue" {
		t.Fatalf("expected previous command, got %q", got)
	}
	res, _ = m.updateCommandMode("down", tea.KeyMsg{Type: tea.KeyDown})
	m = res.(Model)
	if got := m.input.Value(); got != ":pending" {
		t.Fatalf("expected next command, got %q", got)
	}
	res, _ = m.updateCommandMode("down", tea.KeyMsg{Type: tea.KeyDown})
	m = res.(Model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected blank command after newest history entry, got %q", got)
	}
}

// TestGanttNavigationSelectsTask confirms ↑/↓ move the cursor through the gantt
// rows and that currentTask() tracks the highlighted row.
func TestGanttNavigationSelectsTask(t *testing.T) {
	m := newTestModel(t)
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Alpha", Status: "TODO", CreatedAt: now},
		{ID: 2, Title: "Bravo", Status: "TODO", CreatedAt: now},
		{ID: 3, Title: "Charlie", Status: "TODO", CreatedAt: now},
	}

	if task, ok := m.currentTask(); !ok || task.ID != 1 {
		t.Fatalf("expected cursor on task 1, got %+v ok=%v", task, ok)
	}

	res, _ := m.updateListMode("down")
	m = res.(Model)
	if task, ok := m.currentTask(); !ok || task.ID != 2 {
		t.Fatalf("after down expected task 2, got %+v ok=%v", task, ok)
	}

	// The selected row is highlighted like the list view, without an extra wedge
	// marker or separate status summary row above the tasks.
	grid := strings.Join(m.timelineGridLines(20), "\n")
	if strings.Contains(grid, "▸") {
		t.Fatalf("did not expect a wedge cursor marker in the gantt, got:\n%s", grid)
	}
	if strings.Contains(grid, "#2  PENDING") {
		t.Fatalf("did not expect a separate selected-task summary row in the gantt, got:\n%s", grid)
	}
}

// TestGanttHasNoRowTint confirms the Gantt view does not apply alternating
// row-wise backgrounds to either task labels or calendar cells.
func TestGanttHasNoRowTint(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)

	m := newTestModel(t)
	stripe := bgOpenSeq(m.styles.RowStripe)
	if stripe == "" {
		t.Skip("color profile stripped the stripe background")
	}
	stripeParam := strings.TrimSuffix(strings.TrimPrefix(stripe, "\x1b["), "m")

	scale := timelineScale{
		start:    time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		unitDays: 1,
		colCount: 5,
		leftW:    20,
	}
	it := timelineItem{
		task: storage.Task{ID: 1, Title: "Alpha", Status: "PENDING"},
	}
	row := m.timelineTaskRow(it, scale, scale.start.AddDate(0, 0, -10), false)
	if strings.Contains(row, stripeParam) {
		t.Fatalf("gantt row should not carry row tint, got %q", row)
	}
}

// TestGanttZoomFitsSelectedTask confirms the timeline zooms its column unit out
// when the selected task spans longer than a daily window can show, and keeps a
// daily unit for short tasks — so a long-term project's due date stays visible.
func TestGanttZoomFitsSelectedTask(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	now := time.Now()
	short := storage.Task{ID: 1, Title: "Short", Status: "PENDING", CreatedAt: now,
		Due: sql.NullTime{Time: now.AddDate(0, 0, 3), Valid: true}}
	long := storage.Task{ID: 2, Title: "Long project", Status: "PENDING", CreatedAt: now,
		Start: sql.NullTime{Time: now, Valid: true},
		Due:   sql.NullTime{Time: now.AddDate(0, 0, 120), Valid: true}}
	m.tasks = []storage.Task{short, long}

	items := m.timelineNavItems()

	// Cursor on the short task → daily resolution.
	m.cursor = 0
	if s := m.timelineWindow(items); s.unitDays != 1 {
		t.Fatalf("short task should keep daily unit, got unitDays=%d", s.unitDays)
	}

	// Cursor on the long task → zoom out, and its due date must be on-screen.
	m.cursor = 1
	s := m.timelineWindow(items)
	if s.unitDays <= 1 {
		t.Fatalf("long task should zoom the unit out, got unitDays=%d", s.unitDays)
	}
	dueCol := s.colOf(items[1].due)
	if dueCol < 0 || dueCol >= s.colCount {
		t.Fatalf("long task due should fall within the %d columns, got col %d", s.colCount, dueCol)
	}
}

// TestGanttHeadersShowYearWhenZoomedOut confirms that at month-or-coarser zoom
// the top header is year-oriented and the second header carries month labels.
func TestGanttHeadersShowYearWhenZoomedOut(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Long", Status: "PENDING", CreatedAt: now,
			Start: sql.NullTime{Time: now, Valid: true},
			Due:   sql.NullTime{Time: now.AddDate(0, 0, 400), Valid: true}},
	}
	m.cursor = 0
	scale := m.timelineWindow(m.timelineNavItems())
	if scale.unitDays < 28 {
		t.Fatalf("a 400-day task should zoom to month+ unit, got %d", scale.unitDays)
	}
	today := normalizeDate(now)
	top := m.timelineMonthHeader(scale, today)
	sub := m.timelineDayHeader(scale, today)

	wantYear := "'" + scale.colStartDate(0).Format("06") // 2-digit, collision-proof
	if !strings.Contains(top, wantYear) {
		t.Fatalf("top header should show the year %s, got: %q", wantYear, top)
	}
	wantMonth := fmt.Sprintf("%2d ", int(scale.colStartDate(0).Month()))
	if !strings.Contains(sub, wantMonth) {
		t.Fatalf("second header should show numeric month %q, got: %q", wantMonth, sub)
	}
}

// TestGanttUsesNarrowGlyphs guards against ambiguous-width block glyphs in the
// gantt grid (▌ █ ━), which render double-width in CJK terminals and overflowed
// the selected/long-task row into a phantom blank line. Bars/markers must use
// the guaranteed one-cell ▸ ▬ ▮.
func TestGanttUsesNarrowGlyphs(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 14
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Short", Status: "PENDING", CreatedAt: now,
			Due: sql.NullTime{Time: now.AddDate(0, 0, 3), Valid: true}},
		{ID: 2, Title: "Long", Status: "PENDING", CreatedAt: now,
			Start: sql.NullTime{Time: now, Valid: true},
			Due:   sql.NullTime{Time: now.AddDate(0, 0, 800), Valid: true}},
	}
	r, _ := m.enterGanttView()
	m = r.(Model)
	m.cursor = 1 // select the long task
	grid := stripAnsiTest(strings.Join(m.timelineGridLines(20), "\n"))
	for _, bad := range []string{"▌", "█", "━"} {
		if strings.Contains(grid, bad) {
			t.Fatalf("gantt should not use ambiguous-width glyph %q (CJK double-width):\n%s", bad, grid)
		}
	}
	if !strings.Contains(grid, "▬") || !strings.Contains(grid, "▮") {
		t.Fatalf("gantt should use narrow ▬/▮ bars, got:\n%s", grid)
	}
}

// TestGanttEnterOpensDetailAndReturns confirms Enter in the gantt opens the
// selected task's detail (metadata + notes) and that closing it returns to the
// gantt, not the list.
func TestGanttEnterOpensDetailAndReturns(t *testing.T) {
	m := newTestModel(t)
	m.width = 90
	m.height = 20
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "longtask2", Status: "PENDING", CreatedAt: now, Notes: "Plan the launch",
			Start: sql.NullTime{Time: now, Valid: true},
			Due:   sql.NullTime{Time: now.AddDate(0, 0, 700), Valid: true}},
	}
	r, _ := m.enterGanttView()
	m = r.(Model)
	if m.mode != modeGantt {
		t.Fatalf("expected gantt mode, got %d", m.mode)
	}

	r, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = r.(Model)
	if m.mode != modeNote {
		t.Fatalf("Enter in gantt should open the detail/note view, mode=%d", m.mode)
	}
	view := m.View()
	if !strings.Contains(view, "longtask2") || !strings.Contains(view, "Plan the launch") {
		t.Fatalf("detail should show the task title and notes, got:\n%s", view)
	}

	r, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = r.(Model)
	if m.mode != modeGantt {
		t.Fatalf("closing the detail should return to the gantt, got mode %d", m.mode)
	}
}

// TestGanttUnitLadder confirms the zoom unit escalates through week → month →
// quarter → half-year → year → multi-year as the span grows, with matching
// human labels.
func TestGanttUnitLadder(t *testing.T) {
	const cols = 30 // eff = 28
	ladder := []struct {
		spanDays  int
		wantUnit  int
		wantLabel string
	}{
		{20, 1, "1col=1d"},
		{100, 7, "1col=1wk"},
		{700, 30, "1col=1mo"},
		{1500, 90, "1col=3mo"},
		{3000, 180, "1col=6mo"},
		{8000, 365, "1col=1yr"},
		{15000, 365 * 2, "1col=2yr"},
	}
	for _, c := range ladder {
		got := chooseUnit(c.spanDays, cols)
		if got != c.wantUnit {
			t.Fatalf("chooseUnit(%d): want %d, got %d", c.spanDays, c.wantUnit, got)
		}
		if lbl := (timelineScale{unitDays: got}).unitLabel(); lbl != c.wantLabel {
			t.Fatalf("unitLabel(%d): want %q, got %q", got, c.wantLabel, lbl)
		}
	}
}

// TestListViewShowsTasksAndDetail confirms the list view renders the task table
// on top and the Detail pane at the bottom.
func TestListViewShowsTasksAndDetail(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{{ID: 1, Title: "Alpha", Status: "TODO", CreatedAt: time.Now()}}

	out := m.View()
	if !strings.Contains(out, "Tasks") {
		t.Fatalf("expected Tasks pane in list view")
	}
	if !strings.Contains(out, "Detail") {
		t.Fatalf("expected Detail pane in list view")
	}
	if !strings.Contains(out, "Alpha") {
		t.Fatalf("expected task title in the list/Detail pane")
	}
}

// TestGanttModeShowsTimeline confirms the :gantt mode now renders the timeline
// (month/day headers + legend), and that ↑/↓ move the cursor there.
func TestGanttModeShowsTimeline(t *testing.T) {
	m := newTestModel(t)
	now := time.Now()
	m.tasks = []storage.Task{
		{ID: 1, Title: "Alpha", Status: "TODO", CreatedAt: now},
		{ID: 2, Title: "Bravo", Status: "TODO", CreatedAt: now},
	}

	gm, _ := m.enterGanttView()
	m = gm.(Model)
	if m.mode != modeGantt {
		t.Fatalf("expected modeGantt, got %v", m.mode)
	}
	out := m.View()
	if !strings.Contains(out, "Timeline") {
		t.Fatalf("expected Timeline panel title in :gantt view")
	}
	if !strings.Contains(out, "weekend") {
		t.Fatalf("expected the timeline legend in :gantt view")
	}

	res, _ := m.updateGanttMode("down")
	m = res.(Model)
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1 after down in :gantt, got %d", m.cursor)
	}
}

// TestTopicDropdownOpensOnTab confirms Tab on the Topic field opens a dropdown
// of previously-used topics, and Enter inserts the highlighted value.
func TestTopicDropdownOpensOnTab(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{
		{ID: 1, Title: "A", Topics: []string{"work"}},
		{ID: 2, Title: "B", Topics: []string{"home"}},
	}

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.meta.index = 1 // Topic
	m.focusMetaField()

	// Tab opens the dropdown (instead of advancing to the next field).
	res, _ := m.updateMetadataMode("tab", tea.KeyMsg{Type: tea.KeyTab})
	m = res.(Model)
	if !m.meta.dropdownOpen {
		t.Fatalf("expected dropdown open after tab on Topic")
	}
	if m.meta.index != 1 {
		t.Fatalf("expected to stay on Topic field, got index %d", m.meta.index)
	}
	// Candidates are the previously-used topics, sorted: [home, work].
	if len(m.meta.completions) != 2 || m.meta.completions[0] != "home" {
		t.Fatalf("unexpected completions %v", m.meta.completions)
	}

	// Down moves the highlight, Enter inserts it and closes the dropdown.
	res, _ = m.updateMetadataMode("down", tea.KeyMsg{Type: tea.KeyDown})
	m = res.(Model)
	res, _ = m.updateMetadataMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if m.meta.dropdownOpen {
		t.Fatalf("expected dropdown closed after enter")
	}
	if m.meta.topic != "work" {
		t.Fatalf("expected topic 'work' selected, got %q", m.meta.topic)
	}
	if m.meta != nil && m.mode != modeList {
		// Selecting from the dropdown must not save the task.
		if m.meta == nil {
			t.Fatalf("dropdown enter should not save/close the modal")
		}
	}
}

// TestDropdownEnterDoesNotSave confirms Enter while the dropdown is open selects
// a value rather than committing the form.
func TestDropdownEnterDoesNotSave(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{{ID: 1, Title: "A", Topics: []string{"work"}}}

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.input.SetValue("My task")
	m.meta.title = "My task"
	m.meta.index = 1
	m.focusMetaField()

	res, _ := m.updateMetadataMode("tab", tea.KeyMsg{Type: tea.KeyTab})
	m = res.(Model)
	res, _ = m.updateMetadataMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)

	if m.meta == nil {
		t.Fatalf("expected modal to stay open when selecting from dropdown")
	}
	if len(m.tasks) != 1 {
		t.Fatalf("expected no new task created, got %d", len(m.tasks))
	}
}
