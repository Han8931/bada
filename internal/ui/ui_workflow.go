package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/storage"
)

// ---------------------------------------------------------------------------
// Project dashboard (modeDashboard)
// ---------------------------------------------------------------------------

func (m Model) enterDashboardView() (tea.Model, tea.Cmd) {
	m.mode = modeDashboard
	m.dashboardScroll = 0
	m.dashEditing = ""
	topics := m.sortedTopics()
	m.dashboardCursor = clampCursor(m.dashboardCursor, len(topics))
	m.status = "Projects"
	return m, nil
}

// dashboardCurrentTopic returns the topic under the dashboard cursor.
func (m Model) dashboardCurrentTopic() (string, bool) {
	topics := m.sortedTopics()
	if len(topics) == 0 || m.dashboardCursor < 0 || m.dashboardCursor >= len(topics) {
		return "", false
	}
	return topics[m.dashboardCursor], true
}

func (m Model) updateDashboardMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While typing a metadata value, route keys to the text input.
	if m.dashEditing != "" {
		return m.updateDashboardEditing(key, msg)
	}
	topics := m.sortedTopics()
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Dashboard closed"
		return m, nil
	case m.cfg.Keys.Up, "up", "k":
		if m.dashboardCursor > 0 {
			m.dashboardCursor--
		}
	case m.cfg.Keys.Down, "down", "j":
		m.dashboardCursor = clampCursor(m.dashboardCursor+1, len(topics))
	case m.cfg.Keys.Confirm, "enter":
		if topic, ok := m.dashboardCurrentTopic(); ok {
			m.currentTopic = topic
			m.mode = modeList
			m.cursor = clampCursor(0, len(m.visibleItems()))
			m.status = "Scoped to " + topic
		}
		return m, nil
	case "w":
		if topic, ok := m.dashboardCurrentTopic(); ok {
			return m.openWorkflowEditor(topic, modeDashboard)
		}
	case "a":
		return m.toggleDashboardArchived()
	case "e":
		return m.startDashboardEdit("desc")
	case "t":
		return m.startDashboardEdit("target")
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) toggleDashboardArchived() (tea.Model, tea.Cmd) {
	topic, ok := m.dashboardCurrentTopic()
	if !ok {
		return m, nil
	}
	meta := m.topicMeta[topic]
	target := meta.TargetDate
	if err := m.store.UpdateTopicMeta(topic, meta.Description, target, !meta.Archived); err != nil {
		m.status = fmt.Sprintf("update failed: %v", err)
		return m, nil
	}
	if tm, err := m.store.AllTopicMeta(); err == nil {
		m.topicMeta = tm
	}
	if m.topicMeta[topic].Archived {
		m.status = topic + " archived"
	} else {
		m.status = topic + " unarchived"
	}
	return m, nil
}

func (m Model) startDashboardEdit(field string) (tea.Model, tea.Cmd) {
	topic, ok := m.dashboardCurrentTopic()
	if !ok {
		return m, nil
	}
	meta := m.topicMeta[topic]
	m.dashEditing = field
	switch field {
	case "desc":
		m.input.SetValue(meta.Description)
		m.status = "Edit description for " + topic + " (enter to save, esc to cancel)"
	case "target":
		if meta.TargetDate.Valid {
			m.input.SetValue(meta.TargetDate.Time.Format("2006-01-02"))
		} else {
			m.input.SetValue("")
		}
		m.status = "Target date YYYY-MM-DD for " + topic + " (blank to clear)"
	}
	m.input.Focus()
	return m, nil
}

func (m Model) updateDashboardEditing(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.dashEditing = ""
		m.input.SetValue("")
		m.input.Blur()
		m.status = "Cancelled"
		return m, nil
	case "enter":
		topic, ok := m.dashboardCurrentTopic()
		if !ok {
			m.dashEditing = ""
			return m, nil
		}
		meta := m.topicMeta[topic]
		val := strings.TrimSpace(m.input.Value())
		desc := meta.Description
		target := meta.TargetDate
		switch m.dashEditing {
		case "desc":
			desc = val
		case "target":
			parsed, err := parseDate(val)
			if err != nil {
				m.status = "Invalid date (use YYYY-MM-DD)"
				return m, nil
			}
			target = parsed
		}
		if err := m.store.UpdateTopicMeta(topic, desc, target, meta.Archived); err != nil {
			m.status = fmt.Sprintf("save failed: %v", err)
			return m, nil
		}
		if tm, err := m.store.AllTopicMeta(); err == nil {
			m.topicMeta = tm
		}
		m.dashEditing = ""
		m.input.SetValue("")
		m.input.Blur()
		m.status = "Saved"
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m Model) renderDashboardView() string {
	footer := m.dashboardFooter()
	body := m.dashboardContent()
	return m.panel("bada ∙ Projects", body) + "\n" + footer
}

func (m Model) dashboardFooter() string {
	if m.dashEditing != "" {
		return m.hintBar([]keyHint{
			{"enter", "save"},
			{"esc", "cancel"},
		})
	}
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "move"},
		{"enter", "scope"},
		{"w", "workflow"},
		{"e", "desc"},
		{"t", "target"},
		{"a", "archive"},
		{m.cfg.Keys.Cancel, "close"},
	})
}

func (m Model) dashboardContent() string {
	topics := m.sortedTopics()
	if len(topics) == 0 {
		return m.styles.Muted.Render("  No topics yet. Add a topic to a task, then define its workflow here.")
	}
	stats := m.topicStats()
	var b strings.Builder
	b.WriteString(m.styles.Heading.Render(fmt.Sprintf("  Projects (%d)", len(topics))))
	b.WriteString("\n\n")
	for i, topic := range topics {
		st := stats[topic]
		meta := m.topicMeta[topic]
		cursor := "  "
		nameStyle := m.styles.Title
		if i == m.dashboardCursor {
			cursor = m.styles.Accent.Render("▌ ")
			nameStyle = m.styles.Selection
		}
		pct := 0
		if st.total > 0 {
			pct = st.done * 100 / st.total
		}
		name := nameStyle.Render(fmt.Sprintf("%-18s", truncateText(topic, 18)))
		line := fmt.Sprintf("%s%s %s  %d/%d (%d%%)", cursor, name, pctBar(st.done, st.total, 10), st.done, st.total, pct)
		if st.overdue > 0 {
			line += "  " + m.styles.Danger.Render(fmt.Sprintf("⚠%d", st.overdue))
		}
		if len(m.workflows[topic]) > 0 {
			line += "  " + m.styles.Muted.Render("⚙ workflow")
		}
		if meta.Archived {
			line += "  " + m.styles.Muted.Render("[archived]")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	// Detail panel for the selected topic.
	if topic, ok := m.dashboardCurrentTopic(); ok {
		b.WriteString("\n")
		b.WriteString(m.dashboardDetail(topic))
	}
	return b.String()
}

func (m Model) dashboardDetail(topic string) string {
	meta := m.topicMeta[topic]
	var b strings.Builder
	b.WriteString(m.styles.Heading.Render("  ── " + topic + " "))
	b.WriteString("\n")
	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		desc = m.styles.Muted.Render("(no description — press e to add)")
	}
	b.WriteString("  " + m.styles.Muted.Render("Description: ") + desc + "\n")
	target := m.styles.Muted.Render("(none)")
	if meta.TargetDate.Valid {
		target = meta.TargetDate.Time.Format("2006-01-02")
	}
	b.WriteString("  " + m.styles.Muted.Render("Target: ") + target + "\n")
	stages := m.workflows[topic]
	if len(stages) == 0 {
		b.WriteString("  " + m.styles.Muted.Render("Workflow: none — press w to define stages") + "\n")
		return b.String()
	}
	// Stage funnel: name + count for tasks whose primary topic is this topic.
	counts := m.topicStageStats(topic)
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		parts = append(parts, fmt.Sprintf("%s %s", m.stageBadge(c.Stage), m.styles.Accent.Render(fmt.Sprintf("%d", c.Count))))
	}
	b.WriteString("  " + m.styles.Muted.Render("Stages: ") + strings.Join(parts, m.styles.Muted.Render(" → ")) + "\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// Workflow editor (modeWorkflow)
// ---------------------------------------------------------------------------

func (m Model) openWorkflowEditor(topic string, returnMode mode) (tea.Model, tea.Cmd) {
	stages := append([]storage.Stage{}, m.workflows[topic]...)
	m.workflowEdit = &workflowEditState{
		topic:      topic,
		stages:     stages,
		cursor:     0,
		returnMode: returnMode,
	}
	m.mode = modeWorkflow
	m.status = "Workflow: " + topic
	return m, nil
}

func (m Model) updateWorkflowMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	w := m.workflowEdit
	if w == nil {
		m.mode = modeList
		return m, nil
	}
	if w.editing {
		return m.updateWorkflowEditing(key, msg)
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		// Save on close so edits aren't silently lost.
		if w.dirty {
			if err := m.saveWorkflow(); err != nil {
				m.status = fmt.Sprintf("save failed: %v", err)
				return m, nil
			}
		}
		ret := w.returnMode
		m.workflowEdit = nil
		m.mode = ret
		m.status = "Workflow saved"
		return m, nil
	case m.cfg.Keys.Up, "up", "k":
		if w.cursor > 0 {
			w.cursor--
		}
	case m.cfg.Keys.Down, "down", "j":
		if w.cursor < len(w.stages)-1 {
			w.cursor++
		}
	case m.cfg.Keys.Add, "a":
		w.editing = true
		w.adding = true
		m.input.SetValue("")
		m.input.Focus()
		m.status = "New stage name"
		return m, nil
	case m.cfg.Keys.Edit, "e", "enter":
		if len(w.stages) == 0 {
			return m, nil
		}
		w.editing = true
		w.adding = false
		m.input.SetValue(w.stages[w.cursor].Name)
		m.input.Focus()
		m.status = "Rename stage"
		return m, nil
	case m.cfg.Keys.Delete, "D":
		if len(w.stages) == 0 {
			return m, nil
		}
		w.stages = append(w.stages[:w.cursor], w.stages[w.cursor+1:]...)
		if w.cursor >= len(w.stages) && w.cursor > 0 {
			w.cursor--
		}
		w.dirty = true
		m.status = "Stage removed"
	case "c", " ":
		if len(w.stages) == 0 {
			return m, nil
		}
		w.stages[w.cursor].Category = nextStageCategory(w.stages[w.cursor].Category)
		w.dirty = true
		m.status = "Category: " + w.stages[w.cursor].Category
	case "J":
		if w.cursor < len(w.stages)-1 {
			w.stages[w.cursor], w.stages[w.cursor+1] = w.stages[w.cursor+1], w.stages[w.cursor]
			w.cursor++
			w.dirty = true
		}
	case "K":
		if w.cursor > 0 {
			w.stages[w.cursor], w.stages[w.cursor-1] = w.stages[w.cursor-1], w.stages[w.cursor]
			w.cursor--
			w.dirty = true
		}
	case "s":
		if err := m.saveWorkflow(); err != nil {
			m.status = fmt.Sprintf("save failed: %v", err)
			return m, nil
		}
		m.status = "Workflow saved"
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) updateWorkflowEditing(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	w := m.workflowEdit
	switch key {
	case "esc":
		w.editing = false
		w.adding = false
		m.input.SetValue("")
		m.input.Blur()
		m.status = "Cancelled"
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.input.Value())
		if name == "" {
			m.status = "Stage name cannot be empty"
			return m, nil
		}
		if w.adding {
			// New stages default to "active"; the last stage usually means done,
			// but the user marks that explicitly with c.
			w.stages = append(w.stages, storage.Stage{Name: name, Category: storage.StageActive})
			w.cursor = len(w.stages) - 1
		} else {
			w.stages[w.cursor].Name = name
		}
		w.dirty = true
		w.editing = false
		w.adding = false
		m.input.SetValue("")
		m.input.Blur()
		m.status = "Stage saved"
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m *Model) saveWorkflow() error {
	w := m.workflowEdit
	if w == nil {
		return nil
	}
	if err := m.store.SetTopicWorkflow(w.topic, w.stages); err != nil {
		return err
	}
	w.dirty = false
	// Refresh the in-memory workflow map and re-sort, since stage categories
	// affect ordering and labels everywhere.
	return m.reload()
}

func (m Model) renderWorkflowView() string {
	footer := m.workflowFooter()
	body := m.workflowContent()
	title := "bada ∙ Workflow"
	if m.workflowEdit != nil {
		title += " ∙ " + m.workflowEdit.topic
	}
	return m.panel(title, body) + "\n" + footer
}

func (m Model) workflowFooter() string {
	if m.workflowEdit != nil && m.workflowEdit.editing {
		return m.hintBar([]keyHint{{"enter", "save"}, {"esc", "cancel"}})
	}
	return m.hintBar([]keyHint{
		{"a", "add"},
		{"e", "rename"},
		{"c", "category"},
		{"J/K", "reorder"},
		{"D", "delete"},
		{"s", "save"},
		{m.cfg.Keys.Cancel, "save & close"},
	})
}

func (m Model) workflowContent() string {
	w := m.workflowEdit
	if w == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.styles.Muted.Render("  Define the ordered statuses for this project. Mark the final stage 'done'."))
	b.WriteString("\n\n")
	if len(w.stages) == 0 {
		b.WriteString(m.styles.Muted.Render("  (no stages yet — press a to add the first stage)"))
		b.WriteString("\n")
	}
	for i, st := range w.stages {
		cursor := "   "
		if i == w.cursor && !w.editing {
			cursor = m.styles.Accent.Render(" ▌ ")
		}
		name := st.Name
		if i == w.cursor && w.editing && !w.adding {
			name = m.input.View()
		}
		b.WriteString(fmt.Sprintf("%s%2d. %-20s %s\n", cursor, i+1, name, m.stageBadge(st)))
	}
	if w.editing && w.adding {
		b.WriteString("\n")
		b.WriteString("  " + m.styles.Accent.Render("new: ") + m.input.View() + "\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// stageBadge renders a stage's category as a small colored chip.
func (m Model) stageBadge(st storage.Stage) string {
	label := " " + st.Category + " "
	switch st.Category {
	case storage.StageDone:
		return m.styles.StatusDone.Render(label)
	case storage.StagePending:
		return m.styles.StatusPending.Render(strings.TrimSpace(label))
	default:
		return m.styles.StatusProgress.Render(label)
	}
}

// scopedTopicName returns the currently scoped topic when it's a real topic
// (not a special pseudo-topic like RecentlyAdded), else "".
func (m Model) scopedTopicName() string {
	if m.currentTopic == "" || isSpecialTopic(m.currentTopic) {
		return ""
	}
	return m.currentTopic
}

func nextStageCategory(cat string) string {
	switch cat {
	case storage.StagePending:
		return storage.StageActive
	case storage.StageActive:
		return storage.StageDone
	default:
		return storage.StagePending
	}
}

// pctBar renders a fixed-width completion bar (done/total).
func pctBar(done, total, width int) string {
	if width <= 0 {
		return ""
	}
	filled := 0
	if total > 0 {
		filled = done * width / total
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("▓", filled) + strings.Repeat("░", width-filled)
}
