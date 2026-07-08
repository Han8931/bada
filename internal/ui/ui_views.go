package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"bada/internal/storage"
)

func (m Model) enterTrashView() (tea.Model, tea.Cmd) {
	entries, err := m.store.ListTrash()
	if err != nil {
		m.status = fmt.Sprintf("trash load failed: %v", err)
		return m, nil
	}
	m.trash = entries
	m.trashSelected = map[int]bool{}
	m.trashCursor = clampCursor(0, len(entries))
	m.trashScroll = 0
	m.mode = modeTrash
	m.status = fmt.Sprintf("Trash: %d item(s). space to select, u to restore, P to purge, esc to exit", len(entries))
	m.adjustTrashScroll()
	return m, nil
}

func (m Model) enterReportView() (tea.Model, tea.Cmd) {
	m.refreshReport()
	m.mode = modeReport
	m.reportScroll = 0
	m.status = "Reminder report"
	return m, nil
}

func (m Model) enterCalendarView() (tea.Model, tea.Cmd) {
	m.mode = modeCalendar
	now := time.Now()
	m.calendarMonth = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	m.calendarDay = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	m.calendarDetail = false
	m.status = "Calendar view"
	return m, nil
}

func (m Model) enterHelpView() (tea.Model, tea.Cmd) {
	m.mode = modeHelp
	m.helpScroll = 0
	m.status = "Help"
	return m, nil
}

func (m Model) enterGanttView() (tea.Model, tea.Cmd) {
	m.mode = modeGantt
	m.ganttOffsetDays = 0
	m.status = "Gantt view"
	return m, nil
}

func (m Model) enterStatsView() (tea.Model, tea.Cmd) {
	m.mode = modeStats
	m.statsScroll = 0
	m.status = "Stats view"
	return m, nil
}

func (m Model) updateTrashMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.trashConfirm {
		switch key {
		case "y", "Y":
			if err := m.store.PurgeTrash(m.trashPending); err != nil {
				m.status = fmt.Sprintf("purge failed: %v", err)
			} else {
				var err error
				m.trash, err = m.store.ListTrash()
				if err != nil {
					m.status = fmt.Sprintf("reload trash failed: %v", err)
				} else {
					m.status = fmt.Sprintf("Purged %d item(s)", len(m.trashPending))
				}
				m.trashSelected = map[int]bool{}
				m.trashCursor = clampCursor(m.trashCursor, len(m.trash))
			}
			m.trashConfirm = false
			m.trashPending = nil
			m.adjustTrashScroll()
			return m, nil
		case "n", "N", "esc":
			m.trashConfirm = false
			m.trashPending = nil
			m.status = "Purge cancelled"
			return m, nil
		default:
			return m, nil
		}
	}
	switch key {
	case m.cfg.Keys.Cancel, "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.trashSelected = map[int]bool{}
		m.status = "Exited trash"
		return m, nil
	case m.cfg.Keys.Up, "up":
		if len(m.trash) == 0 {
			return m, nil
		}
		if m.trashCursor > 0 {
			m.trashCursor--
		}
	case m.cfg.Keys.Down, "down":
		if len(m.trash) == 0 {
			return m, nil
		}
		m.trashCursor = clampCursor(m.trashCursor+1, len(m.trash))
	case " ":
		if len(m.trash) == 0 {
			return m, nil
		}
		m.toggleTrashSelection(m.trashCursor)
		m.trashCursor = clampCursor(m.trashCursor+1, len(m.trash))
	case "u":
		return m.restoreTrashSelection()
	case "P":
		return m.confirmPurgeTrash()
	}
	m.adjustTrashScroll()
	return m, nil
}

func (m Model) updateReportMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Agenda closed"
		return m, nil
	case ":":
		return m.startCommand()
	case m.cfg.Keys.Up, "up":
		if len(m.reportTaskIDs) > 0 && m.reportCursor > 0 {
			m.reportCursor--
			m.refreshReport()
			m.ensureReportCursorVisible()
		}
		return m, nil
	case m.cfg.Keys.Down, "down":
		if len(m.reportTaskIDs) > 0 {
			m.reportCursor = clampCursor(m.reportCursor+1, len(m.reportTaskIDs))
			m.refreshReport()
			m.ensureReportCursorVisible()
		}
		return m, nil
	case "enter", m.cfg.Keys.NoteView:
		return m.startReportNoteView()
	case m.cfg.Keys.Toggle:
		return m.toggleReportTaskStatus()
	case m.cfg.Keys.Edit:
		if t, ok := m.selectedReportTask(); ok {
			return m.startMetadataEdit(t)
		}
		m.status = "No agenda task selected"
		return m, nil
	case m.cfg.Keys.DueForward, "]":
		return m.shiftReportTaskDue(1)
	case m.cfg.Keys.DueBack, "[":
		return m.shiftReportTaskDue(-1)
	case "g":
		return m.jumpToReportTask()
	case "G":
		m.reportScroll = m.reportMaxScroll()
		m.status = "Bottom"
		return m, nil
	case "z":
		m.agendaHeaderFold = !m.agendaHeaderFold
		m.reportScroll = 0
		if m.agendaHeaderFold {
			m.status = "Header folded"
		} else {
			m.status = "Header expanded"
		}
		m.ensureReportCursorVisible()
		return m, nil
	}
	if m.processScrollKey(key, m.reportMaxScroll(), &m.reportScroll) {
		return m, nil
	}
	return m, nil
}

func (m Model) selectedReportTask() (storage.Task, bool) {
	if len(m.reportTaskIDs) == 0 || m.reportCursor < 0 || m.reportCursor >= len(m.reportTaskIDs) {
		return storage.Task{}, false
	}
	id := m.reportTaskIDs[m.reportCursor]
	for _, t := range m.tasks {
		if t.ID == id {
			return t, true
		}
	}
	return storage.Task{}, false
}

func (m *Model) ensureReportCursorVisible() {
	if len(m.reportTaskIDs) == 0 || m.height <= 0 || m.reportCursor < 0 || m.reportCursor >= len(m.reportTaskIDs) {
		return
	}
	id := m.reportTaskIDs[m.reportCursor]
	needle := fmt.Sprintf("#%-3d", id)
	lines := strings.Split(strings.TrimRight(m.report, "\n"), "\n")
	lineIdx := -1
	for i, line := range lines {
		if strings.Contains(line, needle) {
			lineIdx = i
			break
		}
	}
	if lineIdx < 0 {
		return
	}
	bodyMax := m.reportBodyMaxLines()
	if bodyMax <= 0 {
		return
	}
	if lineIdx < m.reportScroll {
		m.reportScroll = lineIdx
	} else if lineIdx >= m.reportScroll+bodyMax {
		m.reportScroll = lineIdx - bodyMax + 1
	}
	m.reportScroll = clampInt(m.reportScroll, 0, m.reportMaxScroll())
}

func (m Model) reportBodyMaxLines() int {
	if m.height <= 0 {
		return 0
	}
	headerLines := strings.Split(strings.TrimRight(m.renderReportHeader(), "\n"), "\n")
	footerLines := strings.Split(m.renderReportFooter(), "\n")
	bodyMax := (m.height - 1) - len(headerLines) - len(footerLines) - 1
	if bodyMax < 0 {
		return 0
	}
	return bodyMax
}

func (m Model) startReportNoteView() (tea.Model, tea.Cmd) {
	t, ok := m.selectedReportTask()
	if !ok {
		m.status = "No agenda task selected"
		return m, nil
	}
	m.note = &noteState{target: noteTarget{kind: noteTask, taskID: t.ID, title: t.Title}, body: t.Notes}
	m.noteScroll = 0
	m.noteReturnMode = modeReport
	m.mode = modeNote
	m.status = fmt.Sprintf("Notes: %s", t.Title)
	return m, nil
}

func (m Model) toggleReportTaskStatus() (tea.Model, tea.Cmd) {
	t, ok := m.selectedReportTask()
	if !ok {
		m.status = "No agenda task selected"
		return m, nil
	}
	next, err := m.advanceTaskStatus(t)
	if err != nil {
		m.status = fmt.Sprintf("status failed: %v", err)
		return m, nil
	}
	m.refreshReport()
	m.ensureReportCursorVisible()
	m.status = "Status: " + next
	return m, nil
}

func (m Model) shiftReportTaskDue(days int) (tea.Model, tea.Cmd) {
	t, ok := m.selectedReportTask()
	if !ok {
		m.status = "No agenda task selected"
		return m, nil
	}
	if _, err := m.store.ShiftDue(t.ID, days); err != nil {
		m.status = fmt.Sprintf("shift due failed: %v", err)
		return m, nil
	}
	m.snapshotUndo(t, "due change")
	var err error
	m.tasks, err = m.store.FetchTasks()
	if err != nil {
		m.status = fmt.Sprintf("reload failed: %v", err)
		return m, nil
	}
	m.sortTasks()
	m.refreshReport()
	m.ensureReportCursorVisible()
	m.status = fmt.Sprintf("Due shifted by %+dd", days)
	return m, nil
}

func (m Model) jumpToReportTask() (tea.Model, tea.Cmd) {
	t, ok := m.selectedReportTask()
	if !ok {
		m.status = "No agenda task selected"
		return m, nil
	}
	m.filterDone = "all"
	m.currentTopic = ""
	m.searchQuery = ""
	m.searchFuzzy = false
	m.mode = modeList
	m.cursor = clampCursor(m.findVisibleTaskIndex(t.ID), len(m.visibleItems()))
	m.status = fmt.Sprintf("Jumped to task #%d", t.ID)
	return m, nil
}

func (m Model) updateCalendarMode(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		m.calendarDetail = !m.calendarDetail
		return m, nil
	case "esc", m.cfg.Keys.Quit, "q":
		if m.calendarDetail {
			m.calendarDetail = false
			m.status = "Calendar view"
			return m, nil
		}
		m.mode = modeList
		m.status = "Calendar closed"
		return m, nil
	case "h", "left":
		m.calendarDay = m.calendarDay.AddDate(0, 0, -1)
		m.calendarMonth = time.Date(m.calendarDay.Year(), m.calendarDay.Month(), 1, 0, 0, 0, 0, m.calendarDay.Location())
		return m, nil
	case "l", "right":
		m.calendarDay = m.calendarDay.AddDate(0, 0, 1)
		m.calendarMonth = time.Date(m.calendarDay.Year(), m.calendarDay.Month(), 1, 0, 0, 0, 0, m.calendarDay.Location())
		return m, nil
	case "k", "up":
		m.calendarDay = m.calendarDay.AddDate(0, 0, -7)
		m.calendarMonth = time.Date(m.calendarDay.Year(), m.calendarDay.Month(), 1, 0, 0, 0, 0, m.calendarDay.Location())
		return m, nil
	case "j", "down":
		m.calendarDay = m.calendarDay.AddDate(0, 0, 7)
		m.calendarMonth = time.Date(m.calendarDay.Year(), m.calendarDay.Month(), 1, 0, 0, 0, 0, m.calendarDay.Location())
		return m, nil
	case "H":
		m.calendarMonth = m.calendarMonth.AddDate(0, -1, 0)
		m.calendarDay = time.Date(m.calendarMonth.Year(), m.calendarMonth.Month(), 1, 0, 0, 0, 0, m.calendarMonth.Location())
		return m, nil
	case "L":
		m.calendarMonth = m.calendarMonth.AddDate(0, 1, 0)
		m.calendarDay = time.Date(m.calendarMonth.Year(), m.calendarMonth.Month(), 1, 0, 0, 0, 0, m.calendarMonth.Location())
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) updateHelpMode(key string) (tea.Model, tea.Cmd) {
	if m.processScrollKey(key, m.helpMaxScroll(), &m.helpScroll) {
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Help closed"
		return m, nil
	case m.cfg.Keys.Up, "up":
		if m.helpScroll > 0 {
			m.helpScroll--
		}
	case m.cfg.Keys.Down, "down":
		m.helpScroll = clampInt(m.helpScroll+1, 0, m.helpMaxScroll())
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) updateGanttMode(key string) (tea.Model, tea.Cmd) {
	items := m.timelineNavItems()
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Gantt closed"
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		if _, ok := m.currentTask(); !ok {
			m.status = "No task selected"
			return m, nil
		}
		return m.startNoteView() // detail + notes; closes back to the gantt
	case m.cfg.Keys.Edit:
		if t, ok := m.currentTask(); ok {
			return m.startMetadataEdit(t)
		}
		m.status = "No task selected"
		return m, nil
	case m.cfg.Keys.Toggle:
		if _, ok := m.currentTask(); !ok {
			m.status = "No task selected"
			return m, nil
		}
		return m.toggleCurrentTaskStatus()
	case m.cfg.Keys.DueForward, "]":
		if _, ok := m.currentTask(); !ok {
			m.status = "No task selected"
			return m, nil
		}
		return m.shiftDue(1)
	case m.cfg.Keys.DueBack, "[":
		if _, ok := m.currentTask(); !ok {
			m.status = "No task selected"
			return m, nil
		}
		return m.shiftDue(-1)
	case "h", "left":
		m.ganttOffsetDays--
		m.status = fmt.Sprintf("Timeline panned %+dd", m.ganttOffsetDays)
	case "l", "right":
		m.ganttOffsetDays++
		m.status = fmt.Sprintf("Timeline panned %+dd", m.ganttOffsetDays)
	case "H":
		m.ganttOffsetDays -= 7
		m.status = fmt.Sprintf("Timeline panned %+dd", m.ganttOffsetDays)
	case "L":
		m.ganttOffsetDays += 7
		m.status = fmt.Sprintf("Timeline panned %+dd", m.ganttOffsetDays)
	case "0", "home":
		m.ganttOffsetDays = 0
		m.status = "Timeline centered on today"
	case m.cfg.Keys.Up, "up":
		if m.cursor > 0 {
			m.cursor = clampCursor(m.cursor-1, len(items))
		}
	case m.cfg.Keys.Down, "down":
		if len(items) > 0 {
			m.cursor = clampCursor(m.cursor+1, len(items))
		}
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) updateStatsMode(key string) (tea.Model, tea.Cmd) {
	if m.processScrollKey(key, m.statsMaxScroll(), &m.statsScroll) {
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Stats closed"
		return m, nil
	case m.cfg.Keys.Up, "up":
		if m.statsScroll > 0 {
			m.statsScroll--
		}
	case m.cfg.Keys.Down, "down":
		m.statsScroll = clampInt(m.statsScroll+1, 0, m.statsMaxScroll())
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) updateNoteMode(key string) (tea.Model, tea.Cmd) {
	if m.noteConfirm {
		switch key {
		case "y", "Y":
			if err := m.clearNote(m.notePending); err != nil {
				m.status = fmt.Sprintf("note delete failed: %v", err)
				m.noteConfirm = false
				return m, nil
			}
			if m.notePending.kind == noteTask {
				m.applyTaskNoteLocal(m.notePending.taskID, "")
			}
			if m.note != nil && m.note.target.matches(m.notePending) {
				m.note.body = ""
				m.noteScroll = 0
			}
			m.noteConfirm = false
			m.status = "Note deleted"
			return m, nil
		case "n", "N", "esc":
			m.noteConfirm = false
			m.status = "Delete cancelled"
			return m, nil
		default:
			return m, nil
		}
	}
	switch key {
	case m.cfg.Keys.Cancel, m.cfg.Keys.Confirm, "esc", m.cfg.Keys.Quit, "q", "enter":
		m.mode = m.noteReturnMode // back to the view that opened it (list or gantt)
		m.note = nil
		m.status = "Notes closed"
		return m, nil
	case m.cfg.Keys.Edit:
		// e edits the task's fields (the metadata box); topic notes have no
		// fields, so e falls back to editing the note text.
		if m.note != nil && m.note.target.kind == noteTask {
			if idx := m.findTaskIndex(m.note.target.taskID); idx >= 0 {
				return m.startMetadataEdit(m.tasks[idx])
			}
		}
		return m.startNoteEditFromState()
	case "n", m.cfg.Keys.Detail:
		// n / v open the note text editor.
		return m.startNoteEditFromState()
	case "d":
		if m.note == nil {
			return m, nil
		}
		m.noteConfirm = true
		m.notePending = m.note.target
		m.status = "Delete note? y/n"
		return m, nil
	case "j", "down":
		max := m.noteMaxScroll()
		if m.noteScroll < max {
			m.noteScroll++
		}
		return m, nil
	case "k", "up":
		if m.noteScroll > 0 {
			m.noteScroll--
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) toggleTrashSelection(idx int) {
	if m.trashSelected == nil {
		m.trashSelected = map[int]bool{}
	}
	if m.trashSelected[idx] {
		delete(m.trashSelected, idx)
	} else {
		m.trashSelected[idx] = true
	}
}

func (m Model) restoreTrashSelection() (tea.Model, tea.Cmd) {
	if len(m.trash) == 0 {
		m.status = "Trash is empty"
		return m, nil
	}
	if m.trashConfirm {
		return m, nil
	}
	entries := m.selectedTrashEntries()
	if len(entries) == 0 && m.trashCursor < len(m.trash) {
		entries = append(entries, m.trash[m.trashCursor])
	}
	if len(entries) == 0 {
		m.status = "Nothing selected"
		return m, nil
	}
	if err := m.store.RestoreTrash(entries); err != nil {
		m.status = fmt.Sprintf("restore failed: %v", err)
		return m, nil
	}
	var err error
	m.trash, err = m.store.ListTrash()
	if err != nil {
		m.status = fmt.Sprintf("reload trash failed: %v", err)
		return m, nil
	}
	m.trashSelected = map[int]bool{}
	m.trashCursor = clampCursor(m.trashCursor, len(m.trash))
	m.adjustTrashScroll()
	m.tasks, err = m.store.FetchTasks()
	if err == nil {
		m.sortTasks()
		m.status = fmt.Sprintf("Restored %d task(s)", len(entries))
	} else {
		m.status = fmt.Sprintf("restore succeeded, reload failed: %v", err)
	}
	return m, nil
}

func (m Model) selectedTrashEntries() []storage.TrashEntry {
	if len(m.trashSelected) == 0 {
		return nil
	}
	var idxs []int
	for idx := range m.trashSelected {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	entries := make([]storage.TrashEntry, 0, len(idxs))
	for _, idx := range idxs {
		if idx >= 0 && idx < len(m.trash) {
			entries = append(entries, m.trash[idx])
		}
	}
	return entries
}

func (m Model) selectedTrashCount() int {
	return len(m.trashSelected)
}

func (m Model) confirmPurgeTrash() (tea.Model, tea.Cmd) {
	if len(m.trash) == 0 {
		m.status = "Trash is empty"
		return m, nil
	}
	entries := m.selectedTrashEntries()
	if len(entries) == 0 {
		entries = m.trash
	}
	if len(entries) == 0 {
		m.status = "Nothing to purge"
		return m, nil
	}
	m.trashConfirm = true
	m.trashPending = entries
	m.status = fmt.Sprintf("Purge %d trash item(s)? y/n", len(entries))
	return m, nil
}

func (m *Model) toggleTaskSelection(taskID int) {
	if m.selectedTasks == nil {
		m.selectedTasks = map[int]bool{}
	}
	if m.selectedTasks[taskID] {
		delete(m.selectedTasks, taskID)
		return
	}
	m.selectedTasks[taskID] = true
}

func (m Model) isTaskSelected(taskID int) bool {
	if m.selectedTasks == nil {
		return false
	}
	return m.selectedTasks[taskID]
}

func (m Model) selectedTaskList() []storage.Task {
	if len(m.selectedTasks) == 0 {
		return nil
	}
	selected := make([]storage.Task, 0, len(m.selectedTasks))
	for _, t := range m.tasks {
		if m.selectedTasks[t.ID] {
			selected = append(selected, t)
		}
	}
	return selected
}

func (m Model) calendarFooter() string {
	if m.calendarDetail {
		return m.hintBar([]keyHint{
			{"enter", "back"},
			{"h/l", "day"},
			{"j/k", "week"},
			{m.cfg.Keys.Cancel, "close"},
		})
	}
	return m.hintBar([]keyHint{
		{"h/l", "day"},
		{"j/k", "week"},
		{"H/L", "month"},
		{"enter", "day"},
		{m.cfg.Keys.Cancel, "close"},
	})
}

func (m Model) renderCalendarView() string {
	var title, body string
	if m.calendarDetail {
		title = "bada ∙ Calendar ∙ " + m.calendarDay.Format("Mon, Jan 2, 2006")
		body = m.renderCalendarDayList()
	} else {
		title = "bada ∙ Calendar ∙ " + m.calendarMonth.Format("January 2006")
		body = m.renderCalendarGrid()
	}
	return m.panel(title, body) + "\n" + m.calendarFooter()
}

func (m Model) renderCalendarGrid() string {
	weeks := calendarWeeks(m.calendarMonth)
	weeks = filterMonthWeeks(weeks, m.calendarMonth)
	if len(weeks) > 5 {
		selectedWeek := weekIndexForDay(weeks, m.calendarDay)
		start := clampInt(selectedWeek-2, 0, len(weeks)-5)
		weeks = weeks[start : start+5]
	}
	monthStart := time.Date(m.calendarMonth.Year(), m.calendarMonth.Month(), 1, 0, 0, 0, 0, m.calendarMonth.Location())
	nextMonthStart := monthStart.AddDate(0, 1, 0)
	cellWidth := 12
	if m.width > 0 {
		cellWidth = clampInt(m.panelInnerWidth()/7, 10, 20)
	}
	cellHeight := 4
	maxTasks := 2
	dayNames := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	var b strings.Builder
	for i, name := range dayNames {
		style := m.styles.Heading
		if i >= 5 { // Sat/Sun
			style = m.styles.Danger
		}
		b.WriteString(style.Render(padRightWidth(name, cellWidth)))
	}
	b.WriteString("\n")
	for weekIndex, week := range weeks {
		weekCells := make([][]string, 0, len(week))
		for _, day := range week {
			showOverflow := weekIndex == len(weeks)-1
			weekCells = append(weekCells, m.renderCalendarCellLines(day, cellWidth, cellHeight, maxTasks, monthStart, nextMonthStart, showOverflow))
		}
		for line := 0; line < cellHeight; line++ {
			for _, cell := range weekCells {
				b.WriteString(cell[line])
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderCalendarCellLines(day time.Time, width, height, maxTasks int, monthStart, nextMonthStart time.Time, showOverflow bool) []string {
	lines := make([]string, 0, height)
	inMonth := day.Month() == m.calendarMonth.Month()
	if !inMonth {
		if !showOverflow || day.Before(nextMonthStart) {
			for i := 0; i < height; i++ {
				lines = append(lines, m.styles.Muted.Render(padRightWidth("", width)))
			}
			return lines
		}
	}
	tasks := m.tasksForDay(day)
	headerStyle := m.styles.Muted
	taskStyle := m.styles.Muted
	if inMonth {
		headerStyle = m.styles.Border
		taskStyle = lipgloss.NewStyle()
	}
	isToday := isSameDate(day, time.Now())
	isSelected := isSameDate(day, m.calendarDay)
	// Weekends and configured holidays tint the date number (unless the cell is
	// already highlighted as today/selected).
	if inMonth && !isToday && !isSelected {
		_, holiday := m.holidayName(day)
		if holiday || day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			headerStyle = m.styles.Danger
		}
	}
	if isToday {
		headerStyle = m.styles.Warning
		taskStyle = m.styles.Warning
	}
	if isSelected {
		headerStyle = m.styles.Selection
		taskStyle = m.styles.Selection
	}

	dateLabel := fmt.Sprintf("%2d", day.Day())
	if len(tasks) > 0 {
		dateLabel = fmt.Sprintf("%2d (%d)", day.Day(), len(tasks))
	}
	dateLabel = padRightWidth(truncateTextWidth(dateLabel, width), width)
	lines = append(lines, headerStyle.Render(dateLabel))

	showCount := len(tasks)
	if showCount > maxTasks {
		showCount = maxTasks
	}
	highlight := isToday || isSelected
	for i := 0; i < showCount; i++ {
		// "∙" (narrow, one cell even in CJK terminals) instead of "•". On plain
		// in-month cells the dot is colored by task state; highlighted cells
		// recolor the whole line, so a plain dot is used there.
		body := padRightWidth("∙ "+truncateTextWidth(tasks[i].Title, width-2), width)
		if inMonth && !highlight {
			dot := m.calendarTaskAccent(tasks[i]).Render("∙")
			lines = append(lines, dot+taskStyle.Render(strings.TrimPrefix(body, "∙")))
		} else {
			lines = append(lines, taskStyle.Render(body))
		}
	}

	if len(tasks) > maxTasks {
		overflow := fmt.Sprintf("+%d more", len(tasks)-maxTasks)
		overflow = padRightWidth(truncateTextWidth(overflow, width), width)
		overflowStyle := taskStyle
		if inMonth && !isSameDate(day, time.Now()) && !isSameDate(day, m.calendarDay) {
			overflowStyle = m.styles.Muted
		}
		lines = append(lines, overflowStyle.Render(overflow))
	}

	for len(lines) < height {
		lines = append(lines, taskStyle.Render(padRightWidth("", width)))
	}
	return lines
}

// calendarTaskAccent colors a day-cell task dot by state: overdue red,
// in-progress green, otherwise the accent color.
func (m Model) calendarTaskAccent(t storage.Task) lipgloss.Style {
	switch {
	case isOverdue(t):
		return m.styles.Danger
	case t.Status == "IN-PROGRESS":
		return m.styles.Success
	default:
		return m.styles.Accent
	}
}

// renderCalendarDayList shows the selected day's tasks as a compact list view —
// a status badge, priority flag, title, due time, and tags per row — mirroring
// the main task table so the day detail feels like the list.
func (m Model) renderCalendarDayList() string {
	tasks := m.tasksForDay(m.calendarDay)
	if len(tasks) == 0 {
		return m.styles.Muted.Render("  (no tasks for this day)")
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		di, dj := tasks[i].Due, tasks[j].Due
		if di.Valid && dj.Valid && !di.Time.Equal(dj.Time) {
			return di.Time.Before(dj.Time)
		}
		if di.Valid != dj.Valid {
			return di.Valid
		}
		return tasks[i].ID < tasks[j].ID
	})

	inner := m.panelInnerWidth()
	const statusW, timeW, tagsW = 11, 6, 14
	titleW := clampInt(inner-statusW-timeW-tagsW-12, 12, 60)

	var b strings.Builder
	header := fmt.Sprintf("  %-*s %-4s %-*s %-*s %-*s",
		statusW, "Status", "Pri", titleW, "Title", timeW, "Time", tagsW, "Tags")
	b.WriteString(m.styles.TableHeader.Render(padRightWidth(header, inner)))
	b.WriteString("\n")
	for _, t := range tasks {
		tm := "—"
		if t.Due.Valid {
			tm = t.Due.Time.Format("15:04")
		}
		row := fmt.Sprintf("  %s %s %-*s %-*s %-*s",
			m.statusField(t, statusW, true),
			m.priorityField(t.Priority, true),
			titleW, truncateTextWidth(t.Title, titleW),
			timeW, tm,
			tagsW, truncateTextWidth(emptyDash(t.Tags), tagsW))
		b.WriteString(row)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderCalendarInfo() string {
	day := m.calendarDay
	tasks := m.tasksForDay(day)
	label := fmt.Sprintf("%s • tasks: %d", day.Format("Mon, Jan 2, 2006"), len(tasks))
	if len(tasks) == 0 {
		return m.styles.Muted.Render(label)
	}
	var b strings.Builder
	b.WriteString(m.styles.Border.Render(label))
	b.WriteString("\n")
	for _, t := range tasks {
		b.WriteString("  • ")
		b.WriteString(truncateText(t.Title, 40))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) tasksForDay(day time.Time) []storage.Task {
	dayKey := dateKey(day, m.calendarDay.Location())
	var list []storage.Task
	for _, t := range m.tasks {
		if isDone(t) {
			continue
		}
		if t.Due.Valid && dateKey(t.Due.Time, m.calendarDay.Location()) == dayKey {
			list = append(list, t)
			continue
		}
		if next, ok := nextRecurrenceDate(t); ok && dateKey(next, m.calendarDay.Location()) == dayKey {
			list = append(list, t)
		}
	}
	return list
}

func calendarWeeks(month time.Time) [][]time.Time {
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	start := startOfWeek(month, time.Monday)
	weeks := make([][]time.Time, 0, 6)
	cursor := start
	for i := 0; i < 6; i++ {
		week := make([]time.Time, 0, 7)
		for j := 0; j < 7; j++ {
			week = append(week, cursor)
			cursor = cursor.AddDate(0, 0, 1)
		}
		weeks = append(weeks, week)
	}
	return weeks
}

func filterMonthWeeks(weeks [][]time.Time, month time.Time) [][]time.Time {
	filtered := make([][]time.Time, 0, len(weeks))
	for _, week := range weeks {
		if weekHasMonthDay(week, month) {
			filtered = append(filtered, week)
		}
	}
	return filtered
}

func weekHasMonthDay(week []time.Time, month time.Time) bool {
	for _, day := range week {
		if day.Month() == month.Month() && day.Year() == month.Year() {
			return true
		}
	}
	return false
}

func weekIndexForDay(weeks [][]time.Time, day time.Time) int {
	for i, week := range weeks {
		for _, d := range week {
			if isSameDate(d, day) {
				return i
			}
		}
	}
	if len(weeks) == 0 {
		return 0
	}
	return 0
}

func padRightWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	textWidth := lipgloss.Width(text)
	if textWidth >= width {
		return text
	}
	return text + strings.Repeat(" ", width-textWidth)
}

func truncateTextWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	return runewidth.Truncate(text, width, "…")
}

func isSameDate(a, b time.Time) bool {
	return dateKey(a, b.Location()) == dateKey(b, b.Location())
}

func dateKey(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	tt := t.In(loc)
	return fmt.Sprintf("%04d-%02d-%02d", tt.Year(), tt.Month(), tt.Day())
}

func (m Model) helpFooter() string {
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "scroll"},
		{m.cfg.Keys.Cancel, "close"},
		{m.cfg.Keys.Quit, "quit"},
	})
}

func (m Model) renderHelpView() string {
	footer := m.helpFooter()
	bodyMax := 0
	if m.height > 0 {
		bodyMax = m.height - 1 - 2 - countLines(footer) // 2 = panel borders
		if bodyMax < 1 {
			bodyMax = 1
		}
	}
	var body string
	if m.height > 0 {
		body = m.renderHelpBody(bodyMax)
	} else {
		body = m.helpContent()
	}
	return m.panel("bada ∙ Help", body) + "\n" + footer
}

func (m Model) helpContent() string {
	return strings.TrimRight(fmt.Sprintf(`Commands:
  :agenda    Open agenda (z folds header; scope a topic first to filter it)
  :calendar  Open calendar view
  :gantt     Open gantt timeline
  :stats     Open productivity stats
  :projects  Projects overview (progress, workflows, metadata)
  :kanban[t] Stage board for a project's workflow
  :stage <n> Filter list to a workflow stage
  :config    Open the config file in $EDITOR (reloads on save)
  :theme     List palettes; :theme <name> switches presets
  :help / ?  Open this help screen
  :q / :quit Quit bada

Projects (:projects):
  enter  Scope to project   w  Edit status workflow
  e desc · t target · a archive   The 1st topic on a task is its project.
  Workflow editor: a add · e rename · c category · J/K reorder · D delete
  Kanban (:kanban): h/l column · j/k task · enter detail · L/H advance/send back · esc close

List Navigation:
  %s/%s  Move cursor
  %s     Search
  %s     Quit
  gg/G   Jump to top/bottom

Tasks:
  %s     Add task (Create Task dialog)
  %s     Rotate status
  %s     Delete (move to trash)
  %s     Edit metadata
  %s     Notes
  space  Select task (multi-select)
  %s     Delete selected
  %s     Delete all done (with confirm)
  u      Undo last edit (status/priority/due/metadata)
  %s      Toggle light/dark theme (saved to config)

Sorting (press s, then):
  d due · p priority · t created · o topic · w stage · a auto · s state
  Repeat the same sort to reverse the order (↑/↓ in the status bar)

Create/Edit Task:
  up/down or tab/shift+tab  Move fields
  enter / ^s               Save · esc  Cancel
  ▸ More details           Tags, Assignee, Reporter, Start, End, Recurrence, Notes
  Due                      type digits or +/- to set the
                           selected part, ←/→ pick Y/M/D/H/min,
                           x clears it (no due date)

Recurrence:
  Recurrence field supports:
    every day
    every 3 days
    every 2 weeks
    every 2 weeks on Mon
    every month
    every month on Fri
    daily | weekly | monthly (aliases)
  Weekdays: Mon/Tue/Wed/Thu/Fri/Sat/Sun (short or long)
  Interval alone means "every N days"

Calendar:
  h/l day • j/k week • H/L month
  enter day detail • esc/q close

`, m.cfg.Keys.Up, m.cfg.Keys.Down, m.cfg.Keys.Search, m.cfg.Keys.Quit, m.cfg.Keys.Add, m.cfg.Keys.Toggle, m.cfg.Keys.Delete, m.cfg.Keys.Edit, m.cfg.Keys.NoteView, m.cfg.Keys.Delete, m.cfg.Keys.DeleteAllDone, m.cfg.Keys.ThemeToggle), "\n")
}

func (m Model) helpMaxScroll() int {
	if m.height <= 0 {
		return 0
	}
	bodyMax := m.height - 1 - 2 - countLines(m.helpFooter()) // 2 = panel borders
	if bodyMax <= 0 {
		return 0
	}
	lines := strings.Split(strings.TrimRight(m.helpContent(), "\n"), "\n")
	if len(lines) <= bodyMax {
		return 0
	}
	return len(lines) - bodyMax
}

func (m Model) renderHelpBody(maxLines int) string {
	lines := strings.Split(strings.TrimRight(m.helpContent(), "\n"), "\n")
	if maxLines <= 0 || len(lines) == 0 {
		return ""
	}
	maxScroll := len(lines) - maxLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := clampInt(m.helpScroll, 0, maxScroll)
	end := scroll + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[scroll:end], "\n")
}

func (m Model) statsFooter() string {
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "scroll"},
		{m.cfg.Keys.Cancel, "close"},
		{m.cfg.Keys.Quit, "quit"},
	})
}

// statsContent computes the read-only productivity dashboard from m.tasks,
// which always holds the full task set (FetchTasks). All bucketing is done in
// the local timezone so day boundaries match the agenda/report view.
func (m Model) statsContent() string {
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	tomorrow := today.Add(24 * time.Hour)
	weekStart := today.AddDate(0, 0, -6)   // last 7 days, inclusive
	monthStart := today.AddDate(0, 0, -29) // last 30 days, inclusive

	var total, done, pending, overdue, dueToday, recurring int
	var doneToday, doneWeek, doneMonth int
	completedByDay := map[string]int{}
	prioPending := map[int]int{}
	topicPending := map[string]int{}

	for _, t := range m.tasks {
		total++
		if isRecurringTask(t) && isActive(t) {
			recurring++
		}
		if isDone(t) {
			done++
		} else {
			pending++
			prioPending[t.Priority]++
			for _, tp := range t.Topics {
				topicPending[tp]++
			}
			if t.Due.Valid {
				d := t.Due.Time
				if d.Before(today) {
					overdue++
				} else if d.Before(tomorrow) {
					dueToday++
				}
			}
		}
		if t.CompletedAt.Valid {
			c := t.CompletedAt.Time.In(loc)
			cDay := time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, loc)
			completedByDay[cDay.Format("2006-01-02")]++
			if !cDay.Before(today) {
				doneToday++
			}
			if !cDay.Before(weekStart) {
				doneWeek++
			}
			if !cDay.Before(monthStart) {
				doneMonth++
			}
		}
	}

	if total == 0 {
		return m.styles.Muted.Render("No tasks yet — press " + m.cfg.Keys.Add + " to add one.")
	}

	// Streaks. A day counts if it had ≥1 completion. The current streak ends at
	// today, or yesterday if nothing is done yet today (so it survives until a
	// full day is missed).
	inSet := func(d time.Time) bool { _, ok := completedByDay[d.Format("2006-01-02")]; return ok }
	curStreak := 0
	start := today
	if !inSet(start) {
		start = today.AddDate(0, 0, -1)
	}
	for inSet(start) {
		curStreak++
		start = start.AddDate(0, 0, -1)
	}
	var days []time.Time
	for k := range completedByDay {
		if d, err := time.ParseInLocation("2006-01-02", k, loc); err == nil {
			days = append(days, d)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	longest, run := 0, 0
	var prev time.Time
	for i, d := range days {
		if i > 0 && d.Equal(prev.AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		prev = d
	}

	bar := func(filled, width int) string {
		if filled < 0 {
			filled = 0
		}
		if filled > width {
			filled = width
		}
		return strings.Repeat("█", filled) + m.styles.Muted.Render(strings.Repeat("·", width-filled))
	}

	var b strings.Builder
	heading := func(s string) { b.WriteString(m.styles.Heading.Render(s) + "\n") }
	line := func(s string) { b.WriteString(s + "\n") }

	b.WriteString(m.styles.Muted.Render(now.Format("Monday, Jan 2, 2006")) + "\n\n")

	// When scoped to a real topic, lead with that project's progress and, if it
	// has a custom workflow, its stage funnel.
	if scoped := m.scopedTopicName(); scoped != "" {
		st := m.topicStats()[scoped]
		spct := 0
		if st.total > 0 {
			spct = st.done * 100 / st.total
		}
		heading("Project · " + scoped)
		line(fmt.Sprintf("  %s  %d/%d done (%d%%)   %s %d",
			pctBar(st.done, st.total, 16), st.done, st.total, spct,
			m.styles.Danger.Render("overdue"), st.overdue))
		if counts := m.topicStageStats(scoped); len(counts) > 0 {
			parts := make([]string, 0, len(counts))
			for _, c := range counts {
				parts = append(parts, fmt.Sprintf("%s %s", m.stageBadge(c.Stage), m.styles.Accent.Render(fmt.Sprintf("%d", c.Count))))
			}
			line("  " + strings.Join(parts, m.styles.Muted.Render(" → ")))
		}
		b.WriteString("\n")
	}

	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	heading("Overview")
	line(fmt.Sprintf("  Total %d   %s %d (%d%%)   %s %d",
		total,
		m.styles.Success.Render("Done"), done, pct,
		m.styles.Warning.Render("Pending"), pending))
	line(fmt.Sprintf("  %s %d   %s %d   %s %d",
		m.styles.Danger.Render("Overdue"), overdue,
		m.styles.Accent.Render("Due today"), dueToday,
		m.styles.Muted.Render("Recurring"), recurring))
	b.WriteString("\n")

	heading("Completed")
	line(fmt.Sprintf("  Today %d   This week %d   This month %d   All time %d",
		doneToday, doneWeek, doneMonth, done))
	b.WriteString("\n")

	heading("Streak")
	line(fmt.Sprintf("  Current %s   Longest %s",
		m.styles.Accent.Render(fmt.Sprintf("%d day(s)", curStreak)),
		fmt.Sprintf("%d day(s)", longest)))
	b.WriteString("\n")

	heading("Last 7 days")
	maxDay := 1
	for i := 0; i < 7; i++ {
		if c := completedByDay[weekStart.AddDate(0, 0, i).Format("2006-01-02")]; c > maxDay {
			maxDay = c
		}
	}
	for i := 0; i < 7; i++ {
		d := weekStart.AddDate(0, 0, i)
		c := completedByDay[d.Format("2006-01-02")]
		filled := c * 18 / maxDay
		if c > 0 && filled == 0 {
			filled = 1
		}
		label := d.Format("Mon 01/02")
		if d.Equal(today) {
			label = m.styles.Accent.Render(label)
		} else {
			label = m.styles.Muted.Render(label)
		}
		line(fmt.Sprintf("  %s  %s %d", label, bar(filled, 18), c))
	}
	b.WriteString("\n")

	if pending > 0 {
		heading("Pending by priority")
		var prios []int
		for p := range prioPending {
			prios = append(prios, p)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(prios)))
		maxP := 1
		for _, c := range prioPending {
			if c > maxP {
				maxP = c
			}
		}
		for _, p := range prios {
			c := prioPending[p]
			line(fmt.Sprintf("  P%-2d  %s %d", p, bar(c*18/maxP, 18), c))
		}
		b.WriteString("\n")
	}

	if len(topicPending) > 0 {
		heading("Top topics (pending)")
		type tc struct {
			topic string
			count int
		}
		var tcs []tc
		for t, c := range topicPending {
			tcs = append(tcs, tc{t, c})
		}
		sort.Slice(tcs, func(i, j int) bool {
			if tcs[i].count != tcs[j].count {
				return tcs[i].count > tcs[j].count
			}
			return tcs[i].topic < tcs[j].topic
		})
		maxT := 1
		for _, t := range tcs {
			if t.count > maxT {
				maxT = t.count
			}
		}
		for i, t := range tcs {
			if i >= 8 {
				break
			}
			line(fmt.Sprintf("  %-14s %s %d", truncateText(t.topic, 14), bar(t.count*18/maxT, 18), t.count))
		}
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m Model) statsLines() []string {
	return strings.Split(m.statsContent(), "\n")
}

func (m Model) statsMaxScroll() int {
	if m.height <= 0 {
		return 0
	}
	bodyMax := m.height - 1 - 2 - countLines(m.statsFooter()) // 2 = panel borders
	if bodyMax <= 0 {
		return 0
	}
	lines := m.statsLines()
	if len(lines) <= bodyMax {
		return 0
	}
	return len(lines) - bodyMax
}

func (m Model) renderStatsBody(maxLines int) string {
	lines := m.statsLines()
	if maxLines <= 0 || len(lines) == 0 {
		return ""
	}
	maxScroll := len(lines) - maxLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := clampInt(m.statsScroll, 0, maxScroll)
	end := scroll + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[scroll:end], "\n")
}

func (m Model) renderStatsView() string {
	footer := m.statsFooter()
	bodyMax := 0
	if m.height > 0 {
		bodyMax = m.height - 1 - 2 - countLines(footer) // 2 = panel borders
		if bodyMax < 1 {
			bodyMax = 1
		}
	}
	var body string
	if m.height > 0 {
		body = m.renderStatsBody(bodyMax)
	} else {
		body = m.statsContent()
	}
	return m.panel("bada ∙ Stats", body) + "\n" + footer
}

func (m Model) renderTrashView() string {
	footer := m.trashViewFooter()
	bodyMax := 0
	if m.height > 0 {
		bodyMax = m.height - 1 - 2 - countLines(footer) // 2 = panel borders
		if bodyMax < 1 {
			bodyMax = 1
		}
	}
	var body string
	if m.height > 0 {
		body = m.renderTrashBody(bodyMax)
	} else {
		body = m.renderTrashContent()
	}
	return m.panel("bada ∙ Trash", body) + "\n" + footer
}

func (m Model) trashViewFooter() string {
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "move"},
		{"space", "select"},
		{"u", "restore"},
		{"P", "purge"},
		{m.cfg.Keys.Cancel, "close"},
	})
}

func (m Model) trashHeaderLine() string {
	inner := m.panelInnerWidth()
	header := "  🗑 DeletedAt          Title                          Topics"
	return m.styles.TableHeader.Width(inner).MaxWidth(inner).Render(header)
}

func (m Model) renderTrashContent() string {
	head := m.trashHeaderLine()
	rows := m.trashRows()
	if len(rows) == 0 {
		return head + "\n" + m.styles.Muted.Render("  (trash is empty)")
	}
	return head + "\n" + strings.Join(rows, "\n")
}

func (m Model) renderTrashBody(maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	head := m.trashHeaderLine()
	rowMax := maxLines - 1 // reserve a line for the column header
	rows := m.trashRows()
	if len(rows) == 0 {
		return head + "\n" + m.styles.Muted.Render("  (trash is empty)")
	}
	if rowMax < 1 {
		return head
	}
	maxScroll := len(rows) - rowMax
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := clampInt(m.trashScroll, 0, maxScroll)
	end := scroll + rowMax
	if end > len(rows) {
		end = len(rows)
	}
	return head + "\n" + strings.Join(rows[scroll:end], "\n")
}

func (m Model) trashRows() []string {
	inner := m.panelInnerWidth()
	full := func(style lipgloss.Style, s string) string {
		// Clip over-wide rows to the panel width first; otherwise lipgloss's
		// Width() word-wraps them onto a second line, which reads as a phantom
		// duplicate row on terminals too narrow for every column.
		if lipgloss.Width(s) > inner {
			s = truncateANSI(s, inner)
		}
		return style.Width(inner).MaxWidth(inner).Render(s)
	}
	rows := make([]string, 0, len(m.trash))
	for i, entry := range m.trash {
		title := truncateText(entry.Task.Title, 30)
		deleted := entry.DeletedAt.Format("2006-01-02 15:04")
		line := fmt.Sprintf("  🗑 %-18s %-30s %-16s", deleted, title, strings.Join(entry.Task.Topics, ","))
		switch {
		case m.mode == modeTrash && m.trashCursor == i:
			line = full(m.styles.Selection, line)
		case m.trashSelected != nil && m.trashSelected[i]:
			line = full(m.styles.Accent, line)
		default:
			line = full(lipgloss.NewStyle(), line)
		}
		rows = append(rows, line)
	}
	return rows
}

func (m Model) trashBodyMaxLines() int {
	if m.height <= 0 {
		return 0
	}
	// rows available inside the panel (minus borders, footer, and column header)
	bodyMax := m.height - 1 - 2 - countLines(m.trashViewFooter()) - 1
	if bodyMax < 0 {
		bodyMax = 0
	}
	return bodyMax
}

func (m *Model) adjustTrashScroll() {
	bodyMax := m.trashBodyMaxLines()
	if bodyMax <= 0 {
		m.trashScroll = 0
		return
	}
	maxScroll := len(m.trash) - bodyMax
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.trashCursor < m.trashScroll {
		m.trashScroll = m.trashCursor
	} else if m.trashCursor >= m.trashScroll+bodyMax {
		m.trashScroll = m.trashCursor - bodyMax + 1
	}
	m.trashScroll = clampInt(m.trashScroll, 0, maxScroll)
}

func (m Model) ganttFooter() string {
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "move"},
		{"h/l", "pan"},
		{"H/L", "pan week"},
		{"0", "today"},
		{m.cfg.Keys.Toggle, "status"},
		{m.cfg.Keys.Edit, "edit"},
		{m.cfg.Keys.DueBack + "/" + m.cfg.Keys.DueForward, "shift due"},
		{"enter", "notes"},
		{m.cfg.Keys.Cancel, "close"},
	})
}

func (m Model) renderGanttView() string {
	footer := m.ganttFooter()
	bodyMax := 0
	if m.height > 0 {
		bodyMax = m.height - 1 - 2 - countLines(footer) // 2 = panel borders
		if bodyMax < 1 {
			bodyMax = 1
		}
	}
	var body string
	if m.height > 0 {
		body = m.renderTimelinePaneBody(bodyMax)
	} else {
		body = m.renderTimelinePaneBody(-1)
	}
	return m.panel(m.timelinePanelTitle(), body) + "\n" + footer
}

func (m Model) renderTaskList() string {
	return m.renderTaskListWithHeight(-1)
}

func (m Model) renderTimelinePaneBody(maxLines int) string {
	body := m.timelineGridLines(maxLines)
	if maxLines <= 0 {
		return strings.Join(body, "\n")
	}
	if len(body) > maxLines {
		body = body[:maxLines]
	}
	for len(body) < maxLines {
		body = append(body, "")
	}
	return strings.Join(body, "\n")
}

// timelineCellW is the column width, in terminal cells, of a single day on the
// timeline grid. Three keeps day numbers and bar blocks legible without bunching.
const timelineCellW = 3

// timelineItem is one task placed on the timeline grid, with its start/due
// already normalized to whole days.
type timelineItem struct {
	task  storage.Task
	start time.Time
	due   time.Time
}

// timelineNavItems builds one timeline row per visible task, preserving the
// list's order so the cursor maps 1:1 onto gantt rows for navigation. Tasks
// without a due date are kept (selectable/editable) but render an empty track.
func (m Model) timelineNavItems() []timelineItem {
	items := make([]timelineItem, 0)
	for _, it := range m.visibleItems() {
		if it.kind != itemTask {
			continue
		}
		t := it.task
		start := t.CreatedAt
		if t.Start.Valid {
			start = t.Start.Time
		}
		start = normalizeDate(start)
		due := start
		if t.Due.Valid {
			due = normalizeDate(t.Due.Time)
			if due.Before(start) {
				start = due
			}
		}
		items = append(items, timelineItem{task: t, start: start, due: due})
	}
	return items
}

// timelineScale describes the timeline's horizontal axis: the date at column 0,
// how many calendar days each column spans (the zoom unit), the column count,
// and the left label gutter width. Columns are always timelineCellW cells wide
// regardless of the zoom unit.
type timelineScale struct {
	start    time.Time
	unitDays int
	colCount int
	leftW    int
}

func (s timelineScale) colStartDate(i int) time.Time { return s.start.AddDate(0, 0, i*s.unitDays) }
func (s timelineScale) colEndDate(i int) time.Time {
	return s.start.AddDate(0, 0, (i+1)*s.unitDays-1)
}

// colOf returns the column index containing date d (may be <0 or >=colCount).
func (s timelineScale) colOf(d time.Time) int {
	return floorDiv(dayIndexUTC(d, s.start), s.unitDays)
}

func (s timelineScale) unitLabel() string {
	u := s.unitDays
	switch {
	case u == 1:
		return "1col=1d"
	case u == 7:
		return "1col=1wk"
	case u%365 == 0:
		if y := u / 365; y > 1 {
			return fmt.Sprintf("1col=%dyr", y)
		}
		return "1col=1yr"
	case u%30 == 0:
		if mo := u / 30; mo > 1 {
			return fmt.Sprintf("1col=%dmo", mo)
		}
		return "1col=1mo"
	default:
		return fmt.Sprintf("1col=%dd", u)
	}
}

// dayIndexUTC counts whole calendar days from ref to d, using UTC midnights so
// the arithmetic is exact (no DST hour drift).
func dayIndexUTC(d, ref time.Time) int {
	du := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	ru := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
	return int(du.Sub(ru).Hours() / 24)
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// chooseUnit picks days-per-column so a spanDays-long range fits within cols,
// snapping to friendly units (day → week → month → quarter → half-year → year →
// multi-year) and reserving a column of padding on each side.
func chooseUnit(spanDays, cols int) int {
	eff := cols - 2
	if eff < 1 {
		eff = cols
	}
	if spanDays <= eff {
		return 1
	}
	// 1wk, 1mo, 3mo, 6mo, 1yr, 2yr, 3yr, 5yr, 10yr.
	for _, u := range []int{7, 30, 90, 180, 365, 365 * 2, 365 * 3, 365 * 5, 365 * 10} {
		if u*eff >= spanDays {
			return u
		}
	}
	// Beyond ~a century of span, snap up to whole years.
	years := (spanDays + 365*eff - 1) / (365 * eff)
	return years * 365
}

func selectedTimelineItem(items []timelineItem, cursor int) (timelineItem, bool) {
	if cursor < 0 || cursor >= len(items) {
		return timelineItem{}, false
	}
	return items[cursor], true
}

// timelineWindow computes the visible date span and column layout. By default it
// keeps today in view and pulls back to reveal the earliest dated task. When the
// selected task's start→due span doesn't fit a daily window, it zooms the unit
// out (week/month) and reframes so that task's whole bar — and its due date —
// stays visible.
func (m Model) timelineWindow(items []timelineItem) timelineScale {
	inner := m.panelInnerWidth()
	gridWidth := clampInt(inner-40, 21, 90)
	colCount := gridWidth / timelineCellW
	if colCount < 7 {
		colCount = 7
	}
	leftW := inner - colCount*timelineCellW - 1
	if leftW < 24 {
		leftW = 24
	}

	anchor := normalizeDate(time.Now()).AddDate(0, 0, m.ganttOffsetDays)
	start := anchor.AddDate(0, 0, -2)
	haveMin := false
	var minStart time.Time
	for _, it := range items {
		if !it.task.Due.Valid {
			continue // undated tasks don't anchor the date axis
		}
		if !haveMin || it.start.Before(minStart) {
			minStart = it.start
			haveMin = true
		}
	}
	// Pull the window back so earlier (e.g. overdue) tasks stay on screen.
	if haveMin && minStart.Before(start) {
		start = minStart
	}
	// ...but never so far back that the current pan anchor scrolls off the right edge.
	earliest := anchor.AddDate(0, 0, -(colCount - 3))
	if start.Before(earliest) {
		start = earliest
	}

	// Zoom to the selected task when its span overflows the default daily window.
	if sel, ok := selectedTimelineItem(items, m.cursor); ok && sel.task.Due.Valid {
		lo := start
		if sel.start.Before(lo) {
			lo = sel.start
		}
		if anchor.Before(lo) {
			lo = anchor
		}
		hi := anchor
		if sel.due.After(hi) {
			hi = sel.due
		}
		spanDays := dayIndexUTC(hi, lo) + 1
		unit := chooseUnit(spanDays, colCount)
		if unit > 1 {
			return timelineScale{start: lo.AddDate(0, 0, -unit), unitDays: unit, colCount: colCount, leftW: leftW}
		}
		// Daily unit still fits; shift the window if the task falls outside it.
		if sel.start.Before(start) || sel.due.After(start.AddDate(0, 0, colCount-1)) {
			start = lo
		}
	}
	return timelineScale{start: start, unitDays: 1, colCount: colCount, leftW: leftW}
}

// timelinePanelTitle labels the pane with its visible date span, echoing the
// reference's "Gantt Chart (start to end)" header.
func (m Model) timelinePanelTitle() string {
	scale := m.timelineWindow(m.timelineNavItems())
	start := scale.colStartDate(0)
	end := scale.colEndDate(scale.colCount - 1)
	extra := ""
	if scale.unitDays > 1 {
		extra = "  (" + scale.unitLabel() + ")"
	}
	if m.ganttOffsetDays != 0 {
		extra += fmt.Sprintf("  pan %+dd", m.ganttOffsetDays)
	}
	// Keep only the branding "·" (one ambiguous-width cell, like every panel
	// title); the rest stays ASCII so a long title can't overflow the framed top
	// border in CJK terminals, where "·"/"↑"/"–" render double-width.
	return fmt.Sprintf("bada ∙ Timeline  %s-%s%s", start.Format("Jan 2"), end.Format("Jan 2"), extra)
}

// ganttColCellBg styles timeline body cells. Today's column carries a thin
// vertical "now" line (see timelineTaskRow) rather than a full-height band.
func (m Model) ganttColCellBg(base lipgloss.Style, isToday bool) lipgloss.Style {
	return base
}

func (m Model) ganttHeaderCellBg(base lipgloss.Style, d time.Time, isToday bool) lipgloss.Style {
	return base
}

func (m Model) timelineGridLines(maxLines int) []string {
	items := m.timelineNavItems()
	scale := m.timelineWindow(items)
	today := normalizeDate(time.Now())

	lines := make([]string, 0)
	lines = append(lines, m.timelineMonthHeader(scale, today))
	lines = append(lines, m.timelineDayHeader(scale, today))

	rowBudget := len(items)
	if maxLines > 0 {
		rowBudget = maxLines - len(lines) - 1 // reserve a row for the legend
		if rowBudget < 0 {
			rowBudget = 0
		}
	}
	if rowBudget > len(items) {
		rowBudget = len(items)
	}

	// Scroll the row window so the cursor's row stays visible.
	scroll := 0
	if rowBudget > 0 && m.cursor >= rowBudget {
		scroll = m.cursor - rowBudget + 1
	}
	if maxScroll := len(items) - rowBudget; scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}

	for i := 0; i < rowBudget && scroll+i < len(items); i++ {
		idx := scroll + i
		lines = append(lines, m.timelineTaskRow(items[idx], scale, today, idx == m.cursor))
	}
	if len(items) == 0 {
		lines = append(lines, m.styles.Muted.Render("(no tasks)"))
	}
	// Pin the legend to the bottom of the box: pad blank rows between the last
	// task and the legend so it doesn't float up under a short list.
	if maxLines > 0 {
		for len(lines) < maxLines-1 {
			lines = append(lines, "")
		}
	}
	if maxLines <= 0 || len(lines) < maxLines {
		lines = append(lines, m.timelineLegend())
	}
	return lines
}

func (m Model) timelineMonthHeader(scale timelineScale, today time.Time) string {
	n := scale.colCount
	cells := make([]rune, n*timelineCellW)
	for i := range cells {
		cells[i] = ' '
	}
	// Zoomed in (day/week), the top row labels months at month boundaries.
	// Zoomed out (month or coarser), it labels years at year boundaries so a long
	// timeline stays oriented instead of cramming "JanFeb…" with no anchor. Years
	// always print 2-digit ("'26", three cells) so they fit a column exactly and
	// can't collide even when a year boundary lands on adjacent columns (which
	// happens at quarter/half-year units starting late in a year).
	coarse := scale.unitDays >= 28
	prevKey := -1
	for i := 0; i < n; i++ {
		d := scale.colStartDate(i)
		key := int(d.Month())
		text := d.Format("Jan")
		if coarse {
			key = d.Year()
			text = "'" + d.Format("06")
		}
		if i == 0 || key != prevKey {
			label := []rune(text)
			pos := i * timelineCellW
			for j := 0; j < len(label) && pos+j < len(cells); j++ {
				cells[pos+j] = label[j]
			}
		}
		prevKey = key
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		seg := string(cells[i*timelineCellW : i*timelineCellW+timelineCellW])
		b.WriteString(m.ganttHeaderCellBg(m.styles.Accent, scale.colStartDate(i), false).Render(seg))
	}
	return m.styles.Muted.Render(padRightWidth("", scale.leftW)) + " " + b.String()
}

func (m Model) timelineDayHeader(scale timelineScale, today time.Time) string {
	var b strings.Builder
	todayCol := scale.colOf(today)
	// Zoomed out, the second row carries month abbreviations (the year sits on the
	// row above); zoomed in, it carries day-of-month numbers.
	monthMode := scale.unitDays >= 28
	for i := 0; i < scale.colCount; i++ {
		d := scale.colStartDate(i)
		cell := fmt.Sprintf("%2d ", d.Day())
		if monthMode {
			// Numeric months (" 7 ", "12 ") read clearly under the year row above
			// and leave gaps, where packed "JulAugSep" names look cramped.
			cell = fmt.Sprintf("%2d ", int(d.Month()))
		}
		var fg lipgloss.Style
		switch {
		case i == todayCol:
			fg = m.styles.Accent.Bold(true).Underline(true)
		case scale.unitDays > 1:
			// Weekend/holiday shading is only meaningful at daily resolution.
			fg = m.styles.Muted
		case isHoliday(m, d):
			fg = m.styles.Danger
			if c := m.cfg.Theme.HolidayBg; c != "" {
				fg = fg.Background(lipgloss.Color(c))
			}
		case d.Weekday() == time.Saturday || d.Weekday() == time.Sunday:
			fg = m.styles.Danger
			if c := m.cfg.Theme.StatusBg; c != "" {
				fg = fg.Background(lipgloss.Color(c))
			}
		default:
			fg = m.styles.Muted
		}
		b.WriteString(m.ganttHeaderCellBg(fg, d, i == todayCol).Render(cell))
	}
	return m.styles.TableHeader.Render(padRightWidth("  ID  Task", scale.leftW)) + " " + b.String()
}

func isHoliday(m Model, d time.Time) bool {
	_, ok := m.holidayName(d)
	return ok
}

// holidayName reports whether d is a configured public holiday, returning its
// name. A holiday date is matched as a full "2006-01-02" (one-off) or a "01-02"
// month-day that recurs every year.
func (m Model) holidayName(d time.Time) (string, bool) {
	full := d.Format("2006-01-02")
	md := d.Format("01-02")
	for _, h := range m.cfg.Holidays {
		switch strings.TrimSpace(h.Date) {
		case full, md:
			return h.Name, true
		}
	}
	return "", false
}

func (m Model) timelineSelectionSummary(items []timelineItem, scale timelineScale) string {
	width := scale.leftW + 1 + scale.colCount*timelineCellW
	it, ok := selectedTimelineItem(items, m.cursor)
	if !ok {
		return m.styles.Muted.Render(padRightWidth("  No task selected", width))
	}
	t := it.task
	parts := []string{fmt.Sprintf("#%d", t.ID), m.taskStatusLabel(t)}
	if t.Priority != 0 {
		parts = append(parts, priorityLabel(t.Priority))
	}
	parts = append(parts, "start "+it.start.Format("Jan 2"))
	if t.Due.Valid {
		parts = append(parts, "due "+it.due.Format("Jan 2"))
	} else {
		parts = append(parts, "no due")
	}
	if strings.TrimSpace(t.Assignee) != "" {
		parts = append(parts, "@"+strings.TrimSpace(t.Assignee))
	}
	if strings.TrimSpace(t.Tags) != "" {
		parts = append(parts, strings.TrimSpace(t.Tags))
	}
	line := "  " + truncateTextWidth(strings.Join(parts, "  "), width-2)
	return m.styles.StatusAlt.Render(padRightWidth(line, width))
}

func (m Model) timelineTaskRow(it timelineItem, scale timelineScale, today time.Time, selected bool) string {
	task := it.task
	hasDue := task.Due.Valid
	marker := "  "
	title := truncateTextWidth(task.Title, scale.leftW-7)
	if isDone(task) {
		title = lipgloss.NewStyle().Strikethrough(true).Render(title)
	}
	label := fmt.Sprintf("%s%-3d %s", marker, task.ID, title)
	dueCol := -1
	if hasDue {
		dueCol = scale.colOf(it.due)
	}
	todayCol := scale.colOf(today)
	var b strings.Builder
	for i := 0; i < scale.colCount; i++ {
		cs := scale.colStartDate(i)
		ce := scale.colEndDate(i)
		content := " ∙ "
		fg := m.styles.Muted
		// A wide column is "in plan" when it overlaps the task's start→due span.
		inPlan := hasDue && !it.start.After(ce) && !it.due.Before(cs)
		if inPlan {
			// ▬/▮ are guaranteed one cell wide; the heavy "━"/full-block "█" are
			// East-Asian ambiguous and double up in CJK terminals.
			content = "▬▬▬"
			fg = m.styles.Border
			if task.Status == "IN-PROGRESS" {
				fg = m.styles.Success
			}
		}
		if i == dueCol {
			content = "▮▮▮"
			fg = m.styles.Accent.Bold(true)
			bandColor := m.cfg.Theme.Accent
			if isOverdue(task) {
				fg = m.styles.Danger.Bold(true)
				bandColor = m.cfg.Theme.Danger
			}
			if bandColor != "" {
				fg = fg.Background(lipgloss.Color(bandColor))
			}
		}
		if i == todayCol {
			// Draw the current-date marker as a thin vertical line down the chart.
			// It overlays whatever sits in this column's centre — empty slot, plan
			// bar, or due band — so the "now" line stays continuous across rows.
			runes := []rune(content)
			b.WriteString(m.ganttColCellBg(fg, true).Render(string(runes[0])))
			b.WriteString(m.styles.Warning.Bold(true).Render("│"))
			b.WriteString(m.ganttColCellBg(fg, true).Render(string(runes[2])))
		} else {
			b.WriteString(m.ganttColCellBg(fg, false).Render(content))
		}
	}
	labelOut := padRightWidth(label, scale.leftW)
	if selected {
		// Keep the cursor block scoped to the task/title column. stripeLine preserves
		// a continuous selection background even when the title itself contains ANSI
		// styling, such as strikethrough for done tasks.
		labelOut = stripeLine(m.styles.Selection, labelOut)
	}
	row := labelOut + " " + b.String()
	rowWidth := scale.leftW + 1 + scale.colCount*timelineCellW
	if lipgloss.Width(row) > rowWidth {
		return truncateANSI(row, rowWidth)
	}
	return padRightWidth(row, rowWidth)
}

func (m Model) timelineLegend() string {
	weekend := m.styles.Muted
	if c := m.cfg.Theme.StatusBg; c != "" {
		weekend = weekend.Background(lipgloss.Color(c))
	}
	todayCell := m.styles.Accent.Bold(true).Underline(true)
	parts := []string{
		m.styles.Border.Render("▬▬") + m.styles.Muted.Render(" planned"),
		m.styles.Success.Render("▬▬") + m.styles.Muted.Render(" in-progress"),
		m.styles.Accent.Render("▮▮") + m.styles.Muted.Render(" due"),
		m.styles.Danger.Render("▮▮") + m.styles.Muted.Render(" overdue"),
		weekend.Render("  ") + m.styles.Muted.Render(" weekend"),
		todayCell.Render("dd") + m.styles.Warning.Bold(true).Render("│") + m.styles.Muted.Render(" today"),
	}
	// Only advertise holidays when the user has configured some.
	if len(m.cfg.Holidays) > 0 {
		holiday := m.styles.Danger
		if c := m.cfg.Theme.HolidayBg; c != "" {
			holiday = holiday.Background(lipgloss.Color(c))
		}
		parts = append(parts, holiday.Render("  ")+m.styles.Muted.Render(" holiday"))
	}
	return " " + strings.Join(parts, m.styles.Muted.Render("  "))
}

func (m Model) renderTaskListWithHeight(maxLines int) string {
	items := m.visibleItems()
	inner := m.panelInnerWidth()

	statusW := 14
	assigneeW := 7
	reporterW := 7
	topicW := 14
	dateW := 10
	// Title leads the row and takes whatever the other columns don't: lead (2) +
	// 8 non-title columns (76) + their 8 separating spaces = 86.
	titleW := inner - 86
	if titleW < 10 {
		titleW = 10
	}
	if titleW > 60 {
		titleW = 60
	}

	full := func(style lipgloss.Style, s string) string {
		// Clip over-wide rows to the panel width first; otherwise lipgloss's
		// Width() word-wraps them onto a second line, which reads as a phantom
		// duplicate row on terminals too narrow for every column.
		if lipgloss.Width(s) > inner {
			s = truncateANSI(s, inner)
		}
		return style.Width(inner).MaxWidth(inner).Render(s)
	}

	lines := make([]string, 0)
	// Keep the table header as the first body row in every list state. Previously
	// filter/search banners were inserted above it, which made the header jump
	// between terminal rows and could leave a stale duplicate during incremental
	// screen refreshes. Active filter/search state is shown in the panel title and
	// status bar instead.
	// sortCol appends a direction marker to the column whose sort mode is active
	// (▴ default order, ▾ reversed), mirroring the status-bar arrow. Small
	// triangles (U+25B4/25BE) are used instead of the large ▲/▼ because the large
	// ones are East-Asian *ambiguous* width — a CJK terminal renders them as two
	// cells while lipgloss/Go padding count one, overflowing the header row and
	// wrapping it onto a phantom second line. The small triangles are always one
	// cell wide.
	sortMark := "▴"
	if m.sortReversed {
		sortMark = "▾"
	}
	sortCol := func(label, mode string, width int) string {
		if m.sortMode == mode {
			label += sortMark
		}
		return padRightWidth(truncateTextWidth(label, width), width)
	}
	headerCols := []string{
		sortCol("Title", "title", titleW),
		sortCol("Status", "state", statusW),
		sortCol("Topic", "topic", topicW),
		padRightWidth("Assign", assigneeW),
		padRightWidth("Report", reporterW),
		sortCol("Pri", "priority", 4),
		padRightWidth("Due-in", dateW),
		sortCol("Due", "due", dateW),
		padRightWidth("End", dateW),
	}
	header := "  " + strings.Join(headerCols, " ")
	lines = append(lines, full(m.styles.TableHeader, header))

	itemLines := make([]string, 0, len(items))
	for i, it := range items {
		selected := m.cursor == i && m.mode == modeList
		switch it.kind {
		case itemTopic:
			line := ""
			if isSpecialTopic(it.topic) {
				line = fmt.Sprintf("  %-*s %s", statusW, "TOPIC", it.topic)
			} else {
				stat := m.topicStats()[it.topic]
				line = fmt.Sprintf("  %-*s %s (%d/%d)", statusW, "TOPIC", it.topic, stat.overdue, stat.total)
			}
			switch {
			case selected:
				line = full(m.styles.Selection, line)
			case isSpecialTopic(it.topic):
				line = full(m.styles.Heading, line)
			default:
				line = full(m.styles.Accent, line)
			}
			itemLines = append(itemLines, line)
		case itemTask:
			title := truncateText(it.task.Title, titleW)
			assignee := truncateText(emptyDash(it.task.Assignee), assigneeW)
			reporter := truncateText(emptyDash(it.task.Reporter), reporterW)
			topic := truncateText(topicListLabel(it.task.Topics), topicW)
			dueIn := truncateText(relativeDueCell(it.task.Due), dateW)
			due := truncateText(dateCell(it.task.Due), dateW)
			end := truncateText(dateCell(it.task.End), dateW)
			if due == "" {
				due = "pending"
			}
			buildBody := func(statusField, priField string) string {
				return fmt.Sprintf("  %-*s %s %-*s %-*s %-*s %s %-*s %-*s %-*s",
					titleW, title,
					statusField,
					topicW, topic,
					assigneeW, assignee,
					reporterW, reporter,
					priField,
					dateW, dueIn,
					dateW, due,
					dateW, end)
			}

			if selected {
				body := buildBody(m.statusField(it.task, statusW, false), m.priorityField(it.task.Priority, false))
				itemLines = append(itemLines, full(m.styles.Selection, body))
				continue
			}
			// Color the status badge and priority flag only on default rows;
			// selection/done rows recolor the whole line, so use plain cells there
			// to avoid clashing.
			var line string
			switch {
			case m.isTaskSelected(it.task.ID):
				body := buildBody(m.statusField(it.task, statusW, false), m.priorityField(it.task.Priority, false))
				line = full(m.styles.Warning, body)
			case isDone(it.task):
				body := buildBody(m.statusField(it.task, statusW, false), m.priorityField(it.task.Priority, false))
				line = full(m.styles.Done, body)
			default:
				body := buildBody(m.statusField(it.task, statusW, true), m.priorityField(it.task.Priority, true))
				line = full(lipgloss.NewStyle(), body)
			}
			itemLines = append(itemLines, line)
		}
	}
	if len(items) == 0 {
		itemLines = append(itemLines, full(m.styles.Muted, "  (no tasks)"))
	}

	if maxLines >= 0 {
		available := maxLines - len(lines)
		if available < 0 {
			available = 0
		}
		if available == 0 {
			itemLines = nil
		} else if len(itemLines) > available {
			start := 0
			if len(items) > 0 && m.cursor >= 0 {
				cur := clampCursor(m.cursor, len(items))
				if cur >= start+available {
					start = cur - available + 1
				}
				if start+available > len(itemLines) {
					start = len(itemLines) - available
				}
				if start < 0 {
					start = 0
				}
			}
			itemLines = itemLines[start : start+available]
		}
		lines = append(lines, itemLines...)
		if len(lines) > maxLines {
			lines = lines[:maxLines]
		}
		return strings.Join(lines, "\n")
	}

	lines = append(lines, itemLines...)
	return strings.Join(lines, "\n")
}
