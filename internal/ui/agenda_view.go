package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"bada/internal/storage"
)

func (m *Model) refreshReport() {
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	tomorrow := today.Add(24 * time.Hour)
	upcomingDays := m.cfg.Agenda.UpcomingDays
	if upcomingDays <= 0 {
		upcomingDays = 3
	}
	soon := today.AddDate(0, 0, upcomingDays)

	var overdue, todayList, upcoming, recurring []storage.Task
	for _, t := range m.tasks {
		if isRecurringTask(t) && isActive(t) {
			recurring = append(recurring, t)
		}
		if isDone(t) || !t.Due.Valid {
			continue
		}
		d := t.Due.Time.In(loc)
		if d.Before(today) {
			overdue = append(overdue, t)
			continue
		}
		if !d.After(tomorrow.Add(-time.Nanosecond)) && d.After(today.Add(-time.Nanosecond)) {
			todayList = append(todayList, t)
			continue
		}
		if d.Before(soon) {
			upcoming = append(upcoming, t)
			continue
		}
	}

	sortAgendaTasks := func(tasks []storage.Task) {
		sort.SliceStable(tasks, func(i, j int) bool {
			a, b := tasks[i], tasks[j]
			if a.Priority != b.Priority {
				return a.Priority > b.Priority
			}
			if a.Due.Valid && b.Due.Valid && !a.Due.Time.Equal(b.Due.Time) {
				return a.Due.Time.Before(b.Due.Time)
			}
			return a.ID < b.ID
		})
	}
	sortAgendaTasks(overdue)
	sortAgendaTasks(todayList)
	sortAgendaTasks(upcoming)
	sortAgendaTasks(recurring)
	recentAdd := m.recentlyAdded(m.recentLimit)
	recentDone := m.recentlyDone(m.recentLimit)
	totalReportTasks := len(overdue) + len(todayList) + len(upcoming) + len(recurring) + len(recentAdd) + len(recentDone)
	m.reportCursor = clampCursor(m.reportCursor, totalReportTasks)
	m.reportTaskIDs = nil

	// Title column scales with the terminal; the rest of the line (gutter, id,
	// priority, and trailing date) is ~44 cells. "∙" is one cell even in
	// CJK/Termius, unlike "•".
	titleW := clampInt(m.width-48, 16, 52)
	var b strings.Builder
	writeDivider := func() {
		b.WriteString(m.styles.Border.Render(m.ruleLine(m.width)))
		b.WriteString("\n")
	}
	// Section headers read as an iconned label followed by a count badge and a
	// thin rule. Keep the human title casing in the text so the agenda scans like
	// a dashboard instead of a log dump, while retaining "Title (n)" for tests and
	// screen-reader-friendly plain output.
	writeSectionHeader := func(icon, title string, count int, style lipgloss.Style) {
		headText := fmt.Sprintf("  %s  %s (%d)", icon, title, count)
		head := style.Bold(true).Render(headText)
		ruleW := m.width - lipgloss.Width(head) - 1
		if ruleW < 1 {
			ruleW = 1
		}
		rule := m.styles.Border.Render(" " + strings.Repeat("─", ruleW))
		b.WriteString(head + rule)
		b.WriteString("\n")
	}
	localRelativeDue := func(t storage.Task) string {
		if !t.Due.Valid {
			return "-"
		}
		due := normalizeDate(t.Due.Time.In(loc))
		days := int(due.Sub(today).Hours() / 24)
		switch {
		case days == 0:
			if !t.Due.Time.In(loc).IsZero() {
				return "today " + t.Due.Time.In(loc).Format("15:04")
			}
			return "today"
		case days == 1:
			return "tomorrow"
		case days == -1:
			return "1d overdue"
		case days > 1:
			return fmt.Sprintf("in %dd", days)
		default:
			return fmt.Sprintf("%dd overdue", -days)
		}
	}
	// Rows carry a section-colored left gutter, a narrow task bullet, a muted id,
	// the priority flag, a plain (default-foreground) title for readability, and a
	// section-colored trailing date/urgency. The selected row fills with the
	// selection color. The "#NNN" token must stay intact for cursor-visibility
	// lookups.
	writeTasks := func(tasks []storage.Task, style lipgloss.Style, trailing func(storage.Task) string) {
		for _, t := range tasks {
			idx := len(m.reportTaskIDs)
			m.reportTaskIDs = append(m.reportTaskIDs, t.ID)
			title := truncateTextWidth(t.Title, titleW)
			flagCh := "⚐"
			if t.Priority > 0 {
				flagCh = "⚑"
			}
			if idx == m.reportCursor {
				line := fmt.Sprintf(" ▌ ∙ #%-3d %s %-*s  %s", t.ID, flagCh, titleW, title, trailing(t))
				b.WriteString(m.styles.Selection.Render(line))
				b.WriteString("\n")
				continue
			}
			b.WriteString(style.Render(" ▌"))
			b.WriteString(m.styles.Muted.Render(fmt.Sprintf(" ∙ #%-3d ", t.ID)))
			b.WriteString(m.priorityBadge(t.Priority))
			b.WriteString(" ")
			b.WriteString(fmt.Sprintf("%-*s", titleW, title))
			b.WriteString("  ")
			b.WriteString(style.Render(trailing(t)))
			b.WriteString("\n")
		}
	}
	dueTrail := func(t storage.Task) string { return localRelativeDue(t) + " · " + formatDateTime(t.Due) }

	if len(overdue) > 0 {
		writeSectionHeader("⚠", "Overdue", len(overdue), m.styles.Danger)
		writeTasks(overdue, m.styles.Danger, dueTrail)
		b.WriteString("\n")
	}
	if len(todayList) > 0 {
		writeSectionHeader("◆", "Due Today", len(todayList), m.styles.Accent)
		writeTasks(todayList, m.styles.Accent, dueTrail)
		b.WriteString("\n")
	}
	if len(upcoming) > 0 {
		writeSectionHeader("▸", fmt.Sprintf("Upcoming (next %d days)", upcomingDays), len(upcoming), m.styles.Warning)
		grouped := map[string][]storage.Task{}
		var days []time.Time
		seen := map[string]bool{}
		for _, t := range upcoming {
			d := normalizeDate(t.Due.Time.In(loc))
			key := d.Format("2006-01-02")
			grouped[key] = append(grouped[key], t)
			if !seen[key] {
				seen[key] = true
				days = append(days, d)
			}
		}
		sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
		for _, day := range days {
			key := day.Format("2006-01-02")
			b.WriteString(m.styles.Muted.Render("  " + agendaDayLabel(day, today)))
			b.WriteString("\n")
			writeTasks(grouped[key], m.styles.Muted, dueTrail)
		}
		b.WriteString("\n")
	}
	if len(recurring) > 0 {
		writeSectionHeader("↻", "Recurring", len(recurring), m.styles.Heading)
		writeTasks(recurring, m.styles.Warning, func(t storage.Task) string {
			s := "[" + recurrenceRuleLabel(t) + "]"
			if nextDate, ok := nextRecurrenceDate(t); ok {
				s += " next " + nextDate.Format("2006-01-02")
			}
			return s
		})
		b.WriteString("\n")
	}

	writeDivider()
	writeSectionHeader("＋", "Recently Added", len(recentAdd), m.styles.Heading)
	if len(recentAdd) == 0 {
		b.WriteString(m.styles.Muted.Render("  (none)") + "\n")
	} else {
		writeTasks(recentAdd, m.styles.Muted, func(t storage.Task) string { return "added " + t.CreatedAt.Format("2006-01-02") })
	}
	b.WriteString("\n")
	writeSectionHeader("✓", "Recently Done", len(recentDone), m.styles.Heading)
	if len(recentDone) == 0 {
		b.WriteString(m.styles.Muted.Render("  (none)") + "\n")
	} else {
		writeTasks(recentDone, m.styles.Done, func(t storage.Task) string {
			if t.CompletedAt.Valid {
				return "done " + t.CompletedAt.Time.Format("2006-01-02")
			}
			return "done"
		})
	}
	m.report = b.String()
	m.status = "Reminder report"
	m.reportScroll = clampInt(m.reportScroll, 0, m.reportMaxScroll())
}

func (m Model) renderReportHeader() string {
	now := time.Now()
	var b strings.Builder
	b.WriteString(m.renderListBanner())
	b.WriteString("\n\n")
	b.WriteString(m.styles.Heading.Render(fmt.Sprintf("  %s, it's %s", greetingForTime(now), now.Format("Monday, Jan 2"))))
	b.WriteString("\n\n")
	b.WriteString(m.renderAgendaFortune(now))
	b.WriteString("\n\n\n")
	return b.String()
}

func (m Model) renderAgendaFortune(now time.Time) string {
	f := dailyIChingFortune(now)
	inner := m.panelInnerWidth()
	wrapW := inner - 4
	if wrapW < 24 {
		wrapW = inner
	}

	label := m.styles.Accent.Bold(true).Render("  Daily Lesson")
	ruleW := inner - lipgloss.Width(label) - 1
	if ruleW < 1 {
		ruleW = 1
	}
	lesson := ichingDailyLesson(f)

	lines := []string{label + m.styles.Border.Render(" "+strings.Repeat("─", ruleW))}
	lessonStyle := m.styles.Muted.Italic(true)
	for _, line := range strings.Split(wrapText(lesson, wrapW), "\n") {
		lines = append(lines, m.styles.Accent.Render("  ▌ ")+lessonStyle.Render(line))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderReportFooter() string {
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "select"},
		{m.cfg.Keys.Confirm, "notes"},
		{m.cfg.Keys.Toggle, "status"},
		{m.cfg.Keys.Edit, "edit"},
		{m.cfg.Keys.DueBack + "/" + m.cfg.Keys.DueForward, "shift due"},
		{"g", "jump"},
		{m.cfg.Keys.Cancel + "/" + m.cfg.Keys.Quit, "back"},
	})
}

func (m Model) reportLines() []string {
	return strings.Split(strings.TrimRight(m.report, "\n"), "\n")
}

func (m Model) reportMaxScroll() int {
	if m.height <= 0 {
		return 0
	}
	header := m.renderReportHeader()
	footer := m.renderReportFooter()
	gap := "\n"
	bodyMax := m.height - 1 - countLines(header) - countLines(footer) - countLines(gap)
	if bodyMax <= 0 {
		return 0
	}
	lines := m.reportLines()
	if len(lines) <= bodyMax {
		return 0
	}
	return len(lines) - bodyMax
}

func (m Model) renderReportWithHeight(maxLines int) string {
	lines := m.reportLines()
	if maxLines <= 0 {
		return ""
	}
	if len(lines) == 0 {
		return ""
	}
	maxScroll := len(lines) - maxLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := clampInt(m.reportScroll, 0, maxScroll)
	end := scroll + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	// Pad to the full body height so the footer ("Press …") stays pinned to the
	// bottom instead of floating up under a short agenda.
	visible := make([]string, 0, maxLines)
	visible = append(visible, lines[scroll:end]...)
	for len(visible) < maxLines {
		visible = append(visible, "")
	}
	return strings.Join(visible, "\n")
}

func agendaDayLabel(day, today time.Time) string {
	days := int(normalizeDate(day).Sub(normalizeDate(today)).Hours() / 24)
	switch days {
	case 1:
		return "Tomorrow · " + day.Format("Mon, Jan 2")
	case 2:
		return "In 2 days · " + day.Format("Mon, Jan 2")
	default:
		return day.Format("Monday · Jan 2")
	}
}

// greetingForTime returns a time-of-day greeting for the agenda header.
func greetingForTime(t time.Time) string {
	switch h := t.Hour(); {
	case h < 12:
		return "Good morning"
	case h < 18:
		return "Good afternoon"
	default:
		return "Good evening"
	}
}
