package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"bada/internal/storage"
)

// enterBoardView opens the kanban board for a project (topic). The topic is, in
// order of preference: the explicit argument, the currently scoped topic, or the
// primary topic of the selected task. The project must have a custom workflow.
func (m Model) enterBoardView(topic string) (tea.Model, tea.Cmd) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		topic = m.scopedTopicName()
	}
	if topic == "" {
		if t, ok := m.currentTask(); ok {
			topic = strings.TrimSpace(t.PrimaryTopic)
		}
	}
	// Fall back to the busiest project that actually has a workflow, so a bare
	// :board "just works" in the common single-project case.
	if topic == "" || len(m.workflows[topic]) == 0 {
		if best := m.busiestWorkflowTopic(); best != "" {
			topic = best
		}
	}
	if topic == "" {
		m.status = "No project has a workflow yet — open :projects and press w to define one"
		return m, nil
	}
	if len(m.workflows[topic]) == 0 {
		m.status = fmt.Sprintf("%q has no workflow — define one with :projects → w", topic)
		return m, nil
	}
	m.boardTopic = topic
	m.boardCol = 0
	m.boardRow = 0
	m.mode = modeBoard
	m.status = "Kanban · " + topic
	if _, cols, ok := m.boardColumns(); ok {
		total := 0
		for _, c := range cols {
			total += len(c)
		}
		if total == 0 {
			m.status = "Kanban · " + topic + " — no tasks yet (make this the 1st topic on a task)"
		}
	}
	return m, nil
}

// busiestWorkflowTopic returns the workflow-bearing topic that governs the most
// tasks (by primary topic), or "" if no workflow exists. Ties break by name for
// determinism.
func (m Model) busiestWorkflowTopic() string {
	if len(m.workflows) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, t := range m.tasks {
		pt := strings.TrimSpace(t.PrimaryTopic)
		if pt != "" && len(m.workflows[pt]) > 0 {
			counts[pt]++
		}
	}
	best := ""
	for topic := range m.workflows {
		if len(m.workflows[topic]) == 0 {
			continue
		}
		if best == "" || counts[topic] > counts[best] || (counts[topic] == counts[best] && topic < best) {
			best = topic
		}
	}
	return best
}

// boardColumns returns the workflow stages and the tasks bucketed into each,
// for tasks whose primary topic is the board's project.
func (m Model) boardColumns() ([]storage.Stage, [][]storage.Task, bool) {
	stages := m.workflows[m.boardTopic]
	if len(stages) == 0 {
		return nil, nil, false
	}
	cols := make([][]storage.Task, len(stages))
	for _, t := range m.tasks {
		if strings.TrimSpace(t.PrimaryTopic) != m.boardTopic {
			continue
		}
		idx := currentStageIndex(stages, t.Status)
		cols[idx] = append(cols[idx], t)
	}
	return stages, cols, true
}

func (m Model) boardSelectedTask() (storage.Task, bool) {
	_, cols, ok := m.boardColumns()
	if !ok || m.boardCol < 0 || m.boardCol >= len(cols) {
		return storage.Task{}, false
	}
	col := cols[m.boardCol]
	if m.boardRow < 0 || m.boardRow >= len(col) {
		return storage.Task{}, false
	}
	return col[m.boardRow], true
}

func (m Model) updateBoardMode(key string) (tea.Model, tea.Cmd) {
	stages, cols, ok := m.boardColumns()
	if !ok {
		m.mode = modeList
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Kanban closed"
		return m, nil
	case "left", "h":
		if m.boardCol > 0 {
			m.boardCol--
			m.boardRow = clampInt(m.boardRow, 0, len(cols[m.boardCol])-1)
		}
	case "right", "l":
		if m.boardCol < len(cols)-1 {
			m.boardCol++
			m.boardRow = clampInt(m.boardRow, 0, len(cols[m.boardCol])-1)
		}
	case m.cfg.Keys.Up, "up", "k":
		if m.boardRow > 0 {
			m.boardRow--
		}
	case m.cfg.Keys.Down, "down", "j":
		if m.boardRow < len(cols[m.boardCol])-1 {
			m.boardRow++
		}
	case m.cfg.Keys.Confirm, "enter", m.cfg.Keys.Detail, m.cfg.Keys.NoteView:
		if t, ok := m.boardSelectedTask(); ok {
			return m.startNoteViewForTask(t)
		}
		return m, nil
	case "L", m.cfg.Keys.Toggle: // advance task to the next stage
		return m.moveBoardTask(stages, 1)
	case "H": // send task back a stage
		return m.moveBoardTask(stages, -1)
	default:
		return m, nil
	}
	return m, nil
}

// moveBoardTask re-stages the selected task by dir (+1 next, -1 previous) and
// keeps the cursor following it into its new column.
func (m Model) moveBoardTask(stages []storage.Stage, dir int) (tea.Model, tea.Cmd) {
	task, ok := m.boardSelectedTask()
	if !ok {
		return m, nil
	}
	cur := currentStageIndex(stages, task.Status)
	next := clampInt(cur+dir, 0, len(stages)-1)
	if next == cur {
		return m, nil
	}
	if err := m.setTaskStatusTo(task, stages[next].Name); err != nil {
		m.status = fmt.Sprintf("move failed: %v", err)
		return m, nil
	}
	m.snapshotUndo(task, "stage move")
	// Follow the task into its new column.
	m.boardCol = next
	if _, cols, ok := m.boardColumns(); ok {
		m.boardRow = 0
		for i, t := range cols[next] {
			if t.ID == task.ID {
				m.boardRow = i
				break
			}
		}
	}
	m.status = fmt.Sprintf("#%d → %s", task.ID, stages[next].Name)
	return m, nil
}

// startNoteViewForTask opens the task detail + notes view for a specific task
// (the board cursor's task, not the list cursor's), returning to the board on
// close.
func (m Model) startNoteViewForTask(t storage.Task) (tea.Model, tea.Cmd) {
	m.note = &noteState{
		target: noteTarget{kind: noteTask, taskID: t.ID, title: t.Title},
		body:   t.Notes,
	}
	m.noteScroll = 0
	m.noteReturnMode = m.mode // back to the board when the note closes
	m.mode = modeNote
	m.status = fmt.Sprintf("Notes: %s", m.note.target.label())
	return m, nil
}

// setTaskStatusTo sets a task's status to a specific stage, deriving the done
// flag from that stage's category, then reloads.
func (m *Model) setTaskStatusTo(t storage.Task, stageName string) error {
	done := m.statusMeansDone(t, stageName)
	if err := m.store.SetStatus(t.ID, stageName, done); err != nil {
		return err
	}
	return m.reload()
}

func (m Model) renderBoardView() string {
	footer := m.hintBar([]keyHint{
		{"h/l", "column"},
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "task"},
		{"enter", "detail"},
		{"L/H", "advance/back"},
		{m.cfg.Keys.Cancel, "close"},
	})
	return m.panel("bada ∙ Kanban ∙ "+m.boardTopic, m.boardContent()) + "\n" + footer
}

func (m Model) boardContent() string {
	stages, cols, ok := m.boardColumns()
	if !ok {
		return m.styles.Muted.Render("  No workflow for this project.")
	}
	inner := m.panelInnerWidth()
	n := len(stages)
	gap := 1
	colW := (inner - (n-1)*gap) / n
	if colW < 12 {
		colW = 12
	}

	// How many card rows fit, so tall columns don't overflow the screen.
	maxCardRows := 0 // 0 = unbounded (e.g. in tests with no height)
	if m.height > 0 {
		body := m.height - 1 /*status bar*/ - 2 /*panel borders*/ - 1 /*footer*/
		maxCardRows = body - 3                                        /*summary + blank + header*/
		if maxCardRows < 1 {
			maxCardRows = 1
		}
	}

	var out strings.Builder
	out.WriteString(m.boardSummary(stages, cols))
	out.WriteString("\n\n")

	blocks := make([]string, n)
	for ci, stage := range stages {
		blocks[ci] = m.boardColumnBlock(ci, stage, cols[ci], colW, maxCardRows)
	}
	out.WriteString(joinColumns(blocks, strings.Repeat(" ", gap)))
	return out.String()
}

// boardSummary is the one-line header above the columns: task/done/overdue tally.
func (m Model) boardSummary(stages []storage.Stage, cols [][]storage.Task) string {
	total, done, overdue := 0, 0, 0
	for ci, col := range cols {
		isDoneCol := stages[ci].Category == storage.StageDone
		for _, t := range col {
			total++
			if isDoneCol {
				done++
			}
			if isOverdue(t) {
				overdue++
			}
		}
	}
	parts := []string{
		m.styles.Muted.Render(fmt.Sprintf("%d tasks", total)),
		m.styles.Success.Render(fmt.Sprintf("%d done", done)),
	}
	if overdue > 0 {
		parts = append(parts, m.styles.Danger.Render(fmt.Sprintf("⚠ %d overdue", overdue)))
	}
	return "  " + strings.Join(parts, m.styles.Muted.Render("  ·  "))
}

// boardColumnBlock renders one stage column: a filled header bar plus its cards,
// windowed to maxCardRows (keeping the cursor visible in the selected column).
func (m Model) boardColumnBlock(ci int, stage storage.Stage, tasks []storage.Task, colW, maxCardRows int) string {
	var b strings.Builder

	headText := truncateText(fmt.Sprintf(" %s", stage.Name), colW-3) // leave room for count
	count := fmt.Sprintf("%d ", len(tasks))
	pad := colW - lipgloss.Width(headText) - lipgloss.Width(count)
	if pad < 1 {
		pad = 1
	}
	header := headText + strings.Repeat(" ", pad) + count
	b.WriteString(m.boardHeaderStyle(stage.Category).Width(colW).MaxWidth(colW).Render(header))
	b.WriteString("\n")

	if len(tasks) == 0 {
		b.WriteString(padRightWidth(m.styles.Muted.Render("  (empty)"), colW))
		b.WriteString("\n")
		return b.String()
	}

	start, end, more := 0, len(tasks), 0
	if maxCardRows > 0 && len(tasks) > maxCardRows {
		visible := maxCardRows - 1 // reserve a row for the "+N more" marker
		if visible < 1 {
			visible = 1
		}
		cursor := 0
		if ci == m.boardCol {
			cursor = m.boardRow
		}
		start = clampInt(cursor-visible+1, 0, len(tasks)-visible)
		end = start + visible
		more = len(tasks) - end
	}
	for ri := start; ri < end; ri++ {
		b.WriteString(m.boardCard(tasks[ri], colW, ci == m.boardCol && ri == m.boardRow))
		b.WriteString("\n")
	}
	if more > 0 {
		b.WriteString(padRightWidth(m.styles.Muted.Render(fmt.Sprintf("  +%d more", more)), colW))
		b.WriteString("\n")
	}
	return b.String()
}

// boardHeaderStyle colors a column header bar by its stage category: grey for
// pending, amber for active, green for done.
func (m Model) boardHeaderStyle(category string) lipgloss.Style {
	switch category {
	case storage.StageDone:
		return m.styles.StatusDone.Bold(true)
	case storage.StagePending:
		return m.styles.Status.Bold(true)
	default:
		return m.styles.StatusProgress.Bold(true)
	}
}

// boardCard renders a single task card padded to colW: priority flag, title, and
// a relative due tag (red when overdue). The selected card fills with the
// selection color.
func (m Model) boardCard(t storage.Task, colW int, selected bool) string {
	flag := "⚐"
	if t.Priority > 0 {
		flag = "⚑"
	}
	due := ""
	if t.Due.Valid {
		due = relativeDueCell(t.Due)
	}
	// Width budget: leading space + flag + space + title + gap + due.
	avail := colW - 3
	dueW := lipgloss.Width(due)
	if due != "" && avail-dueW-1 >= 4 {
		avail -= dueW + 1
	} else {
		due, dueW = "", 0
	}
	title := truncateText(strings.TrimSpace(t.Title), avail)
	gap := colW - 3 - lipgloss.Width(title) - dueW
	if gap < 1 {
		gap = 1
	}

	if selected {
		plain := fmt.Sprintf(" %s %s%s%s", flag, title, strings.Repeat(" ", gap), due)
		return m.styles.Selection.Width(colW).MaxWidth(colW).Render(plain)
	}
	flagC := m.styles.Muted.Render(flag)
	if t.Priority > 0 {
		flagC = m.priorityStyle(t.Priority).Render(flag)
	}
	dueC := ""
	if due != "" {
		if isOverdue(t) {
			dueC = m.styles.Danger.Render(due)
		} else {
			dueC = m.styles.Muted.Render(due)
		}
	}
	line := " " + flagC + " " + title + strings.Repeat(" ", gap) + dueC
	return padRightWidth(line, colW)
}

// joinColumns lays multi-line column blocks side by side.
func joinColumns(blocks []string, sep string) string {
	cols := make([]string, len(blocks))
	copy(cols, blocks)
	return lipgloss.JoinHorizontal(lipgloss.Top, interleave(cols, sep)...)
}

// interleave inserts sep between each column block.
func interleave(cols []string, sep string) []string {
	if len(cols) == 0 {
		return cols
	}
	out := make([]string, 0, len(cols)*2-1)
	for i, c := range cols {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, c)
	}
	return out
}
