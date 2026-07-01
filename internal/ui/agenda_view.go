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
	scope := m.scopedTopicName()

	var overdue, todayList, upcoming, recurring, noDate []storage.Task
	for _, t := range m.agendaTasks() {
		if isRecurringTask(t) && isActive(t) {
			recurring = append(recurring, t)
		}
		if isDone(t) {
			continue
		}
		if !t.Due.Valid {
			// Undated but prioritized tasks would otherwise be invisible on the
			// agenda; surface them as a gentle nudge (recurring shown separately).
			if t.Priority > 0 && !isRecurringTask(t) {
				noDate = append(noDate, t)
			}
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
	sortAgendaTasks(upcoming)
	sortAgendaTasks(recurring)
	sortAgendaTasks(noDate)
	// Due Today reads as a schedule: earliest first.
	sort.SliceStable(todayList, func(i, j int) bool {
		return todayList[i].Due.Time.Before(todayList[j].Due.Time)
	})
	recentAdd := recentlyAddedFrom(m.agendaTasks(), m.recentLimit)
	recentDone := recentlyDoneFrom(m.agendaTasks(), m.recentLimit)
	totalReportTasks := len(overdue) + len(todayList) + len(upcoming) + len(recurring) + len(noDate) + len(recentAdd) + len(recentDone)
	m.reportCursor = clampCursor(m.reportCursor, totalReportTasks)
	m.reportTaskIDs = nil

	// Title column scales with the terminal; the rest of the line (gutter, id,
	// priority flag, project/stage meta, and trailing date) is ~64 cells. "∙" is
	// one cell even in CJK/Termius, unlike "•".
	metaW := 16
	titleW := clampInt(m.width-64, 14, 44)
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
	// rowMeta is the muted secondary column: the task's project (hidden when the
	// agenda is already scoped to one) and its workflow stage, when governed.
	rowMeta := func(t storage.Task) string {
		var parts []string
		if scope == "" {
			if pt := primaryOrFirstTopic(t); pt != "" {
				parts = append(parts, pt)
			}
		}
		if stages, ok := m.governingWorkflow(t); ok {
			parts = append(parts, stages[currentStageIndex(stages, t.Status)].Name)
		}
		return strings.Join(parts, " · ")
	}
	// Rows carry a section-colored left gutter, a narrow task bullet, a muted id,
	// the priority flag, a plain (default-foreground) title for readability, a
	// muted project/stage column, and a section-colored trailing date/urgency. The
	// selected row fills with the selection color. The "#NNN" token must stay
	// intact for cursor-visibility lookups.
	writeTasks := func(tasks []storage.Task, style lipgloss.Style, trailing func(storage.Task) string) {
		for _, t := range tasks {
			idx := len(m.reportTaskIDs)
			m.reportTaskIDs = append(m.reportTaskIDs, t.ID)
			title := truncateTextWidth(t.Title, titleW)
			meta := truncateTextWidth(rowMeta(t), metaW)
			flagCh := "⚐"
			if t.Priority > 0 {
				flagCh = "⚑"
			}
			if idx == m.reportCursor {
				line := fmt.Sprintf(" ▌ ∙ #%-3d %s %-*s  %-*s  %s", t.ID, flagCh, titleW, title, metaW, meta, trailing(t))
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
			b.WriteString(m.styles.Muted.Render(fmt.Sprintf("%-*s", metaW, meta)))
			b.WriteString("  ")
			b.WriteString(style.Render(trailing(t)))
			b.WriteString("\n")
		}
	}
	dueTrail := func(t storage.Task) string { return localRelativeDue(t) + " · " + t.Due.Time.In(loc).Format("Jan 2") }
	todayTrail := func(t storage.Task) string {
		tm := t.Due.Time.In(loc)
		if tm.Hour() == 0 && tm.Minute() == 0 {
			return "all day"
		}
		return tm.Format("15:04")
	}

	// Friendly all-clear when nothing needs attention now.
	if len(overdue)+len(todayList)+len(upcoming) == 0 {
		b.WriteString("  " + m.styles.Success.Render("✓ All clear — nothing due right now"))
		b.WriteString("\n\n")
	}

	if len(overdue) > 0 {
		writeSectionHeader("⚠", "Overdue", len(overdue), m.styles.Danger)
		writeTasks(overdue, m.styles.Danger, dueTrail)
		b.WriteString("\n")
	}
	if len(todayList) > 0 {
		writeSectionHeader("◆", "Due Today", len(todayList), m.styles.Accent)
		writeTasks(todayList, m.styles.Accent, todayTrail)
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
	if len(noDate) > 0 {
		writeSectionHeader("◷", "No date", len(noDate), m.styles.Muted)
		writeTasks(noDate, m.styles.Muted, func(t storage.Task) string { return "no due date" })
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

// agendaTasks returns the tasks the agenda should consider — all of them, or
// just those in the scoped project when the list is scoped to a topic.
func (m Model) agendaTasks() []storage.Task {
	scope := m.scopedTopicName()
	if scope == "" {
		return m.tasks
	}
	out := make([]storage.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		if taskHasTopic(t, scope) {
			out = append(out, t)
		}
	}
	return out
}

// primaryOrFirstTopic is the task's project: its primary topic, else its first
// label topic.
func primaryOrFirstTopic(t storage.Task) string {
	if p := strings.TrimSpace(t.PrimaryTopic); p != "" {
		return p
	}
	if len(t.Topics) > 0 {
		return t.Topics[0]
	}
	return ""
}

func recentlyAddedFrom(tasks []storage.Task, limit int) []storage.Task {
	cp := append([]storage.Task{}, tasks...)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].CreatedAt.After(cp[j].CreatedAt) })
	if len(cp) > limit {
		cp = cp[:limit]
	}
	return cp
}

func recentlyDoneFrom(tasks []storage.Task, limit int) []storage.Task {
	var done []storage.Task
	for _, t := range tasks {
		if isDone(t) {
			done = append(done, t)
		}
	}
	sort.SliceStable(done, func(i, j int) bool {
		ai, aj := done[i].CompletedAt, done[j].CompletedAt
		if ai.Valid && aj.Valid {
			return ai.Time.After(aj.Time)
		}
		return ai.Valid
	})
	if len(done) > limit {
		done = done[:limit]
	}
	return done
}

func (m Model) renderReportHeader() string {
	now := time.Now()
	var b strings.Builder
	if !m.agendaHeaderFold {
		b.WriteString(m.renderListBanner())
		b.WriteString("\n\n")
	}

	// Greeting line, with the scoped project and a completion streak appended.
	greeting := fmt.Sprintf("  %s, it's %s", greetingForTime(now), now.Format("Monday, Jan 2"))
	line := m.styles.Heading.Render(greeting)
	if scope := m.scopedTopicName(); scope != "" {
		line += m.styles.Muted.Render("   ·   ") + m.styles.Accent.Render("Agenda · "+scope)
	}
	if streak := m.completionStreak(); streak > 1 {
		line += m.styles.Muted.Render("   ·   ") + m.styles.Warning.Render(fmt.Sprintf("🔥 %d-day streak", streak))
	}
	b.WriteString(line)
	b.WriteString("\n")

	// The lesson sits just under the greeting, with a blank line separating it
	// from the triage summary below.
	if !m.agendaHeaderFold {
		b.WriteString(m.renderAgendaFortune(now))
		b.WriteString("\n\n")
	}

	// At-a-glance triage summary + 7-day sparkline.
	b.WriteString(m.renderAgendaSummary())
	b.WriteString("\n")
	b.WriteString(m.renderAgendaWeek(now))
	b.WriteString("\n\n")
	return b.String()
}

// renderAgendaSummary is the color-coded at-a-glance line under the greeting.
func (m Model) renderAgendaSummary() string {
	o, today, up, doneToday := m.agendaCounts()
	var parts []string
	if o > 0 {
		parts = append(parts, m.styles.Danger.Render(fmt.Sprintf("⚠ %d overdue", o)))
	}
	if today > 0 {
		parts = append(parts, m.styles.Accent.Render(fmt.Sprintf("◆ %d due today", today)))
	}
	if up > 0 {
		parts = append(parts, m.styles.Warning.Render(fmt.Sprintf("▸ %d upcoming", up)))
	}
	if len(parts) == 0 {
		parts = append(parts, m.styles.Success.Render("✓ all clear"))
	}
	if doneToday > 0 {
		parts = append(parts, m.styles.Success.Render(fmt.Sprintf("✓ %d done today", doneToday)))
	}
	return "  " + strings.Join(parts, m.styles.Muted.Render("   ∙   "))
}

// agendaCounts tallies the scoped tasks for the summary strip.
func (m Model) agendaCounts() (overdue, today, upcoming, doneToday int) {
	now := time.Now()
	loc := now.Location()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	tomorrow := start.Add(24 * time.Hour)
	upcomingDays := m.cfg.Agenda.UpcomingDays
	if upcomingDays <= 0 {
		upcomingDays = 3
	}
	soon := start.AddDate(0, 0, upcomingDays)
	for _, t := range m.agendaTasks() {
		if t.CompletedAt.Valid {
			c := t.CompletedAt.Time.In(loc)
			if !c.Before(start) && c.Before(tomorrow) {
				doneToday++
			}
		}
		if isDone(t) || !t.Due.Valid {
			continue
		}
		d := t.Due.Time.In(loc)
		switch {
		case d.Before(start):
			overdue++
		case d.Before(tomorrow):
			today++
		case d.Before(soon):
			upcoming++
		}
	}
	return
}

// renderAgendaWeek draws a 7-day due-count sparkline starting today.
func (m Model) renderAgendaWeek(now time.Time) string {
	loc := now.Location()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	var counts [7]int
	for _, t := range m.agendaTasks() {
		if isDone(t) || !t.Due.Valid {
			continue
		}
		d := normalizeDate(t.Due.Time.In(loc))
		off := int(d.Sub(start).Hours() / 24)
		if off >= 0 && off < 7 {
			counts[off]++
		}
	}
	max := 0
	for _, c := range counts {
		if c > max {
			max = c
		}
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	b.WriteString(m.styles.Muted.Render("  Next 7 days "))
	for i := 0; i < 7; i++ {
		day := start.AddDate(0, 0, i)
		label := day.Format("Mon")[:1]
		glyph := "·"
		if counts[i] > 0 {
			lvl := 0
			if max > 0 {
				lvl = (counts[i] - 1) * (len(bars) - 1) / max
			}
			glyph = string(bars[lvl])
		}
		style := m.styles.Muted
		if i == 0 {
			style = m.styles.Accent // today
		}
		b.WriteString(" " + style.Render(label) + m.styles.Heading.Render(glyph))
	}
	return b.String()
}

// completionStreak counts consecutive days (ending today, or yesterday if
// nothing's done yet today) with at least one completed task.
func (m Model) completionStreak() int {
	loc := time.Now().Location()
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	set := map[string]bool{}
	for _, t := range m.tasks {
		if t.CompletedAt.Valid {
			c := t.CompletedAt.Time.In(loc)
			set[time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, loc).Format("2006-01-02")] = true
		}
	}
	streak := 0
	cur := today
	if !set[cur.Format("2006-01-02")] {
		cur = today.AddDate(0, 0, -1)
	}
	for set[cur.Format("2006-01-02")] {
		streak++
		cur = cur.AddDate(0, 0, -1)
	}
	return streak
}

// renderAgendaFortune shows just the day's lesson — no label, no header — as a
// clear (normal-foreground) italic line with a subtle accent gutter.
func (m Model) renderAgendaFortune(now time.Time) string {
	inner := m.panelInnerWidth()
	wrapW := inner - 6
	if wrapW < 24 {
		wrapW = inner
	}
	style := lipgloss.NewStyle().Italic(true)
	var lines []string
	for _, line := range strings.Split(wrapText(ichingDailyLesson(dailyIChingFortune(now)), wrapW), "\n") {
		lines = append(lines, m.styles.Accent.Render("  ▌ ")+style.Render(line))
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
		{"z", "fold"},
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
