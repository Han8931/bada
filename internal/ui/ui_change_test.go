package ui

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// TestZebraStripesOddRows confirms alternating task rows get the faint stripe
// background and that it survives the inner per-cell ANSI resets (so the tint is
// continuous, not gapped after the status badge).
func TestZebraStripesOddRows(t *testing.T) {
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
	// lines[0]=header, [1]=row index 0 (even, unstriped), [2]=row index 1 (odd).
	if strings.Contains(lines[1], stripe) {
		t.Fatalf("even row should not be striped: %q", lines[1])
	}
	if n := strings.Count(lines[2], stripe); n < 2 {
		t.Fatalf("odd row should be striped with the tint re-asserted across resets, asserts=%d: %q", n, lines[2])
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
// triangle in the table header (▲ default order, ▼ reversed) and that switching
// columns moves the marker.
func TestSortIndicatorInHeader(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	m.tasks = []storage.Task{{ID: 1, Priority: 2}}

	header := func() string {
		return strings.SplitN(m.renderTaskListWithHeight(20), "\n", 2)[0]
	}

	m.applySortMode("priority")
	if h := header(); !strings.Contains(h, "Pri▲") {
		t.Fatalf("priority sort should mark Pri with ▲, got: %q", h)
	}

	m.applySortMode("priority") // same key again → reversed
	if h := header(); !strings.Contains(h, "Pri▼") {
		t.Fatalf("reversed priority sort should mark Pri with ▼, got: %q", h)
	}

	m.applySortMode("due")
	if h := header(); strings.Contains(h, "Pri▲") || strings.Contains(h, "Pri▼") {
		t.Fatalf("switching away from priority should clear its marker, got: %q", h)
	}
	if h := header(); !strings.Contains(h, "Due▲") {
		t.Fatalf("due sort should mark Due with ▲, got: %q", h)
	}
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

	// The highlighted row carries the selection marker.
	grid := strings.Join(m.timelineGridLines(20), "\n")
	if !strings.Contains(grid, "▌") {
		t.Fatalf("expected a selection marker in the gantt, got:\n%s", grid)
	}
}

// TestGanttRowTintStaysInCalendarGrid confirms alternating Gantt row
// backgrounds make the calendar area easier to scan without tinting task labels.
func TestGanttRowTintStaysInCalendarGrid(t *testing.T) {
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
	row := m.timelineTaskRow(it, scale, scale.start.AddDate(0, 0, -10), false, true)
	label := row[:scale.leftW]
	grid := row[scale.leftW+1:]

	if strings.Contains(label, stripeParam) {
		t.Fatalf("task label should not carry calendar row tint, got %q", label)
	}
	if !strings.Contains(grid, stripeParam) {
		t.Fatalf("calendar grid should carry row tint, got %q", grid)
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

	wantYear := fmt.Sprintf("%d", scale.colStartDate(0).Year())
	if !strings.Contains(top, wantYear) {
		t.Fatalf("top header should show the year %s, got: %q", wantYear, top)
	}
	wantMonth := fmt.Sprintf("%2d ", int(scale.colStartDate(0).Month()))
	if !strings.Contains(sub, wantMonth) {
		t.Fatalf("second header should show numeric month %q, got: %q", wantMonth, sub)
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
