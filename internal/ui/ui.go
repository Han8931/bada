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
	modeTrash
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
	rule      string
	interval  string
	index     int
}

type Model struct {
	store         *storage.Store
	cfg           config.Config
	tasks         []storage.Task
	trash         []storage.TrashEntry
	cursor        int
	trashCursor   int
	mode          mode
	input         textinput.Model
	status        string
	filterDone    string
	sortMode      string
	sortBuf       string
	currentTopic  string
	confirmDel    bool
	pendingDel    *storage.Task
	confirmTopic  bool
	pendingTopic  string
	trashSelected map[int]bool
	trashConfirm  bool
	trashPending  []storage.TrashEntry
	meta          *metaState
	renameID      int
	renameTopic   string
	renameIsTopic bool
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
		store:         store,
		cfg:           cfg,
		tasks:         tasks,
		cursor:        clampCursor(0, len(tasks)),
		trashSelected: map[int]bool{},
		status:        "",
		input:         ti,
		mode:          modeList,
		filterDone:    strings.ToLower(cfg.DefaultFilter),
		sortMode:      "created",
		currentTopic:  "",
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
		if m.confirmTopic {
			return m.updateDeleteTopicConfirm(msg.String())
		}
		if m.mode == modeTrash {
			return m.updateTrashMode(msg.String(), msg)
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
	vis := m.visibleItems()
	switch key {
	case "ctrl+c", m.cfg.Keys.Quit:
		return m, tea.Quit
	case ":":
		return m.startCommand()
	case "h", "left":
		if m.currentTopic != "" {
			m.currentTopic = ""
			m.cursor = clampCursor(0, len(m.visibleItems()))
			m.status = "Back to root"
		}
	case m.cfg.Keys.Down, "down":
		if len(vis) == 0 {
			return m, nil
		}
		m.cursor = clampCursor(m.cursor+1, len(vis))
	case m.cfg.Keys.Up, "up":
		if m.cursor > 0 {
			m.cursor = clampCursor(m.cursor-1, len(vis))
		}
	case m.cfg.Keys.Add:
		m.mode = modeAdd
		m.input.Focus()
		m.status = "Add mode: type a title and press Enter"
	case m.cfg.Keys.Toggle:
		task, ok := m.currentTask()
		if !ok {
			return m, nil
		}
		err := m.store.SetDone(task.ID, !task.Done)
		if err != nil {
			m.status = fmt.Sprintf("toggle failed: %v", err)
			return m, nil
		}
		m.tasks, err = m.store.FetchTasks()
		if err == nil {
			m.sortTasks()
			vis = m.visibleItems()
			m.cursor = clampCursor(m.cursor+1, len(vis))
			m.status = "Toggled task"
		} else {
			m.status = fmt.Sprintf("reload failed: %v", err)
		}
	case m.cfg.Keys.Delete:
		task, ok := m.currentTask()
		if !ok {
			vis := m.visibleItems()
			if len(vis) > 0 && m.cursor < len(vis) {
				it := vis[m.cursor]
				if it.kind == itemTopic && m.currentTopic == "" {
					m.confirmTopic = true
					m.pendingTopic = it.topic
					m.status = fmt.Sprintf("Delete topic \"%s\" and its tasks? y/n", it.topic)
				}
			}
			return m, nil
		}
		m.confirmDel = true
		m.pendingDel = &task
		m.status = fmt.Sprintf("Delete \"%s\"? y/n", task.Title)
	case m.cfg.Keys.DeleteAllDone:
		m.confirmDel = true
		m.pendingDel = nil
		m.status = "Delete ALL done tasks? y/n"
	case m.cfg.Keys.Rename, "r":
		vis := m.visibleItems()
		if len(vis) == 0 {
			return m, nil
		}
		if m.cursor < len(vis) && vis[m.cursor].kind == itemTopic && m.currentTopic == "" {
			return m.startRenameTopic(vis[m.cursor].topic)
		}
		task, ok := m.currentTask()
		if !ok {
			return m, nil
		}
		return m.startRename(task)
	case m.cfg.Keys.PriorityUp, "+":
		if _, ok := m.currentTask(); !ok {
			return m, nil
		}
		return m.bumpPriority(1)
	case m.cfg.Keys.PriorityDown, "-":
		if _, ok := m.currentTask(); !ok {
			return m, nil
		}
		return m.bumpPriority(-1)
	case m.cfg.Keys.DueForward, "]":
		if _, ok := m.currentTask(); !ok {
			return m, nil
		}
		return m.shiftDue(1)
	case m.cfg.Keys.DueBack, "[":
		if _, ok := m.currentTask(); !ok {
			return m, nil
		}
		return m.shiftDue(-1)
	case m.cfg.Keys.Detail:
		task, ok := m.currentTask()
		if !ok {
			m.status = "No task selected"
			return m, nil
		}
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
			info += " • due:" + task.Due.Time.Format("2006-01-02") + overdueDetail(task)
		}
		if task.Start.Valid {
			info += " • start:" + task.Start.Time.Format("2006-01-02")
		}
		if task.RecurrenceRule != "" && task.RecurrenceRule != "none" {
			info += fmt.Sprintf(" • recur:%s", task.RecurrenceRule)
			if task.RecurrenceInterval > 0 {
				info += fmt.Sprintf("(%dd)", task.RecurrenceInterval)
			}
		}
		m.status = info
	case m.cfg.Keys.Edit:
		task, ok := m.currentTask()
		if !ok {
			m.status = "No tasks to edit"
			return m, nil
		}
		return m.startMetadataEdit(task)
	case m.cfg.Keys.SortDue:
		m.sortMode = "due"
		m.sortTasks()
		m.status = "Sorted by due date"
	case m.cfg.Keys.SortPriority:
		m.sortMode = "priority"
		m.sortTasks()
		m.status = "Sorted by priority"
	case m.cfg.Keys.SortCreated:
		m.sortMode = "created"
		m.sortTasks()
		m.status = "Sorted by created time"
	case m.cfg.Keys.Trash, "T":
		return m.enterTrashView()
	case "l", "right", "enter":
		if m.currentTopic == "" && len(vis) > 0 && m.cursor < len(vis) {
			it := vis[m.cursor]
			if it.kind == itemTopic {
				m.currentTopic = it.topic
				m.cursor = clampCursor(0, len(m.visibleItems()))
				m.status = fmt.Sprintf("Topic: %s", m.currentTopic)
				return m, nil
			}
		}
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString("bada (Bubble Tea + SQLite)")
	b.WriteString("\n\n")

	b.WriteString(m.renderTaskList())

	b.WriteString("\n---\n")

	if m.meta != nil {
		b.WriteString("Metadata editor (tab/shift+tab or h/j/k/l to move, enter to save/next, esc to cancel)")
		b.WriteString("\n\n")
		b.WriteString(m.renderMetaBox())
		b.WriteString("\n")
		b.WriteString("Field: " + m.currentMetaLabel())
		b.WriteString("\n")
		b.WriteString(m.input.View())
	} else if m.mode == modeTrash {
		b.WriteString("Trash (space to select, u to restore, esc to exit)")
		b.WriteString("\n\n")
		b.WriteString(m.renderTrashList())
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
				m.status = fmt.Sprintf("Moved %d done tasks to trash", n)
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
			m.status = "Deleted task (moved to trash)"
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

func (m Model) updateDeleteTopicConfirm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "n", "N", "esc":
		m.status = "Topic delete cancelled"
		m.confirmTopic = false
		m.pendingTopic = ""
		return m, nil
	case "y", "Y":
		if m.pendingTopic == "" {
			m.status = "No topic selected"
			m.confirmTopic = false
			return m, nil
		}
		n, err := m.store.DeleteTopic(m.pendingTopic)
		if err != nil {
			m.status = fmt.Sprintf("delete topic failed: %v", err)
			m.confirmTopic = false
			m.pendingTopic = ""
			return m, nil
		}
		var errReload error
		m.tasks, errReload = m.store.FetchTasks()
		if errReload == nil {
			m.sortTasks()
			m.cursor = clampCursor(0, len(m.visibleItems()))
			m.status = fmt.Sprintf("Deleted topic \"%s\" (%d tasks)", m.pendingTopic, n)
		} else {
			m.status = fmt.Sprintf("reload failed: %v", errReload)
		}
		m.confirmTopic = false
		m.pendingTopic = ""
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) enterTrashView() (tea.Model, tea.Cmd) {
	entries, err := m.store.ListTrash()
	if err != nil {
		m.status = fmt.Sprintf("trash load failed: %v", err)
		return m, nil
	}
	m.trash = entries
	m.trashSelected = map[int]bool{}
	m.trashCursor = clampCursor(0, len(entries))
	m.mode = modeTrash
	m.status = fmt.Sprintf("Trash: %d item(s). space to select, u to restore, P to purge, esc to exit", len(entries))
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
	return m, nil
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

func renderHelp(k config.Keymap) string {
	return fmt.Sprintf("%s/%s move • %s add • %s/%s detail • %s toggle • %s delete • %s edit • %s rename • %s/%s prio • %s/%s due • %s/%s/%s sort • %s trash • %s quit",
		k.Up, k.Down, k.Add, k.Detail, k.Confirm, k.Toggle, k.Delete, k.Edit, k.Rename, k.PriorityUp, k.PriorityDown, k.DueForward, k.DueBack, k.SortDue, k.SortPriority, k.SortCreated, k.Trash, k.Quit)
}

func (m Model) renderTaskList() string {
	var b strings.Builder
	b.WriteString("   C State    Title                                   Due        Topic\n")
	b.WriteString("   --- ------- ---------------------------------------- ---------- ----------------\n")
	items := m.visibleItems()
	for i, it := range items {
		cursor := " "
		if m.cursor == i && m.mode == modeList {
			cursor = ">"
		}
		switch it.kind {
		case itemTopic:
			count := m.topicCounts()[it.topic]
			b.WriteString(fmt.Sprintf("%s    [topic] %-40s (%d)\n", cursor, it.topic, count))
		case itemTask:
			checkbox := "[ ]"
			if it.task.Done {
				checkbox = "[x]"
			}
			title := it.task.Title
			if len(title) > 40 {
				title = title[:40]
			}
			state := humanDone(it.task.Done)
			due := displayDate(it.task.Due)
			if due == "" {
				due = "pending"
			}
			body := fmt.Sprintf("%s %s %-7s %-40s %-10s %-10s", cursor, checkbox, state, title, due, it.task.Project)
			if badge := overdueBadge(it.task); badge != "" {
				body += " " + badge
			}
			b.WriteString(body)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m Model) renderTrashList() string {
	var b strings.Builder
	b.WriteString("   Sel Deleted            Title                          Topic\n")
	b.WriteString("   --- ------------------ ------------------------------ ----------------\n")
	for i, entry := range m.trash {
		cursor := " "
		if m.mode == modeTrash && m.trashCursor == i {
			cursor = ">"
		}
		selected := "[ ]"
		if m.trashSelected != nil && m.trashSelected[i] {
			selected = "[*]"
		}
		title := entry.Task.Title
		if len(title) > 30 {
			title = title[:30]
		}
		deleted := entry.DeletedAt.Format("2006-01-02 15:04")
		b.WriteString(fmt.Sprintf("%s %s %-18s %-30s %-16s\n", cursor, selected, deleted, title, entry.Task.Project))
	}
	if len(m.trash) == 0 {
		b.WriteString("(trash is empty)\n")
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
		start:     defaultStart(t),
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
		m.meta.setCurrentValue(m.input.Value())
		var err error
		m, err = m.applyMetadataAndReload()
		if err != nil {
			m.status = fmt.Sprintf("save failed: %v", err)
			return m, nil
		}
		m.meta = nil
		m.mode = modeList
		m.input.Blur()
		m.status = "Metadata saved"
		return m, nil
	case "tab", "right", "down":
		if m.meta == nil {
			return m, nil
		}
		m.meta.setCurrentValue(m.input.Value())
		m.meta.index = wrapIndex(m.meta.index+1, len(metaFields()))
		m.input.SetValue(m.meta.currentValue())
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	case "shift+tab", "left", "up":
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
		var err error
		m, err = m.applyMetadataAndReload()
		if err != nil {
			m.status = fmt.Sprintf("save failed: %v", err)
			return m, nil
		}
		if m.meta.index >= len(metaFields())-1 {
			m.meta = nil
			m.mode = modeList
			m.input.Blur()
			m.status = "Metadata saved"
			return m, nil
		}
		m.meta.index++
		m.input.SetValue(m.meta.currentValue())
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		// sanitize input per field type
		if m.meta != nil {
			switch m.meta.index {
			case 3: // priority
				m.input.SetValue(filterDigits(m.input.Value()))
			case 4, 5: // dates
				m.input.SetValue(filterDate(m.input.Value()))
			case 6: // recurrence rule
				// allow only letters/dots
				m.input.SetValue(filterRule(m.input.Value()))
			case 7: // interval
				m.input.SetValue(filterDigits(m.input.Value()))
			case 8: // recurring flag
				m.input.SetValue(filterYN(m.input.Value()))
			}
		}
		return m, cmd
	}
}

func metaFields() []string {
	return []string{"title", "topic", "tags", "priority", "due date (YYYY-MM-DD)", "start date (YYYY-MM-DD)", "recurrence", "interval", "recurring (y/n)"}
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
		return ms.rule
	case 7:
		return ms.interval
	case 8:
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
		ms.rule = v
	case 7:
		ms.interval = v
	case 8:
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

func (m Model) applyMetadataAndReload() (Model, error) {
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
	rule := strings.TrimSpace(strings.ToLower(m.meta.rule))
	if rule == "" {
		rule = "none"
	}
	interval := parseInterval(m.meta.interval)

	if err := m.store.UpdateTaskMetadata(taskID, m.meta.project, m.meta.tags, priority, due, start, recurring); err != nil {
		return m, err
	}
	if err := m.store.UpdateRecurrence(taskID, rule, interval); err != nil {
		return m, err
	}
	if err := m.store.UpdateTitle(taskID, title); err != nil {
		return m, err
	}

	tasks, err := m.store.FetchTasks()
	if err != nil {
		return m, err
	}
	m.tasks = tasks
	m.sortTasks()
	for i, t := range m.tasks {
		if t.ID == taskID {
			m.cursor = clampCursor(i, len(m.tasks))
			break
		}
	}
	return m, nil
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

func displayDate(t sql.NullTime) string {
	if t.Valid {
		return formatDate(t)
	}
	return "Unknown"
}

func defaultStart(t storage.Task) string {
	if t.Start.Valid {
		return formatDate(t.Start)
	}
	return formatDate(sql.NullTime{Time: t.CreatedAt, Valid: true})
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
		m.meta.rule,
		m.meta.interval,
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
	task, ok := m.currentTask()
	if !ok {
		return "No task selected"
	}
	var b strings.Builder
	b.WriteString("Metadata\n")
	b.WriteString(fmt.Sprintf("Title     : %s\n", task.Title))
	b.WriteString(fmt.Sprintf("Tags      : %s\n", emptyPlaceholder(task.Tags)))
	b.WriteString(fmt.Sprintf("Priority  : %d\n", task.Priority))
	b.WriteString(fmt.Sprintf("Start     : %s\n", defaultStart(task)))
	b.WriteString(fmt.Sprintf("Recurring : %t\n", task.Recurring))
	if task.RecurrenceRule != "" && task.RecurrenceRule != "none" {
		b.WriteString(fmt.Sprintf("Rule      : %s\n", task.RecurrenceRule))
	}
	if task.RecurrenceInterval > 0 {
		b.WriteString(fmt.Sprintf("Interval  : %d\n", task.RecurrenceInterval))
	}
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
	if m.mode == modeTrash {
		sel := m.selectedTrashCount()
		total := len(m.trash)
		cur := 0
		if total > 0 {
			cur = m.trashCursor + 1
		}
		return fmt.Sprintf("[bada] [%s] cur:%d/%d sel:%d path:%s  %s", modeLabel, cur, total, sel, m.store.TrashDir(), m.status)
	}
	total := len(m.tasks)
	cursor := 0
	if total > 0 {
		cursor = m.cursor + 1
	}
	return fmt.Sprintf("[bada] [%s] sort:%s  %d/%d  %s", modeLabel, m.sortMode, cursor, total, m.status)
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
	case modeTrash:
		return "TRASH"
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

func (m Model) startRenameTopic(name string) (tea.Model, tea.Cmd) {
	m.renameID = 0
	m.renameTopic = name
	m.renameIsTopic = true
	m.input.SetValue(name)
	m.input.Placeholder = "Rename topic"
	m.input.Focus()
	m.mode = modeRename
	m.status = "Rename topic: Enter to save, Esc to cancel"
	return m, nil
}

func (m Model) updateRenameMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel, "esc":
		m.mode = modeList
		m.renameID = 0
		m.renameTopic = ""
		m.renameIsTopic = false
		m.input.Blur()
		m.status = "Rename cancelled"
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		title := strings.TrimSpace(m.input.Value())
		if title == "" {
			m.status = "Title cannot be empty"
			return m, nil
		}
		if m.renameIsTopic {
			if _, err := m.store.RenameTopic(m.renameTopic, title); err != nil {
				m.status = fmt.Sprintf("rename failed: %v", err)
				return m, nil
			}
			var err error
			m.tasks, err = m.store.FetchTasks()
			if err == nil {
				m.sortTasks()
				if m.currentTopic == m.renameTopic {
					m.currentTopic = title
				}
				m.cursor = clampCursor(m.findTopicIndex(title), len(m.visibleItems()))
				m.status = "Renamed topic"
			} else {
				m.status = fmt.Sprintf("reload failed: %v", err)
			}
		} else {
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
		}
		m.renameID = 0
		m.renameTopic = ""
		m.renameIsTopic = false
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
	t, ok := m.currentTask()
	if !ok {
		return ""
	}
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

func overdueBadge(t storage.Task) string {
	if !isOverdue(t) {
		return ""
	}
	days := int(time.Since(t.Due.Time).Hours()/24) + 1
	return fmt.Sprintf("[overdue %dd]", days)
}

func overdueDetail(t storage.Task) string {
	if !isOverdue(t) {
		return ""
	}
	days := int(time.Since(t.Due.Time).Hours()/24) + 1
	return fmt.Sprintf(" (overdue %dd)", days)
}

func isOverdue(t storage.Task) bool {
	if t.Done {
		return false
	}
	if !t.Due.Valid {
		return false
	}
	return time.Now().After(t.Due.Time)
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

func filterDigits(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func filterDate(v string) string {
	var b strings.Builder
	for _, r := range v {
		if (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() >= 10 {
			break
		}
	}
	return b.String()
}

func filterYN(v string) string {
	if v == "" {
		return ""
	}
	r := strings.ToLower(strings.TrimSpace(v))
	if len(r) == 0 {
		return ""
	}
	if r[0] == 'y' || r[0] == 'n' {
		return string(r[0])
	}
	return ""
}

func filterRule(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type itemKind int

const (
	itemTopic itemKind = iota
	itemTask
)

type listItem struct {
	kind  itemKind
	topic string
	task  storage.Task
}

func parseInterval(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	val, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	if val < 0 {
		return 0
	}
	return val
}

func (m Model) visibleItems() []listItem {
	items := make([]listItem, 0)
	if m.currentTopic == "" {
		for _, topic := range m.sortedTopics() {
			items = append(items, listItem{kind: itemTopic, topic: topic})
		}
		for _, t := range m.tasks {
			if strings.TrimSpace(t.Project) == "" {
				items = append(items, listItem{kind: itemTask, task: t})
			}
		}
	} else {
		for _, t := range m.tasks {
			if t.Project == m.currentTopic {
				items = append(items, listItem{kind: itemTask, task: t})
			}
		}
	}
	return items
}

func (m Model) topicCounts() map[string]int {
	counts := make(map[string]int)
	for _, t := range m.tasks {
		topic := strings.TrimSpace(t.Project)
		if topic == "" {
			continue
		}
		counts[topic]++
	}
	return counts
}

func (m Model) sortedTopics() []string {
	set := map[string]struct{}{}
	for _, t := range m.tasks {
		topic := strings.TrimSpace(t.Project)
		if topic == "" {
			continue
		}
		set[topic] = struct{}{}
	}
	topics := make([]string, 0, len(set))
	for k := range set {
		topics = append(topics, k)
	}
	sort.Strings(topics)
	return topics
}

func (m Model) currentTask() (storage.Task, bool) {
	items := m.visibleItems()
	if len(items) == 0 {
		return storage.Task{}, false
	}
	if m.cursor < 0 || m.cursor >= len(items) {
		return storage.Task{}, false
	}
	it := items[m.cursor]
	if it.kind != itemTask {
		return storage.Task{}, false
	}
	return it.task, true
}

func (m Model) findTopicIndex(topic string) int {
	vis := m.visibleItems()
	for i, it := range vis {
		if it.kind == itemTopic && it.topic == topic {
			return i
		}
	}
	return 0
}
