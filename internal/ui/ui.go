package ui

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/config"
	"bada/internal/storage"
)

type mode int

const (
	modeList mode = iota
	modeAdd
	modeMetadata
	modeRename
	modeCommand
)

type metaState struct {
	taskID    int
	title     string
	project   string
	tags      string
	priority  string
	due       string
	start     string
	recurring string
	index     int
}

type Model struct {
	store      *storage.Store
	cfg        config.Config
	tasks      []storage.Task
	cursor     int
	mode       mode
	input      textinput.Model
	status     string
	filterDone string
	sortMode   string
	sortBuf    string
	confirmDel bool
	pendingDel *storage.Task
	meta       *metaState
	renameID   int
}

func Run(store *storage.Store, cfg config.Config) error {
	tasks, err := store.FetchTasks()
	if err != nil {
		return err
	}

	ti := textinput.New()
	ti.Placeholder = "Task title"
	ti.CharLimit = 256
	ti.Width = 40
	ti.Prompt = ""

	m := Model{
		store:      store,
		cfg:        cfg,
		tasks:      tasks,
		cursor:     clampCursor(0, len(tasks)),
		status:     "",
		input:      ti,
		mode:       modeList,
		filterDone: strings.ToLower(cfg.DefaultFilter),
		sortMode:   "created",
	}
	m.sortTasks()

	program := tea.NewProgram(m)
	_, err = program.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.meta != nil {
			return m.updateMetadataMode(msg.String(), msg)
		}
		if m.mode == modeRename {
			return m.updateRenameMode(msg.String(), msg)
		}
		if m.mode == modeCommand {
			return m.updateCommandMode(msg.String(), msg)
		}
		if m.confirmDel {
			return m.updateDeleteConfirm(msg.String())
		}
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.input.Width = msg.Width - 10
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.mode == modeAdd {
		return m.updateAddMode(key, msg)
	}
	if m.mode == modeRename {
		return m.updateRenameMode(key, msg)
	}
	if m.mode == modeCommand {
		return m.updateCommandMode(key, msg)
	}
	if m.processSortKey(key) {
		return m, nil
	}
	return m.updateListMode(key)
}

func (m Model) updateAddMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel:
		m.mode = modeList
		m.input.SetValue("")
		m.status = "Cancelled"
		return m, nil
	case m.cfg.Keys.Confirm:
		title := strings.TrimSpace(m.input.Value())
		if title == "" {
			m.status = "Title cannot be empty"
			return m, nil
		}
		if err := m.store.AddTask(title); err != nil {
			m.status = fmt.Sprintf("save failed: %v", err)
			return m, nil
		}
		var err error
		m.tasks, err = m.store.FetchTasks()
		if err != nil {
			m.status = fmt.Sprintf("reload failed: %v", err)
		} else {
			m.status = "Added task"
			m.sortTasks()
			m.cursor = clampCursor(m.findTaskIndex(m.lastTaskID()), len(m.tasks))
		}
		m.input.SetValue("")
		m.input.Blur()
		m.mode = modeList
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m Model) updateListMode(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c", m.cfg.Keys.Quit:
		return m, tea.Quit
	case ":":
		return m.startCommand()
	case m.cfg.Keys.Down:
		if len(m.tasks) == 0 {
			return m, nil
		}
		m.cursor = clampCursor(m.cursor+1, len(m.tasks))
	case "down":
		if len(m.tasks) == 0 {
			return m, nil
		}
		m.cursor = clampCursor(m.cursor+1, len(m.tasks))
	case m.cfg.Keys.Up:
		if m.cursor > 0 {
			m.cursor = clampCursor(m.cursor-1, len(m.tasks))
		}
	case "up":
		if m.cursor > 0 {
			m.cursor = clampCursor(m.cursor-1, len(m.tasks))
		}
	case m.cfg.Keys.Add:
		m.mode = modeAdd
		m.input.Focus()
		m.status = "Add mode: type a title and press Enter"
	case m.cfg.Keys.Toggle:
		if len(m.tasks) == 0 {
			return m, nil
		}
		task := m.tasks[m.cursor]
		err := m.store.SetDone(task.ID, !task.Done)
		if err != nil {
			m.status = fmt.Sprintf("toggle failed: %v", err)
			return m, nil
		}
		m.tasks, err = m.store.FetchTasks()
		if err == nil {
			m.cursor = clampCursor(m.cursor+1, len(m.tasks))
			m.status = "Toggled task"
		} else {
			m.status = fmt.Sprintf("reload failed: %v", err)
		}
	case m.cfg.Keys.Delete:
		if len(m.tasks) == 0 {
			return m, nil
		}
		t := m.tasks[m.cursor]
		m.confirmDel = true
		m.pendingDel = &t
		m.status = fmt.Sprintf("Delete \"%s\"? y/n", t.Title)
	case m.cfg.Keys.DeleteAllDone:
		m.confirmDel = true
		m.pendingDel = nil
		m.status = "Delete ALL done tasks? y/n"
	case m.cfg.Keys.Rename:
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.startRename(m.tasks[m.cursor])
	case "r":
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.startRename(m.tasks[m.cursor])
	case m.cfg.Keys.PriorityUp, "+":
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.bumpPriority(1)
	case m.cfg.Keys.PriorityDown, "-":
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.bumpPriority(-1)
	case m.cfg.Keys.DueForward:
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.shiftDue(1)
	case m.cfg.Keys.DueBack:
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.shiftDue(-1)
	case "[":
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.shiftDue(-1)
	case "]":
		if len(m.tasks) == 0 {
			return m, nil
		}
		return m.shiftDue(1)
	case m.cfg.Keys.Detail:
		if len(m.tasks) == 0 {
			m.status = "No tasks"
			return m, nil
		}
		task := m.tasks[m.cursor]
		info := fmt.Sprintf("Task #%d • %s • %s", task.ID, task.Title, humanDone(task.Done))
		if task.Project != "" {
			info += " • topic:" + task.Project
		}
		if strings.TrimSpace(task.Tags) != "" {
			info += " • tags:" + task.Tags
		}
		if task.Priority != 0 {
			info += fmt.Sprintf(" • priority:%d", task.Priority)
		}
		if task.Due.Valid {
			info += " • due:" + task.Due.Time.Format("2006-01-02")
		}
		if task.Start.Valid {
			info += " • start:" + task.Start.Time.Format("2006-01-02")
		}
		if task.Recurring {
			info += " • recurring"
		}
		m.status = info
	case m.cfg.Keys.Edit:
		if len(m.tasks) == 0 {
			m.status = "No tasks to edit"
			return m, nil
		}
		return m.startMetadataEdit(m.tasks[m.cursor])
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString("Todo (Bubble Tea + SQLite)")
	b.WriteString("\n\n")

	if len(m.tasks) == 0 {
		b.WriteString("No tasks yet.")
	} else {
		b.WriteString(m.renderTaskList())
	}

	b.WriteString("\n---\n")

	if m.meta != nil {
		b.WriteString("Metadata editor (tab/shift+tab or h/j/k/l to move, enter to save/next, esc to cancel)")
		b.WriteString("\n\n")
		b.WriteString(m.renderMetaBox())
		b.WriteString("\n")
		b.WriteString("Field: " + m.currentMetaLabel())
		b.WriteString("\n")
		b.WriteString(m.input.View())
	} else if m.mode == modeAdd {
		b.WriteString("Add task: ")
		b.WriteString(m.input.View())
	} else if m.mode == modeRename {
		b.WriteString("Rename task: Enter to save, Esc to cancel")
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("Current: %s\n", m.currentTaskTitle()))
		b.WriteString("New: ")
		b.WriteString(m.input.View())
	} else if m.mode == modeCommand {
		b.WriteString(":")
		b.WriteString(m.input.View())
	} else {
		b.WriteString(m.renderMetadataPanel())
	}

	b.WriteString("\n\n")
	b.WriteString(m.renderStatusBar())
	return b.String()
}

func (m Model) updateDeleteConfirm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "n", "N", "esc":
		m.status = "Delete cancelled"
		m.confirmDel = false
		m.pendingDel = nil
		return m, nil
	case "y", "Y":
		if m.pendingDel == nil {
			// delete all done
			n, err := m.store.DeleteDoneTasks()
			if err != nil {
				m.status = fmt.Sprintf("delete failed: %v", err)
				m.confirmDel = false
				return m, nil
			}
			var errReload error
			m.tasks, errReload = m.store.FetchTasks()
			if errReload == nil {
				m.sortTasks()
				m.cursor = clampCursor(m.cursor, len(m.tasks))
				m.status = fmt.Sprintf("Deleted %d done tasks", n)
			} else {
				m.status = fmt.Sprintf("reload failed: %v", errReload)
			}
			m.confirmDel = false
			return m, nil
		}
		if err := m.store.DeleteTask(m.pendingDel.ID); err != nil {
			m.status = fmt.Sprintf("delete failed: %v", err)
			m.confirmDel = false
			m.pendingDel = nil
			return m, nil
		}
		var err error
		m.tasks, err = m.store.FetchTasks()
		if err == nil {
			m.sortTasks()
			m.cursor = clampCursor(m.cursor, len(m.tasks))
			m.status = "Deleted task"
		} else {
			m.status = fmt.Sprintf("reload failed: %v", err)
		}
		m.confirmDel = false
		m.pendingDel = nil
		return m, nil
	default:
		return m, nil
	}
}

func renderHelp(k config.Keymap) string {
	return fmt.Sprintf("%s/%s move • %s add • %s/%s detail • %s toggle • %s delete • %s edit • %s rename • %s/%s prio • %s/%s due • %s/%s/%s sort • %s quit",
		k.Up, k.Down, k.Add, k.Detail, k.Confirm, k.Toggle, k.Delete, k.Edit, k.Rename, k.PriorityUp, k.PriorityDown, k.DueForward, k.DueBack, k.SortDue, k.SortPriority, k.SortCreated, k.Quit)
}

func (m Model) renderTaskList() string {
	var b strings.Builder
	for i, t := range m.tasks {
		cursor := " "
		if m.cursor == i && m.mode == modeList {
			cursor = ">"
		}

		checkbox := "[ ]"
		if t.Done {
			checkbox = "[x]"
		}

		body := fmt.Sprintf("%s %s %s", cursor, checkbox, t.Title)

		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) startMetadataEdit(t storage.Task) (tea.Model, tea.Cmd) {
	m.meta = &metaState{
		taskID:    t.ID,
		title:     t.Title,
		project:   t.Project,
		tags:      t.Tags,
		priority:  fmt.Sprintf("%d", t.Priority),
		due:       formatDate(t.Due),
		start:     formatDate(t.Start),
		recurring: boolToYN(t.Recurring),
		index:     0,
	}
	m.input.SetValue(m.meta.currentValue())
	m.input.Placeholder = m.meta.currentLabel()
	m.input.Focus()
	m.mode = modeMetadata
	m.status = "Edit metadata: tab/hjkl to move, enter to save/next, esc to cancel"
	return m, nil
}

func (m Model) updateMetadataMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel, "esc":
		if m.meta == nil {
			return m, nil
		}
		// Keep current field value, then save what we have so far.
		m.meta.setCurrentValue(m.input.Value())
		res, cmd := m.saveMetadata()
		if mm, ok := res.(Model); ok {
			mm.status = "Metadata saved"
			return mm, cmd
		}
		return res, cmd
	case "tab", "right", "l", "j", "down":
		if m.meta == nil {
			return m, nil
		}
		m.meta.setCurrentValue(m.input.Value())
		m.meta.index = wrapIndex(m.meta.index+1, len(metaFields()))
		m.input.SetValue(m.meta.currentValue())
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	case "shift+tab", "left", "h", "k", "up":
		if m.meta == nil {
			return m, nil
		}
		m.meta.setCurrentValue(m.input.Value())
		m.meta.index = wrapIndex(m.meta.index-1, len(metaFields()))
		m.input.SetValue(m.meta.currentValue())
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		if m.meta == nil {
			return m, nil
		}
		m.meta.setCurrentValue(m.input.Value())
		if m.meta.index >= len(metaFields())-1 {
			return m.saveMetadata()
		}
		m.meta.index++
		m.input.SetValue(m.meta.currentValue())
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m Model) saveMetadata() (tea.Model, tea.Cmd) {
	if m.meta == nil {
		return m, nil
	}
	taskID := m.meta.taskID
	title := strings.TrimSpace(m.meta.title)
	if title == "" {
		m.status = "title cannot be empty"
		return m, nil
	}
	priority, err := parsePriority(m.meta.priority)
	if err != nil {
		m.status = fmt.Sprintf("priority invalid: %v", err)
		return m, nil
	}
	due, err := parseDate(m.meta.due)
	if err != nil {
		m.status = fmt.Sprintf("due date invalid: %v", err)
		return m, nil
	}
	start, err := parseDate(m.meta.start)
	if err != nil {
		m.status = fmt.Sprintf("start date invalid: %v", err)
		return m, nil
	}
	recurring := parseYN(m.meta.recurring)

	err = m.store.UpdateTaskMetadata(m.meta.taskID, m.meta.project, m.meta.tags, priority, due, start, recurring)
	if err != nil {
		m.status = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	if err := m.store.UpdateTitle(m.meta.taskID, title); err != nil {
		m.status = fmt.Sprintf("title save failed: %v", err)
		return m, nil
	}
	m.meta = nil
	m.mode = modeList
	m.input.Blur()

	m.tasks, err = m.store.FetchTasks()
	if err == nil {
		m.sortTasks()
		for i, t := range m.tasks {
			if t.ID == taskID {
				m.cursor = clampCursor(i, len(m.tasks))
				break
			}
		}
		m.status = "Metadata saved"
	} else {
		m.status = fmt.Sprintf("reload failed: %v", err)
	}
	return m, nil
}

func metaFields() []string {
	return []string{"title", "topic", "tags", "priority", "due date (YYYY-MM-DD)", "start date (YYYY-MM-DD)", "recurring (y/n)"}
}

func (ms metaState) currentLabel() string {
	return metaFields()[ms.index]
}

func (ms metaState) currentValue() string {
	switch ms.index {
	case 0:
		return ms.title
	case 1:
		return ms.project
	case 2:
		return ms.tags
	case 3:
		return ms.priority
	case 4:
		return ms.due
	case 5:
		return ms.start
	case 6:
		return ms.recurring
	default:
		return ""
	}
}

func (ms *metaState) setCurrentValue(v string) {
	switch ms.index {
	case 0:
		ms.title = v
	case 1:
		ms.project = v
	case 2:
		ms.tags = v
	case 3:
		ms.priority = v
	case 4:
		ms.due = v
	case 5:
		ms.start = v
	case 6:
		ms.recurring = v
	}
}

func (m Model) metaPrompt() string {
	if m.meta == nil {
		return ""
	}
	return fmt.Sprintf("Editing %s (field %d of %d). Enter to advance, Esc to cancel, tab/hjkl to move.",
		m.meta.currentLabel(), m.meta.index+1, len(metaFields()))
}

func parsePriority(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	val, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	if val < 0 {
		val = 0
	}
	if val > 5 {
		val = 5
	}
	return val, nil
}

func parseDate(v string) (sql.NullTime, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

func formatDate(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}

func parseYN(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "y" || v == "yes" || v == "true" || v == "1"
}

func boolToYN(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

func (m Model) currentMetaLabel() string {
	if m.meta == nil {
		return ""
	}
	return m.meta.currentLabel()
}

func (m Model) renderMetaBox() string {
	if m.meta == nil {
		return ""
	}
	fields := metaFields()
	values := []string{
		m.meta.title,
		m.meta.project,
		m.meta.tags,
		m.meta.priority,
		m.meta.due,
		m.meta.start,
		m.meta.recurring,
	}
	var b strings.Builder
	for i, name := range fields {
		prefix := " "
		if i == m.meta.index {
			prefix = ">"
		}
		val := values[i]
		if strings.TrimSpace(val) == "" {
			val = "(empty)"
		}
		b.WriteString(fmt.Sprintf("%s %-18s : %s\n", prefix, name, val))
	}
	return b.String()
}

func wrapIndex(idx, n int) int {
	if n <= 0 {
		return 0
	}
	idx %= n
	if idx < 0 {
		idx += n
	}
	return idx
}

func (m Model) renderMetadataPanel() string {
	if len(m.tasks) == 0 {
		return "No task selected"
	}
	t := m.tasks[clampCursor(m.cursor, len(m.tasks))]
	var b strings.Builder
	b.WriteString("Metadata\n")
	b.WriteString(fmt.Sprintf("Title     : %s\n", t.Title))
	b.WriteString(fmt.Sprintf("Done      : %s\n", humanDone(t.Done)))
	b.WriteString(fmt.Sprintf("Topic     : %s\n", emptyPlaceholder(t.Project)))
	b.WriteString(fmt.Sprintf("Tags      : %s\n", emptyPlaceholder(t.Tags)))
	b.WriteString(fmt.Sprintf("Priority  : %d\n", t.Priority))
	b.WriteString(fmt.Sprintf("Due       : %s\n", formatDate(t.Due)))
	b.WriteString(fmt.Sprintf("Start     : %s\n", formatDate(t.Start)))
	b.WriteString(fmt.Sprintf("Recurring : %t\n", t.Recurring))
	return b.String()
}

func emptyPlaceholder(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(empty)"
	}
	return v
}

func (m Model) renderStatusBar() string {
	modeLabel := m.modeLabel()
	total := len(m.tasks)
	cursor := 0
	if total > 0 {
		cursor = m.cursor + 1
	}
	return fmt.Sprintf("[%s] sort:%s  %d/%d  %s", modeLabel, m.sortMode, cursor, total, m.status)
}

func (m Model) modeLabel() string {
	switch m.mode {
	case modeList:
		return "LIST"
	case modeAdd:
		return "ADD"
	case modeMetadata:
		return "META"
	case modeRename:
		return "RENAME"
	case modeCommand:
		return "COMMAND"
	default:
		return "?"
	}
}

func (m Model) startCommand() (tea.Model, tea.Cmd) {
	m.mode = modeCommand
	m.input.SetValue("")
	m.input.Placeholder = ""
	m.input.Focus()
	m.status = "Command: type 'help' and Enter, Esc to cancel"
	return m, nil
}

func (m Model) updateCommandMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.status = "Command cancelled"
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		cmd := strings.TrimSpace(m.input.Value())
		cmdLower := strings.ToLower(cmd)
		switch cmdLower {
		case "help":
			m.status = "Commands: help | sort (s then d/p/t) | rename (r) | priority +/- | due ]/["
		default:
			m.status = fmt.Sprintf("unknown command: %s", cmd)
		}
		m.mode = modeList
		m.input.Blur()
		return m, nil
	default:
		var c tea.Cmd
		m.input, c = m.input.Update(msg)
		return m, c
	}
}

func (m Model) startRename(t storage.Task) (tea.Model, tea.Cmd) {
	m.renameID = t.ID
	m.input.SetValue(t.Title)
	m.input.Placeholder = "Rename task"
	m.input.Focus()
	m.mode = modeRename
	m.status = "Rename: Enter to save, Esc to cancel"
	return m, nil
}

func (m Model) updateRenameMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel, "esc":
		m.mode = modeList
		m.renameID = 0
		m.input.Blur()
		m.status = "Rename cancelled"
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		title := strings.TrimSpace(m.input.Value())
		if title == "" {
			m.status = "Title cannot be empty"
			return m, nil
		}
		if err := m.store.UpdateTitle(m.renameID, title); err != nil {
			m.status = fmt.Sprintf("rename failed: %v", err)
			return m, nil
		}
		var err error
		m.tasks, err = m.store.FetchTasks()
		if err == nil {
			m.sortTasks()
			m.cursor = clampCursor(m.findTaskIndex(m.renameID), len(m.tasks))
			m.status = "Renamed task"
		} else {
			m.status = fmt.Sprintf("reload failed: %v", err)
		}
		m.renameID = 0
		m.mode = modeList
		m.input.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m Model) findTaskIndex(id int) int {
	for i, t := range m.tasks {
		if t.ID == id {
			return i
		}
	}
	return 0
}

func (m Model) bumpPriority(delta int) (tea.Model, tea.Cmd) {
	t := m.tasks[m.cursor]
	newPrio := t.Priority + delta
	if newPrio < 0 {
		newPrio = 0
	}
	if newPrio > 5 {
		newPrio = 5
	}
	if err := m.store.UpdatePriority(t.ID, newPrio); err != nil {
		m.status = fmt.Sprintf("priority failed: %v", err)
		return m, nil
	}
	var err error
	m.tasks, err = m.store.FetchTasks()
	if err == nil {
		m.sortTasks()
		m.cursor = clampCursor(m.findTaskIndex(t.ID), len(m.tasks))
		m.status = fmt.Sprintf("Priority set to %d", newPrio)
	} else {
		m.status = fmt.Sprintf("reload failed: %v", err)
	}
	return m, nil
}

func (m Model) shiftDue(days int) (tea.Model, tea.Cmd) {
	t := m.tasks[m.cursor]
	if err := m.store.ShiftDue(t.ID, days); err != nil {
		m.status = fmt.Sprintf("shift due failed: %v", err)
		return m, nil
	}
	var err error
	m.tasks, err = m.store.FetchTasks()
	if err == nil {
		m.sortTasks()
		m.cursor = clampCursor(m.findTaskIndex(t.ID), len(m.tasks))
		m.status = fmt.Sprintf("Due shifted by %+dd", days)
	} else {
		m.status = fmt.Sprintf("reload failed: %v", err)
	}
	return m, nil
}

func (m Model) lastTaskID() int {
	if len(m.tasks) == 0 {
		return 0
	}
	return m.tasks[len(m.tasks)-1].ID
}

func (m *Model) sortTasks() {
	switch m.sortMode {
	case "due":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			di, dj := m.tasks[i].Due, m.tasks[j].Due
			if di.Valid && dj.Valid {
				return di.Time.Before(dj.Time)
			}
			if di.Valid {
				return true
			}
			if dj.Valid {
				return false
			}
			return m.tasks[i].ID < m.tasks[j].ID
		})
	case "priority":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			if m.tasks[i].Priority == m.tasks[j].Priority {
				return m.tasks[i].ID < m.tasks[j].ID
			}
			return m.tasks[i].Priority > m.tasks[j].Priority
		})
	case "created":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			return m.tasks[i].CreatedAt.Before(m.tasks[j].CreatedAt)
		})
	default:
		sort.SliceStable(m.tasks, func(i, j int) bool {
			return m.tasks[i].ID < m.tasks[j].ID
		})
	}
}

func (m Model) currentTaskTitle() string {
	if len(m.tasks) == 0 {
		return ""
	}
	t := m.tasks[clampCursor(m.cursor, len(m.tasks))]
	return t.Title
}

func clampCursor(cur, n int) int {
	if n <= 0 {
		return 0
	}
	if cur < 0 {
		return 0
	}
	if cur >= n {
		return n - 1
	}
	return cur
}

func (m *Model) processSortKey(key string) bool {
	// simple 2-key sequence: s + d/p/t (due/priority/created-time)
	if key == "" {
		return false
	}
	if key == "s" {
		m.sortBuf = "s"
		m.status = "Sort: press d (due), p (priority), t (created)"
		return true
	}
	if m.sortBuf == "s" {
		switch key {
		case "d":
			m.sortMode = "due"
			m.sortTasks()
			m.status = "Sorted by due date"
		case "p":
			m.sortMode = "priority"
			m.sortTasks()
			m.status = "Sorted by priority"
		case "t":
			m.sortMode = "created"
			m.sortTasks()
			m.status = "Sorted by created time"
		default:
			m.status = "Sort cancelled"
		}
		m.sortBuf = ""
		return true
	}
	// reset buffer on other keys
	m.sortBuf = ""
	return false
}

func humanDone(done bool) string {
	if done {
		return "done"
	}
	return "pending"
}
