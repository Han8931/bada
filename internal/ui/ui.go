package ui

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	modeSearch
	modeTrash
	modeNote
	modeReport
)

type noteKind int

const (
	noteTask noteKind = iota
	noteTopic
)

type noteTarget struct {
	kind   noteKind
	taskID int
	title  string
	topic  string
}

type noteState struct {
	target noteTarget
	body   string
}

type noteEditedMsg struct {
	target noteTarget
	notes  string
	err    error
}

type uiStyles struct {
	Title     lipgloss.Style
	Heading   lipgloss.Style
	Accent    lipgloss.Style
	Muted     lipgloss.Style
	Border    lipgloss.Style
	Selection lipgloss.Style
	Done      lipgloss.Style
	Danger    lipgloss.Style
	Warning   lipgloss.Style
	Success   lipgloss.Style
	Status    lipgloss.Style
	StatusAlt lipgloss.Style
}

type metaState struct {
	taskID   int
	title    string
	topic    string
	tags     string
	priority string
	due      string
	start    string
	rule     string
	interval string
	index    int
}

type Model struct {
	store         *storage.Store
	cfg           config.Config
	tasks         []storage.Task
	trash         []storage.TrashEntry
	cursor        int
	navBuf        string
	trashCursor   int
	mode          mode
	report        string
	recentLimit   int
	input         textinput.Model
	status        string
	filterDone    string
	sortMode      string
	sortBuf       string
	pendingSort   bool
	currentTopic  string
	searchQuery   string
	styles        uiStyles
	width         int
	height        int
	noteScroll    int
	noteConfirm   bool
	notePending   noteTarget
	confirmDel    bool
	pendingDel    *storage.Task
	pendingBatch  []storage.Task
	confirmTopic  bool
	pendingTopic  string
	trashSelected map[int]bool
	trashConfirm  bool
	trashPending  []storage.TrashEntry
	selectedTasks map[int]bool
	meta          *metaState
	note          *noteState
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
		selectedTasks: map[int]bool{},
		status:        "",
		input:         ti,
		mode:          modeReport,
		recentLimit:   5,
		filterDone:    strings.ToLower(cfg.DefaultFilter),
		sortMode:      "auto",
		currentTopic:  "",
		styles:        buildStyles(cfg.Theme),
	}
	m.sortTasks()
	m.refreshReport()

	program := tea.NewProgram(m)
	_, err = program.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case noteEditedMsg:
		return m.handleNoteEdited(msg)
	case tea.KeyMsg:
		if m.meta != nil {
			return m.updateMetadataMode(msg.String(), msg)
		}
		if m.confirmTopic {
			return m.updateDeleteTopicConfirm(msg.String())
		}
		if m.mode == modeNote {
			return m.updateNoteMode(msg.String())
		}
		if m.mode == modeReport {
			return m.updateReportMode(msg.String(), msg)
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
		if m.mode == modeSearch {
			return m.updateSearchMode(msg.String(), msg)
		}
		if m.confirmDel {
			return m.updateDeleteConfirm(msg.String())
		}
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.input.Width = msg.Width - 10
		m.width = msg.Width
		m.height = msg.Height
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
	if m.processNavKey(key) {
		return m, nil
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
			m.cursor = clampCursor(m.findVisibleTaskIndex(m.lastTaskID()), len(m.visibleItems()))
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
	m = m.flushPendingSort(key)
	vis := m.visibleItems()
	switch key {
	case "ctrl+c", m.cfg.Keys.Quit:
		return m, tea.Quit
	case ":":
		return m.startCommand()
	case m.cfg.Keys.Search, "/":
		return m.startSearch()
	case m.cfg.Keys.Cancel, "esc":
		if m.searchActive() {
			m.searchQuery = ""
			m.cursor = clampCursor(0, len(m.visibleItems()))
			m.status = "Search cleared"
		}
		return m, nil
	case "h", "left":
		if m.currentTopic != "" {
			prevTopic := m.currentTopic
			m.currentTopic = ""
			m.cursor = clampCursor(m.findTopicIndex(prevTopic), len(m.visibleItems()))
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
			m.cursor = clampCursor(m.cursor, len(vis))
			m.status = "Toggled task"
		} else {
			m.status = fmt.Sprintf("reload failed: %v", err)
		}
	case " ":
		if task, ok := m.currentTask(); ok {
			m.toggleTaskSelection(task.ID)
			m.cursor = clampCursor(m.cursor+1, len(m.visibleItems()))
			return m, nil
		}
	case m.cfg.Keys.Delete:
		if selected := m.selectedTaskList(); len(selected) > 0 {
			m.confirmDel = true
			m.pendingBatch = selected
			m.status = fmt.Sprintf("Delete %d selected task(s)? y/n", len(selected))
			return m, nil
		}
		task, ok := m.currentTask()
		if !ok {
			vis := m.visibleItems()
			if len(vis) > 0 && m.cursor < len(vis) {
				it := vis[m.cursor]
				if it.kind == itemTopic && m.currentTopic == "" && !isSpecialTopic(it.topic) {
					m.confirmTopic = true
					m.pendingTopic = it.topic
					m.status = fmt.Sprintf("Delete topic \"%s\" and remove it from tasks? y/n", it.topic)
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
		if m.cursor < len(vis) && vis[m.cursor].kind == itemTopic && m.currentTopic == "" && !isSpecialTopic(vis[m.cursor].topic) {
			return m.startRenameTopic(vis[m.cursor].topic)
		}
		task, ok := m.currentTask()
		if !ok {
			return m, nil
		}
		return m.startRename(task)
	case "+":
		if m.processSortKey("+") {
			return m, nil
		}
		if _, ok := m.currentTask(); !ok {
			return m, nil
		}
		return m.bumpPriority(1)
	case "-":
		if m.processSortKey("-") {
			return m, nil
		}
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
	case m.cfg.Keys.NoteView:
		return m.startNoteView()
	case m.cfg.Keys.Detail:
		task, ok := m.currentTask()
		if !ok {
			m.status = "No task selected"
			return m, nil
		}
		info := fmt.Sprintf("Task #%d • %s • %s", task.ID, task.Title, humanDone(task.Done))
		if len(task.Topics) > 0 {
			info += " • topics:" + strings.Join(task.Topics, ",")
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
		if recSummary := recurrenceSummary(task); recSummary != "" {
			info += " • recur:" + recSummary
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

	if m.mode == modeNote {
		b.WriteString(m.renderNoteView())
		b.WriteString("\n\n")
		return m.fillView(b.String())
	}

	if m.mode == modeReport {
		b.WriteString(m.renderListBanner())
		b.WriteString("\n\n")
		b.WriteString(m.styles.Accent.Render("Reminder Report"))
		b.WriteString("\n\n")
		b.WriteString(m.report)
		b.WriteString("\n\n")
		return m.fillView(b.String())
	}

	header := m.renderListBanner() + "\n"
	gap := "\n"
	divider := m.styles.Border.Render(m.ruleLine(m.taskListLineWidth())) + "\n"
	footer := m.renderFooterPanel()
	tail := ""

	listMax := 0
	if m.height > 0 {
		available := m.height - 1
		listMax = available - countLines(header) - countLines(gap) - countLines(divider) - countLines(footer) - countLines(tail)
		if listMax < 0 {
			listMax = 0
		}
	}

	b.WriteString(header)
	if m.height > 0 {
		b.WriteString(m.renderTaskListWithHeight(listMax))
	} else {
		b.WriteString(m.renderTaskList())
	}
	b.WriteString(gap)
	b.WriteString(divider)
	b.WriteString(footer)
	b.WriteString(tail)
	return m.fillView(b.String())
}

func (m Model) renderFooterPanel() string {
	var b strings.Builder
	if m.meta != nil {
		b.WriteString(m.renderMetaBox())
		b.WriteString("\n")
		b.WriteString(m.styles.Muted.Render("Field: ") + m.styles.Accent.Render(m.currentMetaLabel()))
		b.WriteString("\n")
		b.WriteString(m.input.View())
		b.WriteString("\n\n")
		b.WriteString(m.styles.Heading.Render("Metadata editor (up/down or tab/shift+tab to move, enter to save/next, esc to cancel)"))
		return b.String()
	}
	switch m.mode {
	case modeReport:
		return m.styles.Muted.Render("Press enter/esc/q to close, : for commands")
	case modeTrash:
		b.WriteString(m.styles.Heading.Render("Trash (space to select, u to restore, esc to exit)"))
		b.WriteString("\n\n")
		b.WriteString(m.renderTrashList())
		return b.String()
	case modeAdd:
		b.WriteString(m.styles.Heading.Render("Add task: "))
		b.WriteString(m.input.View())
		return b.String()
	case modeRename:
		b.WriteString(m.styles.Heading.Render("Rename task: Enter to save, Esc to cancel"))
		b.WriteString("\n\n")
		b.WriteString(m.styles.Muted.Render("Current: ") + m.currentTaskTitle() + "\n")
		b.WriteString(m.styles.Muted.Render("New: "))
		b.WriteString(m.input.View())
		return b.String()
	case modeCommand:
		b.WriteString(m.styles.Heading.Render(":"))
		b.WriteString(m.input.View())
		return b.String()
	case modeSearch:
		b.WriteString(m.styles.Heading.Render("Search: "))
		b.WriteString(m.input.View())
		return b.String()
	default:
		return m.renderMetadataPanel()
	}
}

func (m Model) updateDeleteConfirm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "n", "N", "esc":
		m.status = "Delete cancelled"
		m.confirmDel = false
		m.pendingDel = nil
		m.pendingBatch = nil
		return m, nil
	case "y", "Y":
		if len(m.pendingBatch) > 0 {
			deleted := 0
			for _, task := range m.pendingBatch {
				if err := m.store.DeleteTask(task.ID); err != nil {
					m.status = fmt.Sprintf("delete failed: %v", err)
					m.confirmDel = false
					m.pendingBatch = nil
					return m, nil
				}
				deleted++
			}
			var errReload error
			m.tasks, errReload = m.store.FetchTasks()
			if errReload == nil {
				m.sortTasks()
				m.cursor = clampCursor(m.cursor, len(m.visibleItems()))
				m.status = fmt.Sprintf("Deleted %d task(s) (moved to trash)", deleted)
				m.selectedTasks = map[int]bool{}
			} else {
				m.status = fmt.Sprintf("reload failed: %v", errReload)
			}
			m.confirmDel = false
			m.pendingBatch = nil
			return m, nil
		}
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
				m.cursor = clampCursor(m.cursor, len(m.visibleItems()))
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
			m.cursor = clampCursor(m.cursor, len(m.visibleItems()))
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
			m.status = fmt.Sprintf("Removed topic \"%s\" from %d task(s)", m.pendingTopic, n)
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

func (m Model) enterReportView() (tea.Model, tea.Cmd) {
	m.refreshReport()
	m.mode = modeReport
	m.status = "Reminder report"
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

func (m Model) updateReportMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "enter", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Report closed"
		return m, nil
	case ":":
		return m.startCommand()
	default:
		return m, nil
	}
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
		m.mode = modeList
		m.note = nil
		m.status = "Notes closed"
		return m, nil
	case m.cfg.Keys.Edit:
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

func renderHelp(k config.Keymap) string {
	return fmt.Sprintf("%s/%s move • %s add • space select • %s/%s detail • %s done • %s purge • %s edit • %s notes • %s rename • %s/%s prio • %s/%s due • %s/%s/%s sort • %s trash • %s search • %s quit",
		k.Up, k.Down, k.Add, k.Detail, k.Confirm, k.Toggle, k.Delete, k.Edit, k.NoteView, k.Rename, k.PriorityUp, k.PriorityDown, k.DueForward, k.DueBack, k.SortDue, k.SortPriority, k.SortCreated, k.Trash, k.Search, k.Quit)
}

func (m Model) renderTaskList() string {
	return m.renderTaskListWithHeight(-1)
}

func (m Model) taskListLineWidth() int {
	header := "      Title                                   Due"
	lineWidth := len(header)
	if m.width > lineWidth {
		lineWidth = m.width
	}
	return lineWidth
}

func (m Model) renderTaskListWithHeight(maxLines int) string {
	items := m.visibleItems()
	header := "      Title                                   Due"
	lineWidth := m.taskListLineWidth()

	lines := make([]string, 0)
	if m.searchActive() {
		lines = append(lines, m.styles.Accent.Render(fmt.Sprintf("Search: %q (%d result(s))", m.searchQuery, len(items))))
	}
	lines = append(lines, m.styles.Border.Render(header))
	lines = append(lines, m.styles.Border.Render(m.ruleLine(lineWidth)))

	itemLines := make([]string, 0, len(items))
	for i, it := range items {
		switch it.kind {
		case itemTopic:
			line := ""
			if isSpecialTopic(it.topic) {
				line = fmt.Sprintf("   %-2s %s", "📁", it.topic)
			} else {
				stat := m.topicStats()[it.topic]
				line = fmt.Sprintf("   %-2s %s (%d/%d)", "📁", it.topic, stat.overdue, stat.total)
			}
			if m.cursor == i && m.mode == modeList {
				line = m.styles.Selection.Render(line)
			} else if isSpecialTopic(it.topic) {
				line = m.styles.Heading.Render(line)
			} else {
				line = m.styles.Accent.Render(line)
			}
			itemLines = append(itemLines, line)
		case itemTask:
			title := it.task.Title
			if len(title) > 40 {
				title = title[:40]
			}
			state := humanDone(it.task.Done)
			due := displayDate(it.task.Due)
			badge := overdueBadge(it.task)
			recBadge := recurrenceBadge(it.task)
			if due == "" {
				due = "pending"
			}
			body := fmt.Sprintf("   %-2s %-40s %-10s", state, title, due)
			if badge != "" {
				if m.cursor == i && m.mode == modeList {
					body += " " + badge
				} else {
					body += " " + m.styles.Danger.Render(badge)
				}
			}
			if recBadge != "" {
				if m.cursor == i && m.mode == modeList {
					body += " " + recBadge
				} else {
					body += " " + m.styles.Warning.Render(recBadge)
				}
			}
			if m.searchActive() && len(it.task.Topics) > 0 {
				body += " [" + strings.Join(it.task.Topics, ",") + "]"
			}
			if m.cursor == i && m.mode == modeList {
				body = m.styles.Selection.Render(body)
			} else if m.isTaskSelected(it.task.ID) {
				body = m.styles.Warning.Render(body)
			} else if it.task.Done {
				body = m.styles.Done.Render(body)
			}
			itemLines = append(itemLines, body)
		}
	}
	if len(items) == 0 {
		itemLines = append(itemLines, m.styles.Muted.Render("(no tasks)"))
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

func (m Model) renderTrashList() string {
	var b strings.Builder
	header := "   Sel Deleted            Title                          Topics"
	lineWidth := len(header)
	if m.width > lineWidth {
		lineWidth = m.width
	}
	b.WriteString(m.styles.Border.Render(header))
	b.WriteString("\n")
	b.WriteString(m.styles.Border.Render(m.ruleLine(lineWidth)))
	b.WriteString("\n")
	for i, entry := range m.trash {
		cursor := " "
		selected := "[ ]"
		if m.trashSelected != nil && m.trashSelected[i] {
			selected = "[*]"
		}
		title := entry.Task.Title
		if len(title) > 30 {
			title = title[:30]
		}
		deleted := entry.DeletedAt.Format("2006-01-02 15:04")
		line := fmt.Sprintf("%s %s %-18s %-30s %-16s", cursor, selected, deleted, title, strings.Join(entry.Task.Topics, ","))
		if m.mode == modeTrash && m.trashCursor == i {
			line = m.styles.Selection.Render(line)
		} else if m.trashSelected != nil && m.trashSelected[i] {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(m.trash) == 0 {
		b.WriteString(m.styles.Muted.Render("(trash is empty)"))
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) startNoteView() (tea.Model, tea.Cmd) {
	target, notes, ok, err := m.noteTargetFromSelection()
	if err != nil {
		m.status = fmt.Sprintf("note load failed: %v", err)
		return m, nil
	}
	if !ok {
		m.status = "No task or topic selected"
		return m, nil
	}
	m.note = &noteState{target: target, body: notes}
	m.noteScroll = 0
	m.mode = modeNote
	m.status = fmt.Sprintf("Notes: %s", target.label())
	return m, nil
}

func (m Model) startNoteEditFromState() (tea.Model, tea.Cmd) {
	if m.note == nil {
		m.status = "No note loaded"
		return m, nil
	}
	cmd, err := m.noteEditorCmd(m.note.target, m.note.body)
	if err != nil {
		m.status = fmt.Sprintf("note editor failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Editing note: %s", m.note.target.label())
	return m, cmd
}

func (m Model) noteTargetFromSelection() (noteTarget, string, bool, error) {
	if task, ok := m.currentTask(); ok {
		target := noteTarget{kind: noteTask, taskID: task.ID, title: task.Title}
		return target, task.Notes, true, nil
	}
	if topic, ok := m.currentTopicItem(); ok {
		if isSpecialTopic(topic) {
			return noteTarget{}, "", false, errors.New("notes are not available for system topics")
		}
		notes, err := m.store.TopicNote(topic)
		if err != nil {
			return noteTarget{}, "", false, err
		}
		target := noteTarget{kind: noteTopic, topic: topic, title: topic}
		return target, notes, true, nil
	}
	return noteTarget{}, "", false, nil
}

func (m Model) renderNoteView() string {
	if m.note == nil {
		return m.styles.Muted.Render("No notes")
	}
	var b strings.Builder
	headerLines := []string{
		m.styles.Heading.Render("Notes: ") + m.styles.Accent.Render(m.note.target.label()),
		"",
	}
	footerLine := m.styles.Muted.Render(fmt.Sprintf("Press %s/%s/enter to close, %s to edit, %s to purge",
		m.cfg.Keys.Cancel, m.cfg.Keys.Quit, m.cfg.Keys.Edit, m.cfg.Keys.Delete))

	bodyLines := m.noteBodyLines()
	available := m.noteAvailableHeight()
	if available < 0 {
		for _, line := range headerLines {
			b.WriteString(line)
			b.WriteString("\n")
		}
		for _, line := range bodyLines {
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(footerLine)
		return b.String()
	}
	maxScroll := m.noteMaxScrollWith(available, len(bodyLines))
	scroll := m.noteScroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	start := scroll
	end := start + available
	if start < 0 {
		start = 0
	}
	if end > len(bodyLines) {
		end = len(bodyLines)
	}
	for _, line := range headerLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range bodyLines[start:end] {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(footerLine)
	return b.String()
}

func (m Model) noteEditorCmd(target noteTarget, notes string) (tea.Cmd, error) {
	parts := resolveEditor()
	if len(parts) == 0 {
		return nil, errors.New("editor not set")
	}
	tmp, err := os.CreateTemp("", "bada-note-*.md")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	if _, err := tmp.WriteString(notes); err != nil {
		tmp.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		data, readErr := os.ReadFile(path)
		_ = os.Remove(path)
		if err == nil && readErr != nil {
			err = readErr
		}
		return noteEditedMsg{target: target, notes: string(data), err: err}
	}), nil
}

func resolveEditor() []string {
	if v := strings.TrimSpace(os.Getenv("VISUAL")); v != "" {
		return strings.Fields(v)
	}
	if v := strings.TrimSpace(os.Getenv("EDITOR")); v != "" {
		return strings.Fields(v)
	}
	return []string{"vi"}
}

func buildStyles(theme config.Theme) uiStyles {
	styles := uiStyles{
		Title:     lipgloss.NewStyle().Bold(true),
		Heading:   lipgloss.NewStyle().Bold(true),
		Accent:    lipgloss.NewStyle().Bold(true),
		Muted:     lipgloss.NewStyle(),
		Border:    lipgloss.NewStyle(),
		Selection: lipgloss.NewStyle().Bold(true),
		Done:      lipgloss.NewStyle().Strikethrough(true),
		Danger:    lipgloss.NewStyle().Bold(true),
		Warning:   lipgloss.NewStyle(),
		Success:   lipgloss.NewStyle(),
		Status:    lipgloss.NewStyle(),
		StatusAlt: lipgloss.NewStyle(),
	}

	styles.Title = applyFg(styles.Title, theme.Title)
	styles.Heading = applyFg(styles.Heading, theme.Heading)
	styles.Accent = applyFg(styles.Accent, theme.Accent)
	styles.Muted = applyFg(styles.Muted, theme.Muted)
	styles.Border = applyFg(styles.Border, theme.Border)
	styles.Danger = applyFg(styles.Danger, theme.Danger)
	styles.Warning = applyFg(styles.Warning, theme.Warning)
	styles.Success = applyFg(styles.Success, theme.Success)
	styles.Done = applyFg(styles.Done, theme.Muted)

	styles.Selection = applyBg(styles.Selection, theme.SelectionBg)
	styles.Selection = applyFg(styles.Selection, theme.SelectionFg)

	styles.Status = applyBg(styles.Status, theme.StatusBg)
	styles.Status = applyFg(styles.Status, theme.StatusFg)
	styles.StatusAlt = applyBg(styles.StatusAlt, theme.StatusAltBg)
	styles.StatusAlt = applyFg(styles.StatusAlt, theme.StatusAltFg)

	return styles
}

func applyFg(style lipgloss.Style, color string) lipgloss.Style {
	if strings.TrimSpace(color) == "" {
		return style
	}
	return style.Foreground(lipgloss.Color(color))
}

func applyBg(style lipgloss.Style, color string) lipgloss.Style {
	if strings.TrimSpace(color) == "" {
		return style
	}
	return style.Background(lipgloss.Color(color))
}

func (m Model) handleNoteEdited(msg noteEditedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("note edit failed: %v", msg.err)
		return m, nil
	}
	switch msg.target.kind {
	case noteTask:
		if err := m.store.UpdateTaskNotes(msg.target.taskID, msg.notes); err != nil {
			m.status = fmt.Sprintf("note save failed: %v", err)
			return m, nil
		}
		m.applyTaskNoteLocal(msg.target.taskID, msg.notes)
		m.status = fmt.Sprintf("Saved note: %s", msg.target.label())
	case noteTopic:
		if err := m.store.UpdateTopicNote(msg.target.topic, msg.notes); err != nil {
			m.status = fmt.Sprintf("note save failed: %v", err)
			return m, nil
		}
		m.status = fmt.Sprintf("Saved note: %s", msg.target.label())
	}
	if m.note != nil && m.note.target.matches(msg.target) {
		m.note.body = msg.notes
		m.noteScroll = clampInt(m.noteScroll, 0, m.noteMaxScroll())
	}
	return m, nil
}

func (m *Model) applyTaskNoteLocal(taskID int, notes string) {
	idx := m.findTaskIndex(taskID)
	if idx < 0 || idx >= len(m.tasks) {
		return
	}
	m.tasks[idx].Notes = notes
}

func (t noteTarget) label() string {
	switch t.kind {
	case noteTask:
		if t.title != "" {
			return fmt.Sprintf("Task #%d %s", t.taskID, t.title)
		}
		return fmt.Sprintf("Task #%d", t.taskID)
	case noteTopic:
		if t.topic != "" {
			return fmt.Sprintf("Topic %s", t.topic)
		}
		return "Topic"
	default:
		return "Notes"
	}
}

func (t noteTarget) matches(other noteTarget) bool {
	if t.kind != other.kind {
		return false
	}
	if t.kind == noteTask {
		return t.taskID == other.taskID
	}
	return t.topic == other.topic
}

func (m Model) startMetadataEdit(t storage.Task) (tea.Model, tea.Cmd) {
	m.meta = &metaState{
		taskID:   t.ID,
		title:    t.Title,
		topic:    strings.Join(t.Topics, ","),
		tags:     t.Tags,
		priority: fmt.Sprintf("%d", t.Priority),
		due:      formatDate(t.Due),
		start:    defaultStart(t),
		rule:     t.RecurrenceRule,
		interval: intervalString(t.RecurrenceInterval),
		index:    0,
	}
	m.input.SetValue(m.meta.currentValue())
	m.input.CursorEnd()
	m.input.Placeholder = m.meta.currentLabel()
	m.input.Focus()
	m.mode = modeMetadata
	m.status = "Edit metadata: up/down or tab/shift+tab to move, enter to save/next, esc to cancel"
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
	case "tab", "down":
		if m.meta == nil {
			return m, nil
		}
		m.meta.setCurrentValue(m.input.Value())
		m.meta.index = wrapIndex(m.meta.index+1, len(metaFields()))
		m.input.SetValue(m.meta.currentValue())
		m.input.CursorEnd()
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	case "shift+tab", "up":
		if m.meta == nil {
			return m, nil
		}
		m.meta.setCurrentValue(m.input.Value())
		m.meta.index = wrapIndex(m.meta.index-1, len(metaFields()))
		m.input.SetValue(m.meta.currentValue())
		m.input.CursorEnd()
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
		m.input.CursorEnd()
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	case "+":
		if m.meta != nil && m.meta.index == 3 {
			val, _ := strconv.Atoi(filterDigits(m.input.Value()))
			if val < 5 {
				val++
			}
			m.meta.priority = fmt.Sprintf("%d", val)
			m.input.SetValue(m.meta.priority)
			m.status = m.metaPrompt()
			return m, nil
		}
		// not editing priority; handle as normal input so characters like '-' go through for dates
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.applyMetaInputSanitizer()
		return m, cmd
	case "-":
		if m.meta != nil && m.meta.index == 3 {
			val, _ := strconv.Atoi(filterDigits(m.input.Value()))
			if val > 0 {
				val--
			}
			m.meta.priority = fmt.Sprintf("%d", val)
			m.input.SetValue(m.meta.priority)
			m.status = m.metaPrompt()
			return m, nil
		}
		// not editing priority; handle as normal input so '-' works in dates
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.applyMetaInputSanitizer()
		return m, cmd
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.applyMetaInputSanitizer()
		return m, cmd
	}
	return m, nil
}

func (m *Model) applyMetaInputSanitizer() {
	// sanitize input per field type and store it
	if m.meta == nil {
		return
	}
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
	}
	m.meta.setCurrentValue(m.input.Value())
}

func metaFields() []string {
	return []string{
		"Title",
		"Topics (CSV)",
		"Tags",
		"Priority",
		"Due Date (YYYY-MM-DD)",
		"Start Date (YYYY-MM-DD)",
		"Recurrence",
		"Interval",
	}
}

func (ms metaState) currentLabel() string {
	return metaFields()[ms.index]
}

func (ms metaState) currentValue() string {
	switch ms.index {
	case 0:
		return ms.title
	case 1:
		return ms.topic
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
	default:
		return ""
	}
}

func (ms *metaState) setCurrentValue(v string) {
	switch ms.index {
	case 0:
		ms.title = v
	case 1:
		ms.topic = v
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
	}
}

func (m Model) metaPrompt() string {
	if m.meta == nil {
		return ""
	}
	return fmt.Sprintf("Editing %s (field %d of %d). Enter to advance, Esc to cancel, up/down or tab/shift+tab to move.",
		m.meta.currentLabel(), m.meta.index+1, len(metaFields()))
}

func (m Model) flushPendingSort(nextKey string) Model {
	if !m.pendingSort || nextKey == "+" || nextKey == "-" || nextKey == "[" || nextKey == "]" {
		return m
	}
	tasks, err := m.store.FetchTasks()
	if err != nil {
		m.status = fmt.Sprintf("reload failed: %v", err)
		return m
	}
	m.tasks = tasks
	m.sortTasks()
	m.pendingSort = false
	return m
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
	ruleInput := strings.TrimSpace(m.meta.rule)
	rule := strings.TrimSpace(ruleInput)
	interval := parseInterval(m.meta.interval)
	recurring := rule != "" || interval > 0
	if spec, ok := parseRecurrenceSpec(ruleInput); ok {
		rule = spec.label
		interval = 0
		recurring = true
	}
	if strings.TrimSpace(rule) == "" || strings.EqualFold(rule, "none") {
		if interval > 0 {
			rule = fmt.Sprintf("every %d days", interval)
			interval = 0
		} else {
			rule = "none"
		}
	}

	if err := m.store.UpdateTaskMetadata(taskID, m.meta.topic, m.meta.tags, priority, due, start, recurring); err != nil {
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
	m.cursor = clampCursor(m.findVisibleTaskIndex(taskID), len(m.visibleItems()))
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
	labelWidth := 0
	for _, name := range fields {
		if len(name) > labelWidth {
			labelWidth = len(name)
		}
	}
	values := []string{
		m.meta.title,
		m.meta.topic,
		m.meta.tags,
		m.meta.priority,
		m.meta.due,
		m.meta.start,
		m.meta.rule,
		m.meta.interval,
	}
	var b strings.Builder
	for i, name := range fields {
		prefix := " "
		val := values[i]
		if strings.TrimSpace(val) == "" {
			val = "(empty)"
		}
		label := fmt.Sprintf("%-*s", labelWidth, name)
		line := fmt.Sprintf("%s %s : %s", prefix, m.styles.Heading.Render(label), val)
		if i == m.meta.index {
			line = m.styles.Selection.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) refreshReport() {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.Add(24 * time.Hour)
	soon := today.Add(72 * time.Hour)

	var overdue, todayList, upcoming, recurring []storage.Task
	for _, t := range m.tasks {
		if isRecurringTask(t) && !t.Done {
			recurring = append(recurring, t)
		}
		if t.Done || !t.Due.Valid {
			continue
		}
		d := t.Due.Time
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

	var b strings.Builder
	writeDivider := func() {
		b.WriteString(m.styles.Border.Render(m.ruleLine(m.width)))
		b.WriteString("\n")
	}
	writeSectionHeader := func(title string, count int) {
		line := fmt.Sprintf("%s (%d)", title, count)
		b.WriteString(m.styles.Heading.Render(line))
		b.WriteString("\n")
	}
	writeEmpty := func() {
		b.WriteString(m.styles.Muted.Render("  (none)"))
		b.WriteString("\n")
	}

	b.WriteString(m.styles.Muted.Render(now.Format("Monday, Jan 2, 2006")))
	b.WriteString("\n")
	writeDivider()
	if len(upcoming) > 0 {
		summary := fmt.Sprintf("Upcoming: %d task(s) in next 3 days", len(upcoming))
		b.WriteString(m.styles.Warning.Render("  " + summary))
		b.WriteString("\n")
		writeDivider()
	}

	if len(overdue) == 0 && len(todayList) == 0 && len(upcoming) == 0 {
		b.WriteString(m.styles.Success.Render("  All clear. No due tasks."))
		b.WriteString("\n\n")
	} else {
		writeSection := func(title string, tasks []storage.Task, style lipgloss.Style) {
			writeSectionHeader(title, len(tasks))
			if len(tasks) == 0 {
				writeEmpty()
				b.WriteString("\n")
				return
			}
			for _, t := range tasks {
				due := formatDate(t.Due)
				line := fmt.Sprintf("  • #%d %-40s  due %s", t.ID, truncateText(t.Title, 40), due)
				b.WriteString(style.Render(line))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
		if len(overdue) > 0 {
			writeSection("Overdue", overdue, m.styles.Danger)
		}
		if len(todayList) > 0 {
			writeSection("Due Today", todayList, m.styles.Accent)
		}
		if len(upcoming) > 0 {
			writeSection("Upcoming (3d)", upcoming, m.styles.Muted)
		}
	}
	if len(recurring) > 0 {
		writeSectionHeader("Recurring Tasks", len(recurring))
		for _, t := range recurring {
			due := "no due"
			if t.Due.Valid {
				due = fmt.Sprintf("due %s", formatDate(t.Due))
			}
			next := ""
			if nextDate, ok := nextRecurrenceDate(t); ok {
				next = fmt.Sprintf("next %s", nextDate.Format("2006-01-02"))
			}
			line := fmt.Sprintf("  • #%d %-40s  [%s] %s", t.ID, truncateText(t.Title, 40), recurrenceRuleLabel(t), due)
			if next != "" {
				line += " • " + next
			}
			b.WriteString(m.styles.Warning.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	writeDivider()

	recentAdd := m.recentlyAdded(m.recentLimit)
	recentDone := m.recentlyDone(m.recentLimit)
	writeSectionHeader("Recently Added", len(recentAdd))
	if len(recentAdd) == 0 {
		writeEmpty()
	} else {
		for _, t := range recentAdd {
			b.WriteString(fmt.Sprintf("  • #%d %-40s  created %s\n", t.ID, truncateText(t.Title, 40), t.CreatedAt.Format("2006-01-02")))
		}
	}
	b.WriteString("\n")
	writeSectionHeader("Recently Done", len(recentDone))
	if len(recentDone) == 0 {
		writeEmpty()
	} else {
		for _, t := range recentDone {
			when := "unknown"
			if t.CompletedAt.Valid {
				when = t.CompletedAt.Time.Format("2006-01-02")
			}
			b.WriteString(fmt.Sprintf("  • #%d %-40s  done %s\n", t.ID, truncateText(t.Title, 40), when))
		}
	}
	b.WriteString("\n")
	m.report = b.String()
	m.status = "Reminder report"
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
	type row struct {
		label string
		value string
	}
	rows := []row{
		{label: "Title", value: ""},
		{label: "Topics", value: ""},
		{label: "Tags", value: ""},
		{label: "Priority", value: ""},
		{label: "Start", value: ""},
		{label: "Recurrence", value: ""},
	}
	if ok {
		rows[0].value = task.Title
		rows[1].value = emptyPlaceholder(strings.Join(task.Topics, ", "))
		rows[2].value = emptyPlaceholder(task.Tags)
		rows[3].value = fmt.Sprintf("%d", task.Priority)
		rows[4].value = defaultStart(task)
		if recSummary := recurrenceSummary(task); recSummary != "" {
			if next, ok := nextRecurrenceDate(task); ok {
				rows[5].value = fmt.Sprintf("%s • Next: %s", recSummary, next.Format("2006-01-02"))
			} else {
				rows[5].value = recSummary
			}
		} else {
			rows[5].value = "off"
		}
	} else {
		for i := range rows {
			rows[i].value = "(empty)"
		}
	}

	var b strings.Builder
	labelWidth := 0
	for _, r := range rows {
		if len(r.label) > labelWidth {
			labelWidth = len(r.label)
		}
	}
	for _, r := range rows {
		label := fmt.Sprintf("%-*s", labelWidth, r.label)
		b.WriteString(fmt.Sprintf("%s%s\n", m.styles.Muted.Render(label+" : "), r.value))
	}
	return b.String()
}

func truncateText(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(text) <= max {
		return text
	}
	if max <= 1 {
		return text[:max]
	}
	return text[:max-1] + "…"
}

func (m Model) renderMarkdown(input string) string {
	var b strings.Builder
	lines := strings.Split(input, "\n")
	inCode := false
	var codeLines []string
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inCode = !inCode
			if !inCode {
				b.WriteString(m.renderCodeBlock(codeLines))
				b.WriteString("\n")
				codeLines = nil
			}
			continue
		}
		if inCode {
			codeLines = append(codeLines, line)
			continue
		}
		if trim == "" {
			b.WriteString("\n")
			continue
		}
		if isRuleLine(trim) {
			b.WriteString(m.styles.Border.Render(m.ruleLine(m.width)))
			b.WriteString("\n")
			continue
		}
		if strings.HasPrefix(trim, "#") {
			level, title := parseHeading(trim)
			hashes := strings.Repeat("#", level)
			prefix := m.styles.Muted.Render(hashes + " ")
			style := m.styles.Heading
			if level == 1 {
				style = m.styles.Accent
			}
			b.WriteString(prefix + style.Render(title))
			b.WriteString("\n")
			continue
		}
		if prefix, rest, ok := parseList(trim); ok {
			b.WriteString(prefix)
			b.WriteString(m.renderInlineMarkdown(rest))
			b.WriteString("\n")
			continue
		}
		if strings.HasPrefix(trim, ">") {
			rest := strings.TrimSpace(strings.TrimPrefix(trim, ">"))
			b.WriteString(m.styles.Muted.Render("│ "))
			b.WriteString(m.renderInlineMarkdown(rest))
			b.WriteString("\n")
			continue
		}
		b.WriteString(m.renderInlineMarkdown(line))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) noteBodyLines() []string {
	if m.note == nil {
		return []string{m.styles.Muted.Render("(empty)")}
	}
	body := m.note.body
	if strings.TrimSpace(body) == "" {
		return []string{m.styles.Muted.Render("(empty)")}
	}
	rendered := m.renderMarkdown(body)
	return strings.Split(rendered, "\n")
}

func (m Model) noteAvailableHeight() int {
	if m.height <= 0 {
		return -1
	}
	headerLines := 2
	footerLines := 1
	blankBeforeFooter := 1
	usable := m.height - 1 - headerLines - footerLines - blankBeforeFooter
	if usable < 0 {
		return 0
	}
	return usable
}

func (m Model) noteMaxScroll() int {
	available := m.noteAvailableHeight()
	if available < 0 {
		return 0
	}
	bodyLines := m.noteBodyLines()
	return m.noteMaxScrollWith(available, len(bodyLines))
}

func (m Model) clearNote(target noteTarget) error {
	switch target.kind {
	case noteTask:
		return m.store.UpdateTaskNotes(target.taskID, "")
	case noteTopic:
		return m.store.DeleteTopicNote(target.topic)
	default:
		return nil
	}
}

func (m Model) noteMaxScrollWith(available, bodyLines int) int {
	if available <= 0 || bodyLines <= available {
		return 0
	}
	return bodyLines - available
}

func (m Model) renderListBanner() string {
	lines := []string{
		" ____    _    ____    _    ",
		"| __ )  / \\  |  _ \\  / \\   ",
		"|  _ \\ / _ \\ | | | \\/ _ \\  ",
		"| |_) / ___ \\| |_| / ___ \\ ",
		"|____/_/   \\_\\____/_/   \\_\\",
	}
	for i, line := range lines {
		lines[i] = m.styles.Accent.Render(line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderInlineMarkdown(input string) string {
	var b strings.Builder
	var buf strings.Builder
	inBold := false
	inItalic := false
	inCode := false
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		text := buf.String()
		buf.Reset()
		switch {
		case inCode:
			b.WriteString(m.styles.Muted.Render(text))
		case inBold && inItalic:
			b.WriteString(lipgloss.NewStyle().Bold(true).Italic(true).Render(text))
		case inBold:
			b.WriteString(lipgloss.NewStyle().Bold(true).Render(text))
		case inItalic:
			b.WriteString(lipgloss.NewStyle().Italic(true).Render(text))
		default:
			b.WriteString(text)
		}
	}
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if ch == '`' {
			flush()
			inCode = !inCode
			continue
		}
		if ch == '*' && next == '*' {
			flush()
			inBold = !inBold
			i++
			continue
		}
		if ch == '*' || ch == '_' {
			flush()
			inItalic = !inItalic
			continue
		}
		buf.WriteRune(ch)
	}
	flush()
	return b.String()
}

func parseHeading(line string) (int, string) {
	level := 0
	for _, r := range line {
		if r != '#' {
			break
		}
		level++
	}
	if level == 0 {
		return 0, line
	}
	title := strings.TrimSpace(line[level:])
	if title == "" {
		title = strings.Repeat("#", level)
	}
	if level > 6 {
		level = 6
	}
	return level, title
}

func parseList(line string) (string, string, bool) {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return "  • ", strings.TrimSpace(line[2:]), true
	}
	dot := strings.Index(line, ". ")
	if dot > 0 {
		prefix := line[:dot]
		if _, err := strconv.Atoi(prefix); err == nil {
			return "  • ", strings.TrimSpace(line[dot+2:]), true
		}
	}
	return "", "", false
}

func isRuleLine(line string) bool {
	if len(line) < 3 {
		return false
	}
	for _, r := range line {
		if r != '-' && r != '_' && r != '*' {
			return false
		}
	}
	return true
}

func (m Model) renderCodeBlock(lines []string) string {
	if len(lines) == 0 {
		return m.styles.Border.Render("┌──┐") + "\n" + m.styles.Border.Render("└──┘")
	}
	maxLen := 0
	for _, line := range lines {
		if len(line) > maxLen {
			maxLen = len(line)
		}
	}
	contentWidth := maxLen
	if m.width > 0 {
		limit := m.width - 4
		if limit > 0 && contentWidth > limit {
			contentWidth = limit
		}
	}
	horiz := strings.Repeat("─", contentWidth+2)
	var b strings.Builder
	b.WriteString(m.styles.Border.Render("┌" + horiz + "┐"))
	b.WriteString("\n")
	for _, line := range lines {
		text := truncateText(line, contentWidth)
		padding := contentWidth - len(text)
		if padding < 0 {
			padding = 0
		}
		b.WriteString(m.styles.Border.Render("│ "))
		b.WriteString(m.styles.Muted.Render(text))
		b.WriteString(strings.Repeat(" ", padding))
		b.WriteString(m.styles.Border.Render(" │"))
		b.WriteString("\n")
	}
	b.WriteString(m.styles.Border.Render("└" + horiz + "┘"))
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
	style := m.styles.Status
	if m.mode == modeTrash || m.mode == modeNote {
		style = m.styles.StatusAlt
	}
	if m.mode == modeReport {
		return style.Render(fmt.Sprintf("[bada] [%s] %s", modeLabel, m.status))
	}
	if m.mode == modeNote {
		target := ""
		if m.note != nil {
			target = m.note.target.label()
		}
		if target != "" {
			return style.Render(fmt.Sprintf("[bada] [%s] %s  %s", modeLabel, target, m.status))
		}
		return style.Render(fmt.Sprintf("[bada] [%s] %s", modeLabel, m.status))
	}
	if m.mode == modeTrash {
		sel := m.selectedTrashCount()
		total := len(m.trash)
		cur := 0
		if total > 0 {
			cur = m.trashCursor + 1
		}
		return style.Render(fmt.Sprintf("[bada] [%s] cur:%d/%d sel:%d path:%s  %s", modeLabel, cur, total, sel, m.store.TrashDir(), m.status))
	}
	total := len(m.visibleItems())
	cursor := 0
	if total > 0 {
		cursor = clampCursor(m.cursor, total) + 1
	}
	search := ""
	if m.searchActive() {
		search = fmt.Sprintf(" search:%q", m.searchQuery)
	}
	return style.Render(fmt.Sprintf("[bada] [%s] sort:%s%s  %d/%d  %s", modeLabel, m.sortMode, search, cursor, total, m.status))
}

func (m Model) fillView(body string) string {
	if m.height <= 0 {
		return body + m.renderStatusBar()
	}
	lines := countLines(body)
	target := m.height - 1
	if target < 0 {
		target = 0
	}
	if lines < target {
		body += strings.Repeat("\n", target-lines)
	}
	return body + m.renderStatusBar()
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func (m Model) ruleLine(width int) string {
	if width <= 0 {
		width = 24
	}
	return strings.Repeat("─", width)
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
	case modeSearch:
		return "SEARCH"
	case modeTrash:
		return "TRASH"
	case modeNote:
		return "NOTE"
	case modeReport:
		return "REPORT"
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

func (m Model) startSearch() (tea.Model, tea.Cmd) {
	m.mode = modeSearch
	m.input.SetValue(m.searchQuery)
	m.input.Placeholder = "Search tasks"
	m.input.Focus()
	m.status = "Search: type a query, Enter to apply, Esc to cancel"
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
			m.status = "Commands: help | sort (s then d/p/t/a/s) | rename (r) | priority +/- | due ]/[ | notes (enter view, e edit)"
		case "agenda":
			return m.enterReportView()
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

func (m Model) updateSearchMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel, "esc":
		m.mode = modeList
		m.input.Blur()
		m.status = "Search cancelled"
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		m.searchQuery = strings.TrimSpace(m.input.Value())
		m.mode = modeList
		m.input.Blur()
		if m.searchActive() {
			m.status = fmt.Sprintf("Search: %s", m.searchQuery)
		} else {
			m.status = "Search cleared"
		}
		m.cursor = clampCursor(0, len(m.visibleItems()))
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
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
				m.cursor = clampCursor(m.findVisibleTaskIndex(m.renameID), len(m.visibleItems()))
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
	return -1
}

func (m Model) findVisibleTaskIndex(id int) int {
	items := m.visibleItems()
	for i, it := range items {
		if it.kind == itemTask && it.task.ID == id {
			return i
		}
	}
	return -1
}

func (m Model) bumpPriority(delta int) (tea.Model, tea.Cmd) {
	t, ok := m.currentTask()
	if !ok {
		return m, nil
	}
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
	if idx := m.findTaskIndex(t.ID); idx >= 0 && idx < len(m.tasks) {
		m.tasks[idx].Priority = newPrio
	}
	m.pendingSort = true
	m.status = fmt.Sprintf("Priority set to %d", newPrio)
	return m, nil
}

func (m Model) shiftDue(days int) (tea.Model, tea.Cmd) {
	t, ok := m.currentTask()
	if !ok {
		return m, nil
	}
	if err := m.store.ShiftDue(t.ID, days); err != nil {
		m.status = fmt.Sprintf("shift due failed: %v", err)
		return m, nil
	}
	base := time.Now().UTC()
	if t.Due.Valid {
		base = t.Due.Time
	}
	newTime := base.AddDate(0, 0, days)
	if idx := m.findTaskIndex(t.ID); idx >= 0 && idx < len(m.tasks) {
		m.tasks[idx].Due = sql.NullTime{Time: newTime, Valid: true}
	}
	m.pendingSort = true
	m.status = fmt.Sprintf("Due shifted by %+dd", days)
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
	case "auto":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			a := m.tasks[i]
			b := m.tasks[j]
			if a.Done != b.Done {
				return !a.Done && b.Done
			}
			if a.Due.Valid && b.Due.Valid {
				if !a.Due.Time.Equal(b.Due.Time) {
					return a.Due.Time.Before(b.Due.Time)
				}
			} else if a.Due.Valid {
				return true
			} else if b.Due.Valid {
				return false
			}
			if a.Priority != b.Priority {
				return a.Priority > b.Priority
			}
			return a.ID < b.ID
		})
	case "state":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			a := m.tasks[i]
			b := m.tasks[j]
			if a.Done != b.Done {
				return !a.Done && b.Done
			}
			return a.ID < b.ID
		})
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

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func overdueBadge(t storage.Task) string {
	if !isOverdue(t) {
		return ""
	}
	days := int(time.Since(t.Due.Time).Hours()/24) + 1
	return fmt.Sprintf("[+%dd]", days)
}

func overdueDetail(t storage.Task) string {
	if !isOverdue(t) {
		return ""
	}
	days := int(time.Since(t.Due.Time).Hours()/24) + 1
	return fmt.Sprintf(" (overdue %dd)", days)
}

type recurrenceSpec struct {
	every   int
	unit    string
	weekday *time.Weekday
	label   string
}

func recurrenceBadge(t storage.Task) string {
	if !isRecurringTask(t) {
		return ""
	}
	if summary := recurrenceSummary(t); summary != "" {
		return fmt.Sprintf("[recur %s]", summary)
	}
	return "[recur]"
}

func recurrenceRuleLabel(t storage.Task) string {
	if spec, ok := parseRecurrenceSpec(t.RecurrenceRule); ok {
		return spec.label
	}
	rule := strings.TrimSpace(t.RecurrenceRule)
	if strings.ToLower(rule) == "none" {
		rule = ""
	}
	if rule == "" {
		return "recur"
	}
	return rule
}

func recurrenceSummary(t storage.Task) string {
	if !isRecurringTask(t) {
		return ""
	}
	if spec, ok := parseRecurrenceSpec(t.RecurrenceRule); ok {
		return spec.label
	}
	rule := strings.TrimSpace(t.RecurrenceRule)
	if rule == "" || strings.EqualFold(rule, "none") {
		rule = "custom"
	}
	if t.RecurrenceInterval > 0 {
		return fmt.Sprintf("%s/%dd", rule, t.RecurrenceInterval)
	}
	return rule
}

func isRecurringTask(t storage.Task) bool {
	rule := strings.ToLower(strings.TrimSpace(t.RecurrenceRule))
	return t.Recurring || (rule != "" && rule != "none")
}

func parseRecurrenceSpec(input string) (recurrenceSpec, bool) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return recurrenceSpec{}, false
	}
	everyRe := regexp.MustCompile(`(?i)^every\s*(\d+)?\s*(day|days|week|weeks|month|months)(?:\s+on\s+([a-z]+))?$`)
	dailyRe := regexp.MustCompile(`(?i)^(daily|weekly|monthly)(?:\s+on\s+([a-z]+))?$`)
	if m := everyRe.FindStringSubmatch(raw); m != nil {
		count := 1
		if strings.TrimSpace(m[1]) != "" {
			if v, err := strconv.Atoi(m[1]); err == nil && v > 0 {
				count = v
			}
		}
		unit := strings.ToLower(m[2])
		unit = strings.TrimSuffix(unit, "s")
		var weekday *time.Weekday
		if strings.TrimSpace(m[3]) != "" {
			if wd, ok := parseWeekday(m[3]); ok {
				weekday = &wd
			}
		}
		label := formatRecurrenceLabel(count, unit, weekday)
		return recurrenceSpec{every: count, unit: unit, weekday: weekday, label: label}, true
	}
	if m := dailyRe.FindStringSubmatch(raw); m != nil {
		unit := strings.ToLower(m[1])
		switch unit {
		case "daily":
			unit = "day"
		case "weekly":
			unit = "week"
		case "monthly":
			unit = "month"
		}
		var weekday *time.Weekday
		if strings.TrimSpace(m[2]) != "" {
			if wd, ok := parseWeekday(m[2]); ok {
				weekday = &wd
			}
		}
		label := formatRecurrenceLabel(1, unit, weekday)
		return recurrenceSpec{every: 1, unit: unit, weekday: weekday, label: label}, true
	}
	return recurrenceSpec{}, false
}

func formatRecurrenceLabel(every int, unit string, weekday *time.Weekday) string {
	unitLabel := unit
	if every == 1 {
		unitLabel = unit
	} else {
		unitLabel = unit + "s"
	}
	base := ""
	if every == 1 {
		base = "every " + unitLabel
	} else {
		base = fmt.Sprintf("every %d %s", every, unitLabel)
	}
	if weekday != nil {
		base += " on " + weekdayShort(*weekday)
	}
	return base
}

func parseWeekday(input string) (time.Weekday, bool) {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "mon", "monday":
		return time.Monday, true
	case "tue", "tues", "tuesday":
		return time.Tuesday, true
	case "wed", "wednesday":
		return time.Wednesday, true
	case "thu", "thur", "thurs", "thursday":
		return time.Thursday, true
	case "fri", "friday":
		return time.Friday, true
	case "sat", "saturday":
		return time.Saturday, true
	case "sun", "sunday":
		return time.Sunday, true
	default:
		return time.Sunday, false
	}
}

func weekdayShort(day time.Weekday) string {
	switch day {
	case time.Monday:
		return "Mon"
	case time.Tuesday:
		return "Tue"
	case time.Wednesday:
		return "Wed"
	case time.Thursday:
		return "Thu"
	case time.Friday:
		return "Fri"
	case time.Saturday:
		return "Sat"
	default:
		return "Sun"
	}
}

func nextRecurrenceDate(t storage.Task) (time.Time, bool) {
	if !isRecurringTask(t) {
		return time.Time{}, false
	}
	base, ok := recurrenceBaseDate(t)
	if !ok {
		return time.Time{}, false
	}
	now := time.Now().In(base.Location())
	rule := strings.TrimSpace(t.RecurrenceRule)
	useSpec := strings.HasPrefix(strings.ToLower(rule), "every")
	if spec, ok := parseRecurrenceSpec(rule); ok && (useSpec || t.RecurrenceInterval == 0) {
		return nextFromSpec(base, now, spec), true
	}
	if t.RecurrenceInterval > 0 {
		return nextByDays(base, now, t.RecurrenceInterval), true
	}
	if spec, ok := parseRecurrenceSpec(rule); ok {
		return nextFromSpec(base, now, spec), true
	}
	return time.Time{}, false
}

func recurrenceBaseDate(t storage.Task) (time.Time, bool) {
	switch {
	case t.Due.Valid:
		return normalizeDate(t.Due.Time), true
	case t.Start.Valid:
		return normalizeDate(t.Start.Time), true
	default:
		if t.CreatedAt.IsZero() {
			return time.Time{}, false
		}
		return normalizeDate(t.CreatedAt), true
	}
}

func normalizeDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func nextByDays(base, now time.Time, interval int) time.Time {
	base = normalizeDate(base)
	now = normalizeDate(now)
	if interval <= 0 {
		return base
	}
	if base.After(now) {
		return base
	}
	diffDays := int(now.Sub(base).Hours() / 24)
	steps := diffDays/interval + 1
	return base.AddDate(0, 0, steps*interval)
}

func nextFromSpec(base, now time.Time, spec recurrenceSpec) time.Time {
	switch spec.unit {
	case "day":
		return nextByDays(base, now, spec.every)
	case "week":
		if spec.weekday != nil {
			return nextWeeklyByWeekday(base, now, spec.every, *spec.weekday)
		}
		return nextByDays(base, now, spec.every*7)
	case "month":
		if spec.weekday != nil {
			return nextMonthlyByWeekday(base, now, spec.every, *spec.weekday)
		}
		return nextByMonths(base, now, spec.every)
	default:
		return base
	}
}

func nextWeeklyByWeekday(base, now time.Time, every int, weekday time.Weekday) time.Time {
	if every <= 0 {
		every = 1
	}
	base = normalizeDate(base)
	now = normalizeDate(now)
	weekStart := startOfWeek(base, time.Monday)
	nowWeekStart := startOfWeek(now, time.Monday)
	weeksSince := int(nowWeekStart.Sub(weekStart).Hours() / 24 / 7)
	if weeksSince < 0 {
		weeksSince = 0
	}
	adjust := weeksSince % every
	if adjust != 0 {
		weeksSince += every - adjust
	}
	for {
		candidateWeek := weekStart.AddDate(0, 0, weeksSince*7)
		candidate := candidateWeek.AddDate(0, 0, weekdayOffset(time.Monday, weekday))
		if candidate.After(now) {
			return candidate
		}
		weeksSince += every
	}
}

func nextByMonths(base, now time.Time, every int) time.Time {
	if every <= 0 {
		every = 1
	}
	base = normalizeDate(base)
	now = normalizeDate(now)
	candidate := base
	for !candidate.After(now) {
		candidate = candidate.AddDate(0, every, 0)
	}
	return candidate
}

func nextMonthlyByWeekday(base, now time.Time, every int, weekday time.Weekday) time.Time {
	if every <= 0 {
		every = 1
	}
	base = normalizeDate(base)
	now = normalizeDate(now)
	candidate := firstWeekdayOfMonth(base, weekday)
	for !candidate.After(now) {
		base = base.AddDate(0, every, 0)
		candidate = firstWeekdayOfMonth(base, weekday)
	}
	return candidate
}

func firstWeekdayOfMonth(date time.Time, weekday time.Weekday) time.Time {
	start := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location())
	offset := (int(weekday) - int(start.Weekday()) + 7) % 7
	return start.AddDate(0, 0, offset)
}

func startOfWeek(date time.Time, weekStart time.Weekday) time.Time {
	date = normalizeDate(date)
	offset := (int(date.Weekday()) - int(weekStart) + 7) % 7
	return date.AddDate(0, 0, -offset)
}

func weekdayOffset(weekStart, target time.Weekday) int {
	return (int(target) - int(weekStart) + 7) % 7
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
		m.status = "Sort: press d (due), p (priority), t (created), a (auto), s (state)"
		return true
	}
	if m.sortBuf == "s" {
		switch key {
		case "d":
			m.sortMode = "due"
			m.sortTasks()
			m.pendingSort = false
			m.status = "Sorted by due date"
		case "p":
			m.sortMode = "priority"
			m.sortTasks()
			m.pendingSort = false
			m.status = "Sorted by priority"
		case "t":
			m.sortMode = "created"
			m.sortTasks()
			m.pendingSort = false
			m.status = "Sorted by created time"
		case "a":
			m.sortMode = "auto"
			m.sortTasks()
			m.pendingSort = false
			m.status = "Sorted by auto (state/priority/due)"
		case "s":
			m.sortMode = "state"
			m.sortTasks()
			m.pendingSort = false
			m.status = "Sorted by state (pending first)"
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

func (m *Model) processNavKey(key string) bool {
	if key == "" {
		return false
	}
	if m.pendingSort && key != "+" && key != "-" {
		m.flushPendingSort(key)
	}
	if key == "g" {
		if m.navBuf == "g" {
			m.cursor = 0
			m.navBuf = ""
			m.status = "Top"
		} else {
			m.navBuf = "g"
			m.status = "g (press g for top)"
		}
		return true
	}
	if m.navBuf == "g" {
		m.navBuf = ""
	}
	// capital G
	if key == "G" {
		items := m.visibleItems()
		if len(items) > 0 {
			m.cursor = len(items) - 1
			m.status = "Bottom"
		}
		return true
	}
	return false
}

func humanDone(done bool) string {
	if done {
		return "✓"
	}
	return "⏳"
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
		if r == '-' || r == '_' || r == '/' || r == ',' || r == ' ' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
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

type topicStat struct {
	overdue int
	total   int
}

func (m Model) recentlyAdded(limit int) []storage.Task {
	cp := append([]storage.Task{}, m.tasks...)
	sort.SliceStable(cp, func(i, j int) bool {
		return cp[i].CreatedAt.After(cp[j].CreatedAt)
	})
	if len(cp) > limit {
		cp = cp[:limit]
	}
	return cp
}

func (m Model) recentlyDone(limit int) []storage.Task {
	var done []storage.Task
	for _, t := range m.tasks {
		if t.Done {
			done = append(done, t)
		}
	}
	sort.SliceStable(done, func(i, j int) bool {
		ai := done[i].CompletedAt
		aj := done[j].CompletedAt
		if ai.Valid && aj.Valid {
			return ai.Time.After(aj.Time)
		}
		if ai.Valid {
			return true
		}
		if aj.Valid {
			return false
		}
		return done[i].ID > done[j].ID
	})
	if len(done) > limit {
		done = done[:limit]
	}
	return done
}

func (m Model) countOverdue(list []storage.Task) int {
	now := time.Now()
	n := 0
	for _, t := range list {
		if t.Done || !t.Due.Valid {
			continue
		}
		if now.After(t.Due.Time) {
			n++
		}
	}
	return n
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

func intervalString(v int) string {
	if v <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", v)
}

func (m Model) visibleItems() []listItem {
	if m.searchActive() {
		return m.searchItems()
	}
	return m.defaultVisibleItems()
}

func (m Model) defaultVisibleItems() []listItem {
	items := make([]listItem, 0)
	if m.currentTopic == "" {
		for _, topic := range []string{"RecentlyAdded", "RecentlyDone"} {
			items = append(items, listItem{kind: itemTopic, topic: topic})
		}
		for _, topic := range m.sortedTopics() {
			items = append(items, listItem{kind: itemTopic, topic: topic})
		}
		for _, t := range m.tasks {
			if len(t.Topics) == 0 {
				items = append(items, listItem{kind: itemTask, task: t})
			}
		}
		return items
	}

	switch m.currentTopic {
	case "RecentlyAdded":
		for _, t := range m.recentlyAdded(m.recentLimit) {
			items = append(items, listItem{kind: itemTask, task: t})
		}
	case "RecentlyDone":
		for _, t := range m.recentlyDone(m.recentLimit) {
			items = append(items, listItem{kind: itemTask, task: t})
		}
	default:
		for _, t := range m.tasks {
			if taskHasTopic(t, m.currentTopic) {
				items = append(items, listItem{kind: itemTask, task: t})
			}
		}
	}
	return items
}

func (m Model) searchItems() []listItem {
	query := strings.TrimSpace(m.searchQuery)
	if query == "" {
		return m.defaultVisibleItems()
	}
	q := strings.ToLower(query)
	items := make([]listItem, 0)
	var candidates []storage.Task
	switch {
	case m.currentTopic == "RecentlyAdded":
		candidates = m.recentlyAdded(m.recentLimit)
	case m.currentTopic == "RecentlyDone":
		candidates = m.recentlyDone(m.recentLimit)
	case m.currentTopic != "":
		for _, t := range m.tasks {
			if taskHasTopic(t, m.currentTopic) {
				candidates = append(candidates, t)
			}
		}
	default:
		candidates = m.tasks
	}
	for _, t := range candidates {
		if taskMatchesQuery(t, q) {
			items = append(items, listItem{kind: itemTask, task: t, topic: strings.Join(t.Topics, ",")})
		}
	}
	return items
}

func (m Model) searchActive() bool {
	return strings.TrimSpace(m.searchQuery) != ""
}

func taskMatchesQuery(t storage.Task, query string) bool {
	fields := []string{t.Title, strings.Join(t.Topics, " "), t.Tags}
	if t.Due.Valid {
		fields = append(fields, t.Due.Time.Format("2006-01-02"))
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func taskHasTopic(t storage.Task, topic string) bool {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return false
	}
	for _, tpc := range t.Topics {
		if tpc == topic {
			return true
		}
	}
	return false
}

func uniqueTopics(topics []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		if _, ok := seen[topic]; ok {
			continue
		}
		seen[topic] = struct{}{}
		out = append(out, topic)
	}
	return out
}

func (m Model) topicStats() map[string]topicStat {
	stats := make(map[string]topicStat)
	for _, t := range m.tasks {
		if len(t.Topics) == 0 {
			continue
		}
		overdue := isOverdue(t)
		for _, topic := range uniqueTopics(t.Topics) {
			stat := stats[topic]
			stat.total++
			if overdue {
				stat.overdue++
			}
			stats[topic] = stat
		}
	}
	return stats
}

func (m Model) sortedTopics() []string {
	set := map[string]struct{}{}
	for _, t := range m.tasks {
		if len(t.Topics) == 0 {
			continue
		}
		for _, topic := range uniqueTopics(t.Topics) {
			set[topic] = struct{}{}
		}
	}
	topics := make([]string, 0, len(set))
	for k := range set {
		topics = append(topics, k)
	}
	sort.Strings(topics)
	return topics
}

func isSpecialTopic(topic string) bool {
	return topic == "RecentlyAdded" || topic == "RecentlyDone"
}

func (m Model) currentTopicItem() (string, bool) {
	items := m.visibleItems()
	if len(items) == 0 {
		return "", false
	}
	if m.cursor < 0 || m.cursor >= len(items) {
		return "", false
	}
	it := items[m.cursor]
	if it.kind != itemTopic {
		return "", false
	}
	return it.topic, true
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
