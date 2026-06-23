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
	"github.com/mattn/go-runewidth"

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
	modeConfig
	modeSearch
	modeTrash
	modeNote
	modeReport
	modeCalendar
	modeHelp
	modeGantt
	modeStats
)

type noteKind int

const (
	noteTask noteKind = iota
	noteTopic
)

type configStage int

const (
	configStagePath configStage = iota
	configStageDB
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
	Title       lipgloss.Style
	Heading     lipgloss.Style
	Accent      lipgloss.Style
	Muted       lipgloss.Style
	Border      lipgloss.Style
	Selection   lipgloss.Style
	Done        lipgloss.Style
	Danger      lipgloss.Style
	Warning     lipgloss.Style
	Success     lipgloss.Style
	Status      lipgloss.Style
	StatusAlt   lipgloss.Style
	Panel       lipgloss.Style // rounded border for framed panels
	PanelTitle  lipgloss.Style // title text shown in a panel's top border
	TableHeader lipgloss.Style // colored column-header bar
	KeyCap      lipgloss.Style // highlighted key glyph in hint bars
	KeyLabel    lipgloss.Style // muted label following a KeyCap
}

type metaState struct {
	taskID        int
	title         string
	topic         string
	tags          string
	assignee      string
	reporter      string
	priority      string
	due           string
	start         string
	end           string
	timezone      string
	rule          string
	interval      string
	notes         string
	notesOrig     string // notes as loaded, to detect real edits (the modal field is single-line)
	recurring     bool
	index         int
	completions   []string
	completionIdx int
	lastInput     string
	expanded      bool   // detail fields revealed in the Create/Edit modal
	validation    string // inline validation message shown in the modal
	dueComponent  int    // which part of the Due stepper is selected (0=Y..4=min)
	dueTyping     string // digits typed into the active Due component (for direct entry)
}

// fieldMore is a sentinel "index" for the modal's expand/collapse toggle row.
const fieldMore = -1

// metaCoreFields are the field indices shown by default in the modal:
// Title, Topics, Priority, Due.
func metaCoreFields() []int { return []int{0, 1, 3, 4} }

// metaDetailFields are revealed when the modal is expanded:
// Tags, Assignee, Reporter, Start, End, Timezone, Recurrence, Interval, Notes.
func metaDetailFields() []int { return []int{2, 5, 6, 7, 8, 9, 10, 11, 12} }

// order returns the navigable rows in the modal, in display order. The
// expand/collapse toggle (fieldMore) always sits between the core fields and
// the (optional) detail fields.
func (ms metaState) order() []int {
	order := append([]int{}, metaCoreFields()...)
	order = append(order, fieldMore)
	if ms.expanded {
		order = append(order, metaDetailFields()...)
	}
	return order
}

// orderPos is the position of the current row within order().
func (ms metaState) orderPos() int {
	for i, f := range ms.order() {
		if f == ms.index {
			return i
		}
	}
	return 0
}

// metaShortLabel is the compact field label used in the modal.
func metaShortLabel(idx int) string {
	switch idx {
	case 0:
		return "Title"
	case 1:
		return "Topic"
	case 2:
		return "Tags"
	case 3:
		return "Priority"
	case 4:
		return "Due"
	case 5:
		return "Assignee"
	case 6:
		return "Reporter"
	case 7:
		return "Start"
	case 8:
		return "End"
	case 9:
		return "Timezone"
	case 10:
		return "Recurs"
	case 11:
		return "Interval"
	case 12:
		return "Notes"
	default:
		return ""
	}
}

type Model struct {
	store          *storage.Store
	cfg            config.Config
	configPath     string
	tasks          []storage.Task
	trash          []storage.TrashEntry
	cursor         int
	navBuf         string
	trashCursor    int
	mode           mode
	report         string
	recentLimit    int
	input          textinput.Model
	status         string
	filterDone     string
	sortMode       string
	sortBuf        string
	pendingSort    bool
	currentTopic   string
	searchQuery    string
	styles         uiStyles
	width          int
	height         int
	noteScroll     int
	noteConfirm    bool
	notePending    noteTarget
	confirmDel     bool
	pendingDel     *storage.Task
	pendingBatch   []storage.Task
	reportScroll   int
	trashScroll    int
	confirmTopic   bool
	pendingTopic   string
	trashSelected  map[int]bool
	trashConfirm   bool
	trashPending   []storage.TrashEntry
	selectedTasks  map[int]bool
	meta           *metaState
	note           *noteState
	renameID       int
	renameTopic    string
	renameIsTopic  bool
	calendarMonth  time.Time
	calendarDay    time.Time
	calendarDetail bool
	helpScroll     int
	ganttScroll    int
	statsScroll    int
	configStage    configStage
	pendingCfgPath string
	pendingDBPath  string
}

func Run(store *storage.Store, cfg config.Config, configPath string, firstLaunch bool) error {
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
		configPath:    configPath,
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
	if firstLaunch {
		m, _ = m.startConfig()
	}

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
		if m.mode == modeCalendar {
			return m.updateCalendarMode(msg.String())
		}
		if m.mode == modeHelp {
			return m.updateHelpMode(msg.String())
		}
		if m.mode == modeGantt {
			return m.updateGanttMode(msg.String())
		}
		if m.mode == modeStats {
			return m.updateStatsMode(msg.String())
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
		if m.mode == modeConfig {
			return m.updateConfigMode(msg.String(), msg)
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
		taskID, err := m.store.AddTask(title)
		if err != nil {
			m.status = fmt.Sprintf("save failed: %v", err)
			return m, nil
		}
		m.tasks, err = m.store.FetchTasks()
		if err != nil {
			m.status = fmt.Sprintf("reload failed: %v", err)
			return m, nil
		}
		m.sortTasks()
		m.input.SetValue("")
		m.input.Blur()
		m.mode = modeList
		if idx := m.findTaskIndex(taskID); idx >= 0 {
			return m.startMetadataEdit(m.tasks[idx])
		}
		m.status = "Added task"
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
		return m.startMetadataAdd()
	case m.cfg.Keys.Toggle:
		task, ok := m.currentTask()
		if !ok {
			return m, nil
		}
		nextStatus := nextTaskStatus(task)
		err := m.store.SetStatus(task.ID, nextStatus)
		if err != nil {
			m.status = fmt.Sprintf("status failed: %v", err)
			return m, nil
		}
		m.tasks, err = m.store.FetchTasks()
		if err == nil {
			m.sortTasks()
			vis = m.visibleItems()
			m.cursor = clampCursor(m.cursor, len(vis))
			m.status = "Status: " + nextStatus
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
			m = m.deleteTasksToTrash(selected)
			m.selectedTasks = map[int]bool{}
			return m, nil
		}
		task, ok := m.currentTask()
		if !ok {
			return m, nil
		}
		m = m.deleteTasksToTrash([]storage.Task{task})
	case m.cfg.Keys.DeleteAllDone:
		m.confirmDel = true
		m.pendingDel = nil
		m.status = "Delete ALL done tasks? y/n"
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
		info := fmt.Sprintf("Task #%d • %s • %s", task.ID, task.Title, taskStatusLabel(task))
		if len(task.Topics) > 0 {
			info += " • topics:" + strings.Join(task.Topics, ",")
		}
		if strings.TrimSpace(task.Tags) != "" {
			info += " • tags:" + task.Tags
		}
		if strings.TrimSpace(task.Assignee) != "" {
			info += " • assignee:" + task.Assignee
		}
		if strings.TrimSpace(task.Reporter) != "" {
			info += " • reporter:" + task.Reporter
		}
		if task.Priority != 0 {
			info += fmt.Sprintf(" • priority:%d", task.Priority)
		}
		if task.Due.Valid {
			info += " • due:" + formatDateTime(task.Due) + overdueDetail(task)
		}
		if task.Start.Valid {
			info += " • start:" + task.Start.Time.Format("2006-01-02")
		}
		if task.End.Valid {
			info += " • end:" + formatDateTime(task.End)
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
	case "?":
		return m.enterHelpView()
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

	if m.meta != nil {
		return m.fillView(m.renderMetaModalView())
	}

	if m.mode == modeNote {
		b.WriteString(m.renderNoteView())
		b.WriteString("\n\n")
		return m.fillView(b.String())
	}

	if m.mode == modeReport {
		header := m.renderReportHeader()
		footer := m.renderReportFooter()
		gap := "\n"
		tail := ""
		bodyMax := 0
		if m.height > 0 {
			available := m.height - 1
			bodyMax = available - countLines(header) - countLines(footer) - countLines(gap) - countLines(tail)
			if bodyMax < 0 {
				bodyMax = 0
			}
		}
		b.WriteString(header)
		if m.height > 0 {
			b.WriteString(m.renderReportWithHeight(bodyMax))
		} else {
			b.WriteString(m.report)
		}
		b.WriteString(gap)
		b.WriteString(footer)
		b.WriteString(tail)
		return m.fillView(b.String())
	}

	if m.mode == modeCalendar {
		b.WriteString(m.renderCalendarView())
		return m.fillView(b.String())
	}

	if m.mode == modeHelp {
		b.WriteString(m.renderHelpView())
		return m.fillView(b.String())
	}

	if m.mode == modeTrash {
		b.WriteString(m.renderTrashView())
		return m.fillView(b.String())
	}

	if m.mode == modeGantt {
		b.WriteString(m.renderGanttView())
		return m.fillView(b.String())
	}

	if m.mode == modeStats {
		b.WriteString(m.renderStatsView())
		return m.fillView(b.String())
	}

	footer := strings.TrimRight(m.renderFooterPanel(), "\n")
	showHints := m.mode == modeList && m.meta == nil
	hints := ""
	if showHints {
		hints = m.hintBar(m.listHints())
	}

	// Bottom block: the Detail pane (and key hints), pinned to the screen bottom.
	bottom := footer
	if showHints {
		bottom += "\n" + hints
	}

	if m.height <= 0 {
		top := m.panel("bada · Tasks", m.renderTaskPaneBody(-1))
		return m.fillView(top + "\n" + bottom)
	}

	// Make the Tasks panel the large upper box and leave only a single separator
	// before the bottom Detail pane.
	bottomLines := strings.Split(bottom, "\n")
	bodyLines := (m.height - 1) - len(bottomLines) - 1 - 2 // separator + panel borders
	if bodyLines < 1 {
		bodyLines = 1
	}
	top := m.panel("bada · Tasks", m.renderTaskPaneBody(bodyLines))
	lines := strings.Split(top, "\n")
	lines = append(lines, "")
	lines = append(lines, bottomLines...)
	return m.fillView(strings.Join(lines, "\n"))
}

// listHints returns the key-hint chips shown beneath the task list.
func (m Model) listHints() []keyHint {
	k := m.cfg.Keys
	return []keyHint{
		{k.Quit, "quit"},
		{k.Add, "add"},
		{k.Search, "search"},
		{k.Detail, "detail"},
		{k.Edit, "edit"},
		{"T", "trash"},
		{":", "cmd"},
		{"?", "help"},
	}
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
	case modeConfig:
		if m.configStage == configStageDB {
			b.WriteString(m.styles.Heading.Render("DB path: "))
		} else {
			b.WriteString(m.styles.Heading.Render("Config path: "))
		}
		b.WriteString(m.input.View())
		return b.String()
	case modeSearch:
		b.WriteString(m.styles.Heading.Render("Search: "))
		b.WriteString(m.input.View())
		return b.String()
	default:
		// List view: show the current task's details in its own framed pane.
		return m.panel("bada · Detail", strings.TrimRight(m.renderMetadataPanel(), "\n"))
	}
}

func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			b.WriteString(line)
			b.WriteString("\n")
			line = word
			continue
		}
		line += " " + word
	}
	b.WriteString(line)
	return b.String()
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

func (m Model) deleteTasksToTrash(tasks []storage.Task) Model {
	if len(tasks) == 0 {
		return m
	}
	deleted := 0
	for _, task := range tasks {
		if err := m.store.DeleteTask(task.ID); err != nil {
			m.status = fmt.Sprintf("delete failed: %v", err)
			return m
		}
		deleted++
	}
	var err error
	m.tasks, err = m.store.FetchTasks()
	if err != nil {
		m.status = fmt.Sprintf("reload failed: %v", err)
		return m
	}
	m.sortTasks()
	m.cursor = clampCursor(m.cursor, len(m.visibleItems()))
	if deleted == 1 {
		m.status = "Deleted task (moved to trash)"
	} else {
		m.status = fmt.Sprintf("Deleted %d task(s) (moved to trash)", deleted)
	}
	return m
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
	m.ganttScroll = 0
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
	if m.processScrollKey(key, m.reportMaxScroll(), &m.reportScroll) {
		return m, nil
	}
	switch key {
	case "esc", "enter", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Report closed"
		return m, nil
	case ":":
		return m.startCommand()
	case m.cfg.Keys.Up, "up":
		if m.reportScroll > 0 {
			m.reportScroll--
		}
	case m.cfg.Keys.Down, "down":
		m.reportScroll = clampInt(m.reportScroll+1, 0, m.reportMaxScroll())
	default:
		return m, nil
	}
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
	if m.processScrollKey(key, m.ganttMaxScroll(), &m.ganttScroll) {
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		m.mode = modeList
		m.status = "Gantt closed"
		return m, nil
	case m.cfg.Keys.Up, "up":
		if m.ganttScroll > 0 {
			m.ganttScroll--
		}
	case m.cfg.Keys.Down, "down":
		m.ganttScroll = clampInt(m.ganttScroll+1, 0, m.ganttMaxScroll())
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
		title = "bada · Calendar — " + m.calendarDay.Format("Mon, Jan 2, 2006")
		body = m.renderCalendarDayList()
	} else {
		title = "bada · Calendar — " + m.calendarMonth.Format("January 2006")
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
	for _, name := range dayNames {
		b.WriteString(padRightWidth(name, cellWidth))
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
	if isSameDate(day, time.Now()) {
		headerStyle = m.styles.Warning
		taskStyle = m.styles.Warning
	}
	if isSameDate(day, m.calendarDay) {
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
	for i := 0; i < showCount; i++ {
		text := "• " + truncateTextWidth(tasks[i].Title, width-2)
		lines = append(lines, taskStyle.Render(padRightWidth(text, width)))
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

func (m Model) renderCalendarDayList() string {
	tasks := m.tasksForDay(m.calendarDay)
	if len(tasks) == 0 {
		return m.styles.Muted.Render("(no tasks)")
	}
	var b strings.Builder
	for _, t := range tasks {
		due := "no due"
		if t.Due.Valid {
			due = formatDateTime(t.Due)
		}
		line := fmt.Sprintf("  • #%d %-40s  %s", t.ID, truncateText(t.Title, 40), due)
		if rec := recurrenceSummary(t); rec != "" {
			line += " [" + rec + "]"
		}
		b.WriteString(line)
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

func padRight(text string, width int) string {
	if len(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len(text))
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

func (m Model) renderReportHeader() string {
	var b strings.Builder
	b.WriteString(m.renderListBanner())
	b.WriteString("\n\n")
	b.WriteString(m.styles.Accent.Render("#### Reminder Report ####"))
	b.WriteString("\n\n")
	return b.String()
}

func (m Model) renderReportFooter() string {
	return m.styles.Muted.Render("Press enter/esc/q to close, : for commands")
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
	return strings.Join(lines[scroll:end], "\n")
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
	return m.panel("bada · Help", body) + "\n" + footer
}

func (m Model) helpContent() string {
	return strings.TrimRight(fmt.Sprintf(`Commands:
  :agenda    Open reminder report
  :calendar  Open calendar view
  :gantt     Open gantt timeline
  :stats     Open productivity stats
  :config    Update config and db paths
  :help / ?  Open this help screen
  :q / :quit Quit bada

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

`, m.cfg.Keys.Up, m.cfg.Keys.Down, m.cfg.Keys.Search, m.cfg.Keys.Quit, m.cfg.Keys.Add, m.cfg.Keys.Toggle, m.cfg.Keys.Delete, m.cfg.Keys.Edit, m.cfg.Keys.NoteView, m.cfg.Keys.Delete, m.cfg.Keys.DeleteAllDone), "\n")
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
	return m.panel("bada · Stats", body) + "\n" + footer
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
	return m.panel("bada · Trash", body) + "\n" + footer
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
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "scroll"},
		{m.cfg.Keys.Cancel, "close"},
		{m.cfg.Keys.Quit, "quit"},
	})
}

func (m Model) renderGanttView() string {
	footer := m.ganttFooter()
	headerLines := m.renderGanttHeaderLines()
	headerCount := 0
	if strings.TrimSpace(headerLines) != "" {
		headerCount = countLines(headerLines)
	}
	bodyMax := 0
	if m.height > 0 {
		bodyMax = m.height - 1 - 2 - countLines(footer) - headerCount // 2 = panel borders
		if bodyMax < 1 {
			bodyMax = 1
		}
	}
	var rows string
	if m.height > 0 {
		rows = m.renderGanttBody(bodyMax)
	} else {
		rows = m.ganttContent()
	}
	body := rows
	if headerCount > 0 {
		body = headerLines + "\n" + rows
	}
	return m.panel("bada · Gantt", body) + "\n" + footer
}

func (m Model) renderGanttHeaderLines() string {
	_, header := m.ganttDataRowsWithHeader()
	if strings.TrimSpace(header) == "" {
		return ""
	}
	parts := strings.Split(header, "\n")
	lines := make([]string, 0, len(parts))
	for i, part := range parts {
		if i == 0 {
			lines = append(lines, m.styles.Heading.Render(part))
			continue
		}
		lines = append(lines, m.styles.Border.Render(part))
	}
	return strings.Join(lines, "\n")
}

func (m Model) ganttContent() string {
	rows := m.ganttRows()
	if len(rows) == 0 {
		return m.styles.Muted.Render("(no tasks with due dates)")
	}
	return strings.Join(rows, "\n")
}

func (m Model) ganttMaxScroll() int {
	if m.height <= 0 {
		return 0
	}
	headerLines := m.renderGanttHeaderLines()
	headerCount := 0
	if strings.TrimSpace(headerLines) != "" {
		headerCount = countLines(headerLines)
	}
	bodyMax := m.height - 1 - 2 - countLines(m.ganttFooter()) - headerCount // 2 = panel borders
	if bodyMax <= 0 {
		return 0
	}
	rows := m.ganttRows()
	if len(rows) <= bodyMax {
		return 0
	}
	return len(rows) - bodyMax
}

func (m Model) renderGanttBody(maxLines int) string {
	rows := m.ganttRows()
	if maxLines <= 0 {
		return ""
	}
	if len(rows) == 0 {
		return m.styles.Muted.Render("(no tasks with due dates)")
	}
	maxScroll := len(rows) - maxLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := clampInt(m.ganttScroll, 0, maxScroll)
	end := scroll + maxLines
	if end > len(rows) {
		end = len(rows)
	}
	return strings.Join(rows[scroll:end], "\n")
}

func (m Model) ganttRows() []string {
	rows, _ := m.ganttDataRowsWithHeader()
	return rows
}

func (m Model) ganttDataRowsWithHeader() ([]string, string) {
	type ganttItem struct {
		task  storage.Task
		start time.Time
		due   time.Time
	}
	items := make([]ganttItem, 0)
	for _, t := range m.tasks {
		if isDone(t) || !t.Due.Valid {
			continue
		}
		start := t.CreatedAt
		if t.Start.Valid {
			start = t.Start.Time
		}
		start = normalizeDate(start)
		due := normalizeDate(t.Due.Time)
		if due.Before(start) {
			start = due
		}
		items = append(items, ganttItem{task: t, start: start, due: due})
	}
	if len(items) == 0 {
		barWidth := 40
		if m.width > 0 {
			barWidth = clampInt(m.panelInnerWidth()-52, 20, 60)
		}
		fallbackStart := normalizeDate(time.Now())
		header := buildGanttHeader(fallbackStart, 14, barWidth)
		return nil, header
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].start.Equal(items[j].start) {
			return items[i].due.Before(items[j].due)
		}
		return items[i].start.Before(items[j].start)
	})
	minDate := items[0].start
	maxDate := items[0].due
	for _, it := range items[1:] {
		if it.start.Before(minDate) {
			minDate = it.start
		}
		if it.due.After(maxDate) {
			maxDate = it.due
		}
	}
	spanDays := int(maxDate.Sub(minDate).Hours()/24) + 1
	if spanDays < 1 {
		spanDays = 1
	}
	barWidth := 40
	if m.width > 0 {
		barWidth = clampInt(m.panelInnerWidth()-52, 20, 60)
	}
	rows := make([]string, 0, len(items))
	header := buildGanttHeader(minDate, spanDays, barWidth)
	today := normalizeDate(time.Now())
	for _, it := range items {
		title := truncateText(it.task.Title, 24)
		bar := renderGanttBar(minDate, spanDays, barWidth, it.start, it.due, today)
		line := fmt.Sprintf("%-4d %-24s %-10s %-10s %s", it.task.ID, title, it.start.Format("2006-01-02"), it.due.Format("2006-01-02"), bar)
		rows = append(rows, line)
	}
	return rows, header
}

func buildGanttHeader(start time.Time, spanDays, barWidth int) string {
	scaleLine, labelLine := renderGanttScaleLines(start, spanDays, barWidth)
	return fmt.Sprintf("%-4s %-24s %-10s %-10s %s\n%-4s %-24s %-10s %-10s %s",
		"ID", "Title", "Start", "Due", scaleLine,
		"", "", "", "", labelLine,
	)
}

func renderGanttScaleLines(start time.Time, spanDays, width int) (string, string) {
	if width <= 0 {
		return "", ""
	}
	if spanDays <= 1 {
		return strings.Repeat("─", width), padRight(start.Format("Jan _2"), width)
	}
	scaleRunes := []rune(strings.Repeat("─", width))
	for d := 0; d <= spanDays; d += 7 {
		pos := (d * width) / spanDays
		if pos >= width {
			pos = width - 1
		}
		scaleRunes[pos] = '┬'
	}
	scaleLine := string(scaleRunes)
	labelLine := renderGanttLabelLine(start, spanDays, width)
	return scaleLine, labelLine
}

func renderGanttLabelLine(start time.Time, spanDays, width int) string {
	positions := []int{0, width / 2, width - 6}
	labels := []string{
		start.Format("Jan _2"),
		start.AddDate(0, 0, spanDays/2).Format("Jan _2"),
		start.AddDate(0, 0, spanDays).Format("Jan _2"),
	}
	line := []rune(strings.Repeat(" ", width))
	for i, pos := range positions {
		if pos < 0 || pos >= width {
			continue
		}
		label := []rune(labels[i])
		for j := 0; j < len(label) && pos+j < width; j++ {
			line[pos+j] = label[j]
		}
	}
	return string(line)
}

func renderGanttBar(start time.Time, spanDays, width int, taskStart, taskDue, today time.Time) string {
	if width <= 0 {
		return ""
	}
	startOffset := int(taskStart.Sub(start).Hours() / 24)
	endOffset := int(taskDue.Sub(start).Hours() / 24)
	if startOffset < 0 {
		startOffset = 0
	}
	if endOffset < startOffset {
		endOffset = startOffset
	}
	if endOffset >= spanDays {
		endOffset = spanDays - 1
	}
	startPos := (startOffset * width) / spanDays
	endPos := (endOffset * width) / spanDays
	if endPos < startPos {
		endPos = startPos
	}
	todayOffset := int(today.Sub(start).Hours() / 24)
	todayPos := (todayOffset * width) / spanDays
	var b strings.Builder
	for i := 0; i < width; i++ {
		switch {
		case i == startPos || i == endPos:
			b.WriteRune('│')
		case i > startPos && i < endPos:
			b.WriteRune('─')
		default:
			b.WriteRune(' ')
		}
	}
	bar := []rune(b.String())
	if todayPos >= 0 && todayPos < width {
		bar[todayPos] = '•'
	}
	return string(bar)
}

func renderHelp(k config.Keymap) string {
	return fmt.Sprintf("%s/%s move • %s add • space select • %s/%s detail • %s done • %s purge • %s edit • %s notes • %s rename • %s/%s prio • %s/%s due • %s/%s/%s sort • %s trash • %s search • %s quit",
		k.Up, k.Down, k.Add, k.Detail, k.Confirm, k.Toggle, k.Delete, k.Edit, k.NoteView, k.Rename, k.PriorityUp, k.PriorityDown, k.DueForward, k.DueBack, k.SortDue, k.SortPriority, k.SortCreated, k.Trash, k.Search, k.Quit)
}

func (m Model) renderTaskList() string {
	return m.renderTaskListWithHeight(-1)
}

func (m Model) renderTaskPaneBody(maxLines int) string {
	legend := m.legendBar()
	if maxLines <= 0 {
		return m.renderTaskList() + "\n" + legend
	}

	legendLines := countLines(legend)
	listMax := maxLines - legendLines
	if listMax < 1 {
		return m.renderTaskListWithHeight(maxLines)
	}

	lines := strings.Split(m.renderTaskListWithHeight(listMax), "\n")
	for len(lines) < listMax {
		lines = append(lines, "")
	}
	lines = append(lines, strings.Split(legend, "\n")...)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderTaskListWithHeight(maxLines int) string {
	items := m.visibleItems()
	inner := m.panelInnerWidth()

	statusW := 11
	assigneeW := 10
	topicW := 12
	titleW := inner - 65
	if titleW < 10 {
		titleW = 10
	}
	if titleW > 42 {
		titleW = 42
	}

	full := func(style lipgloss.Style, s string) string {
		return style.Width(inner).MaxWidth(inner).Render(s)
	}

	lines := make([]string, 0)
	if m.searchActive() {
		search := fmt.Sprintf(" Search: %q (%d result(s))", m.searchQuery, len(items))
		lines = append(lines, full(m.styles.Accent, search))
	}
	header := fmt.Sprintf("  %-*s %-*s %-*s %-4s %-16s  %-*s", statusW, "Status", titleW, "Title", assigneeW, "Assignee", "Pri", "Due", topicW, "Topic")
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
			status := taskStatusLabel(it.task)
			assignee := truncateText(emptyDash(it.task.Assignee), assigneeW)
			topic := truncateText(topicListLabel(it.task.Topics), topicW)
			due := displayDate(it.task.Due)
			if due == "" {
				due = "pending"
			}
			pri := fmt.Sprintf("P%d", it.task.Priority)
			body := fmt.Sprintf("  %-*s %-*s %-*s %-4s %-16s  %-*s", statusW, status, titleW, title, assigneeW, assignee, pri, due, topicW, topic)

			recBadge := recurrenceBadge(it.task)
			if selected {
				if recBadge != "" {
					body += "  " + recBadge
				}
				itemLines = append(itemLines, full(m.styles.Selection, body))
				continue
			}
			if recBadge != "" {
				body += "  " + m.styles.Warning.Render(recBadge)
			}
			switch {
			case m.isTaskSelected(it.task.ID):
				body = full(m.styles.Warning, body)
			case isDone(it.task):
				body = full(m.styles.Done, body)
			default:
				body = full(lipgloss.NewStyle(), body)
			}
			itemLines = append(itemLines, body)
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
		m.styles.Accent.Render(m.note.target.label()),
		"",
	}
	metaLines := m.noteMetaBlockLines()
	if len(metaLines) > 0 {
		headerLines = append(headerLines, metaLines...)
		headerLines = append(headerLines, m.noteMetaSeparator(), "")
	}
	footerLine := m.styles.Muted.Render(fmt.Sprintf("Press %s/%s/enter to close, %s to edit, d to delete note",
		m.cfg.Keys.Cancel, m.cfg.Keys.Quit, m.cfg.Keys.Edit))

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
	selectionFg := theme.SelectionFg
	if strings.TrimSpace(selectionFg) == "" {
		selectionFg = theme.StatusFg
	}
	styles.Selection = applyFg(styles.Selection, selectionFg)

	styles.Status = applyBg(styles.Status, theme.StatusBg)
	styles.Status = applyFg(styles.Status, theme.StatusFg)
	styles.StatusAlt = applyBg(styles.StatusAlt, theme.StatusAltBg)
	styles.StatusAlt = applyFg(styles.StatusAlt, theme.StatusAltFg)

	// Structural styles derived from the theme so they stay configurable
	// while giving bada a cohesive, taskdog-like framed look.
	styles.Panel = applyFg(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()), theme.Border)
	styles.PanelTitle = applyFg(lipgloss.NewStyle().Bold(true), theme.Accent)

	styles.TableHeader = lipgloss.NewStyle().Bold(true)
	styles.TableHeader = applyBg(styles.TableHeader, theme.StatusAltBg)
	styles.TableHeader = applyFg(styles.TableHeader, theme.StatusAltFg)

	styles.KeyCap = lipgloss.NewStyle().Bold(true)
	styles.KeyCap = applyBg(styles.KeyCap, theme.StatusAltBg)
	styles.KeyCap = applyFg(styles.KeyCap, theme.StatusAltFg)
	styles.KeyLabel = applyFg(lipgloss.NewStyle(), theme.Muted)

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
		taskID:    t.ID,
		title:     t.Title,
		topic:     strings.Join(t.Topics, ","),
		tags:      t.Tags,
		assignee:  t.Assignee,
		reporter:  t.Reporter,
		priority:  fmt.Sprintf("%d", t.Priority),
		due:       formatDateTime(t.Due),
		start:     defaultStart(t),
		end:       formatDateTime(t.End),
		timezone:  defaultTimezone(t.Timezone),
		rule:      t.RecurrenceRule,
		interval:  intervalString(t.RecurrenceInterval),
		notes:     t.Notes,
		notesOrig: t.Notes,
		recurring: t.Recurring,
		index:     0,
		expanded:  true, // editing: show every field up front
	}
	m.focusMetaField()
	m.mode = modeMetadata
	m.status = m.metaPrompt()
	return m, nil
}

func (m Model) startMetadataAdd() (tea.Model, tea.Cmd) {
	defaultTopic := ""
	if m.currentTopic != "" && !isSpecialTopic(m.currentTopic) {
		defaultTopic = m.currentTopic
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	m.meta = &metaState{
		taskID:    0,
		title:     "",
		topic:     defaultTopic,
		tags:      "",
		assignee:  "",
		reporter:  "",
		priority:  "",
		due:       today.Format("2006-01-02 15:04"), // default to today; x clears
		start:     "",
		end:       "",
		timezone:  defaultTimezone(""),
		rule:      "",
		interval:  "",
		notes:     "",
		recurring: false,
		index:     0,
		expanded:  false, // adding: start lean, reveal details on demand
	}
	m.focusMetaField()
	m.mode = modeMetadata
	m.status = m.metaPrompt()
	return m, nil
}

// focusMetaField syncs the shared text input to the current modal field and
// resets any pending autocomplete state.
func (m *Model) focusMetaField() {
	if m.meta == nil {
		return
	}
	m.meta.completions = nil
	m.meta.completionIdx = 0
	m.meta.lastInput = ""
	m.input.Width = 36
	// The toggle row and the stepper fields (Priority, Due) aren't typed into,
	// so the text input stays blurred there.
	if m.meta.index == fieldMore || m.meta.index == 3 || m.meta.index == 4 {
		if m.meta.index == 4 {
			m.meta.dueComponent = 2 // default to the day part
		}
		m.input.SetValue(m.meta.currentValue())
		m.input.Blur()
		return
	}
	m.input.SetValue(m.meta.currentValue())
	m.input.CursorEnd()
	m.input.Placeholder = metaShortLabel(m.meta.index)
	m.input.Focus()
}

// metaMove advances the modal cursor by delta rows (wrapping), saving the
// current field's value first.
func (m *Model) metaMove(delta int) {
	if m.meta == nil {
		return
	}
	if m.meta.index != fieldMore {
		m.meta.setCurrentValue(m.input.Value())
	}
	ord := m.meta.order()
	pos := wrapIndex(m.meta.orderPos()+delta, len(ord))
	m.meta.index = ord[pos]
	m.meta.validation = ""
	m.meta.dueTyping = ""
	m.focusMetaField()
	m.status = m.metaPrompt()
}

// toggleMetaDetails expands or collapses the modal's detail section.
func (m *Model) toggleMetaDetails() {
	if m.meta == nil {
		return
	}
	m.meta.expanded = !m.meta.expanded
	if m.meta.expanded {
		m.status = "Details shown"
	} else {
		m.status = "Details hidden"
	}
}

// updateMetadataMode drives the Create/Edit Task dialog as a plain form: type
// directly into text fields, Tab/arrows move between fields, +/- and ←/→ drive
// the Priority/Due/Recurrence steppers, Enter/^S save, Esc cancels.
func (m Model) updateMetadataMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.meta == nil {
		return m, nil
	}
	onMore := m.meta.index == fieldMore
	onPriority := m.meta.index == 3
	onRecurrence := m.meta.index == 10
	onDue := m.meta.index == 4

	switch key {
	case m.cfg.Keys.Cancel, "esc":
		// Esc cancels and discards (single press).
		m.meta = nil
		m.mode = modeList
		m.input.Blur()
		m.status = "Cancelled"
		return m, nil
	case "ctrl+s":
		return m.saveMeta()
	case m.cfg.Keys.Confirm, "enter":
		if onMore {
			m.toggleMetaDetails()
			m.metaMove(1)
			return m, nil
		}
		return m.saveMeta()
	case "tab", "down":
		m.metaMove(1)
		return m, nil
	case "shift+tab", "up":
		m.metaMove(-1)
		return m, nil
	case "ctrl+n":
		return m.cycleCompletion(1), nil
	case "ctrl+p":
		return m.cycleCompletion(-1), nil
	case " ", "space":
		if onMore {
			m.toggleMetaDetails()
			m.metaMove(1)
			return m, nil
		}
	case "left":
		if onMore {
			if m.meta.expanded {
				m.toggleMetaDetails()
			}
			return m, nil
		}
		if onPriority {
			m.stepPriority(-1)
			return m, nil
		}
		if onRecurrence {
			m.cycleRecurrence(-1)
			return m, nil
		}
		if onDue {
			m.moveDueComponent(-1)
			return m, nil
		}
	case "right":
		if onMore {
			if !m.meta.expanded {
				m.toggleMetaDetails()
				m.metaMove(1)
			}
			return m, nil
		}
		if onPriority {
			m.stepPriority(1)
			return m, nil
		}
		if onRecurrence {
			m.cycleRecurrence(1)
			return m, nil
		}
		if onDue {
			m.moveDueComponent(1)
			return m, nil
		}
	case "+", "=":
		if onPriority {
			m.stepPriority(1)
			return m, nil
		}
		if onDue {
			m.stepDue(1)
			return m, nil
		}
	case "-", "_":
		if onPriority {
			m.stepPriority(-1)
			return m, nil
		}
		if onDue {
			m.stepDue(-1)
			return m, nil
		}
	case "x":
		if onDue {
			m.clearDue() // due isn't text; x clears it
			return m, nil
		}
	}

	// On the Due field, digits fill the selected part directly (type the date).
	if onDue && len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		m.typeDueDigit(key[0])
		return m, nil
	}
	// The toggle row and the stepper fields don't accept typed text.
	if onMore || onPriority || onDue {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.applyMetaInputSanitizer()
	m.meta.validation = ""
	return m, cmd
}

// saveMeta validates and commits the modal, returning to the list on success
// or keeping the modal open (with an inline message) on a validation error.
func (m Model) saveMeta() (tea.Model, tea.Cmd) {
	if m.meta == nil {
		return m, nil
	}
	if m.meta.index != fieldMore {
		m.meta.setCurrentValue(m.input.Value())
	}
	if strings.TrimSpace(m.meta.title) == "" {
		m.meta.validation = "Title is required"
		m.meta.index = 0
		m.focusMetaField()
		m.status = "Title is required"
		return m, nil
	}
	verb := "created"
	if m.meta.taskID != 0 {
		verb = "saved"
	}
	var err error
	m, err = m.applyMetadataAndReload()
	if err != nil {
		m.status = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	// applyMetadataAndReload reports field-level problems via status without
	// committing; keep the modal open so the user can fix them.
	if strings.HasPrefix(m.status, "due date invalid") ||
		strings.HasPrefix(m.status, "priority invalid") ||
		strings.HasPrefix(m.status, "start date invalid") ||
		strings.HasPrefix(m.status, "end date invalid") {
		m.meta.validation = m.status
		return m, nil
	}
	m.meta = nil
	m.mode = modeList
	m.input.Blur()
	m.status = "Task " + verb
	return m, nil
}

// cycleCompletion advances the autocomplete suggestion for the current field.
func (m Model) cycleCompletion(dir int) Model {
	if m.meta == nil || m.meta.index == fieldMore {
		return m
	}
	currentInput := m.input.Value()
	if currentInput != m.meta.lastInput {
		m.meta.completions = m.metaCompletions(m.meta.index, currentInput)
		if dir >= 0 {
			m.meta.completionIdx = 0
		} else {
			m.meta.completionIdx = len(m.meta.completions) - 1
		}
		m.meta.lastInput = currentInput
	}
	if len(m.meta.completions) == 0 {
		m.status = "No completions available"
		return m
	}
	completion := m.meta.completions[m.meta.completionIdx]
	m.input.SetValue(completion)
	m.input.CursorEnd()
	m.meta.setCurrentValue(completion)
	m.meta.lastInput = completion
	m.meta.completionIdx = wrapIndex(m.meta.completionIdx+dir, len(m.meta.completions))
	m.status = fmt.Sprintf("Completion: %s", completion)
	return m
}

// stepPriority nudges the Priority field within the 0–5 range.
func (m *Model) stepPriority(delta int) {
	if m.meta == nil {
		return
	}
	val, _ := strconv.Atoi(filterDigits(m.meta.priority))
	val += delta
	if val < 0 {
		val = 0
	}
	if val > 5 {
		val = 5
	}
	m.meta.priority = fmt.Sprintf("%d", val)
	m.input.SetValue(m.meta.priority)
	m.meta.validation = ""
	m.status = m.metaPrompt()
}

// cycleRecurrence steps the Recurrence field through "off" and the common rules.
func (m *Model) cycleRecurrence(delta int) {
	if m.meta == nil {
		return
	}
	options := append([]string{""}, commonRecurrenceRules()...)
	cur := strings.TrimSpace(strings.ToLower(m.meta.rule))
	idx := 0
	for i, o := range options {
		if strings.ToLower(o) == cur {
			idx = i
			break
		}
	}
	idx = wrapIndex(idx+delta, len(options))
	m.meta.rule = options[idx]
	m.input.SetValue(m.meta.rule)
	m.input.CursorEnd()
	m.meta.validation = ""
	m.status = m.metaPrompt()
}

// dueTime resolves the Due field to a concrete time, falling back to today.
func (m Model) dueTime(now time.Time) time.Time {
	if t, err := parseDueInput(m.meta.due, now); err == nil && t.Valid {
		return t.Time
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// stepDue adjusts the selected component of the Due stepper. The first press on
// an empty field seeds it with today's date so the user never types a baseline.
func (m *Model) stepDue(delta int) {
	if m.meta == nil {
		return
	}
	now := time.Now()
	m.meta.dueTyping = ""
	if strings.TrimSpace(m.meta.due) == "" {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		m.meta.due = today.Format("2006-01-02 15:04")
		m.input.SetValue(m.meta.due)
		m.meta.validation = ""
		m.status = m.metaPrompt()
		return
	}
	t := m.dueTime(now)
	switch m.meta.dueComponent {
	case 0:
		t = t.AddDate(delta, 0, 0)
	case 1:
		t = t.AddDate(0, delta, 0)
	case 2:
		t = t.AddDate(0, 0, delta)
	case 3:
		t = t.Add(time.Duration(delta) * time.Hour)
	case 4:
		t = t.Add(time.Duration(delta) * time.Minute)
	}
	m.meta.due = t.Format("2006-01-02 15:04")
	m.input.SetValue(m.meta.due)
	m.meta.validation = ""
	m.status = m.metaPrompt()
}

// moveDueComponent moves the Due stepper's selection across Y/M/D/H/min.
func (m *Model) moveDueComponent(delta int) {
	if m.meta == nil {
		return
	}
	c := m.meta.dueComponent + delta
	if c < 0 {
		c = 0
	}
	if c > 4 {
		c = 4
	}
	m.meta.dueComponent = c
	m.meta.dueTyping = "" // moving parts starts a fresh number for the new part
	m.status = m.metaPrompt()
}

// clearDue removes the due date entirely.
func (m *Model) clearDue() {
	if m.meta == nil {
		return
	}
	m.meta.due = ""
	m.meta.dueTyping = ""
	m.input.SetValue("")
	m.meta.validation = ""
	m.status = m.metaPrompt()
}

// typeDueDigit fills the selected Due component by typing digits directly. Digits
// accumulate into the active part (Y is 4 wide, the rest 2); once the part is
// full the selection auto-advances to the next part. Seeds today when empty.
func (m *Model) typeDueDigit(d byte) {
	if m.meta == nil {
		return
	}
	now := time.Now()
	if strings.TrimSpace(m.meta.due) == "" {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		m.meta.due = today.Format("2006-01-02 15:04")
		m.meta.dueTyping = ""
	}
	comp := m.meta.dueComponent
	maxLen := 2
	if comp == 0 {
		maxLen = 4
	}
	buf := m.meta.dueTyping + string(d)
	if len(buf) > maxLen {
		buf = string(d) // overflow: start the part over with this digit
	}
	val, _ := strconv.Atoi(buf)
	t := applyDueComponent(m.dueTime(now), comp, val)
	m.meta.due = t.Format("2006-01-02 15:04")
	m.input.SetValue(m.meta.due)
	if len(buf) >= maxLen {
		if comp < 4 {
			m.meta.dueComponent = comp + 1
		}
		m.meta.dueTyping = ""
	} else {
		m.meta.dueTyping = buf
	}
	m.meta.validation = ""
	m.status = m.metaPrompt()
}

// applyDueComponent sets one part (0=Y..4=min) of t to val, clamping to a valid
// calendar value (months 1-12, days to the month length, hours/minutes in range).
func applyDueComponent(t time.Time, comp, val int) time.Time {
	y, mo, d := t.Date()
	h, mi := t.Hour(), t.Minute()
	loc := t.Location()
	switch comp {
	case 0:
		y = val
	case 1:
		if val < 1 {
			val = 1
		}
		if val > 12 {
			val = 12
		}
		mo = time.Month(val)
	case 2:
		if val < 1 {
			val = 1
		}
		d = val
	case 3:
		if val > 23 {
			val = 23
		}
		h = val
	case 4:
		if val > 59 {
			val = 59
		}
		mi = val
	}
	if dim := daysInMonth(y, mo); d > dim {
		d = dim
	}
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

// daysInMonth returns the number of days in the given month/year.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// flattenInline collapses line breaks to spaces, matching how the single-line
// modal input renders multi-line notes; used to compare loaded vs edited notes.
func flattenInline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func (m *Model) applyMetaInputSanitizer() {
	// sanitize input per field type and store it
	if m.meta == nil {
		return
	}
	switch m.meta.index {
	case 3: // priority
		m.input.SetValue(filterDigits(m.input.Value()))
	case 4: // due datetime (accepts natural language: today, tomorrow, in 3d, ...)
		m.input.SetValue(filterDueInput(m.input.Value()))
	case 5: // assignee
		m.input.SetValue(strings.TrimSpace(m.input.Value()))
	case 6: // reporter
		m.input.SetValue(strings.TrimSpace(m.input.Value()))
	case 7: // start date
		m.input.SetValue(filterDate(m.input.Value()))
	case 8: // end date/time
		m.input.SetValue(filterDueInput(m.input.Value()))
	case 9: // timezone
		m.input.SetValue(filterTimezone(m.input.Value()))
	case 10: // recurrence rule
		m.input.SetValue(filterRule(m.input.Value()))
	case 11: // interval
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
		"Due (YYYY-MM-DD or YYYY-MM-DD HH:MM)",
		"Assignee",
		"Reporter",
		"Start Date (YYYY-MM-DD)",
		"End (YYYY-MM-DD or YYYY-MM-DD HH:MM)",
		"Timezone (UTC±HH:MM)",
		"Recurrence",
		"Interval",
		"Notes",
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
		return ms.assignee
	case 6:
		return ms.reporter
	case 7:
		return ms.start
	case 8:
		return ms.end
	case 9:
		return ms.timezone
	case 10:
		return ms.rule
	case 11:
		return ms.interval
	case 12:
		return ms.notes
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
		ms.assignee = v
	case 6:
		ms.reporter = v
	case 7:
		ms.start = v
	case 8:
		ms.end = v
	case 9:
		ms.timezone = v
	case 10:
		ms.rule = v
	case 11:
		ms.interval = v
	case 12:
		ms.notes = v
	}
}

func (m Model) metaPrompt() string {
	if m.meta == nil {
		return ""
	}
	verb := "Create task"
	if m.meta.taskID != 0 {
		verb = "Edit task"
	}
	return verb + ": tab/↑↓ move · +/- adjust · ⏎/^S save · esc cancel · ^n/^p complete"
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
	due, err := parseDueInput(m.meta.due, time.Now())
	if err != nil {
		m.status = fmt.Sprintf("due date invalid: %v", err)
		return m, nil
	}
	start, err := parseDate(m.meta.start)
	if err != nil {
		m.status = fmt.Sprintf("start date invalid: %v", err)
		return m, nil
	}
	end, err := parseDateTime(m.meta.end)
	if err != nil {
		m.status = fmt.Sprintf("end date invalid: %v", err)
		return m, nil
	}
	timezone := normalizeTimezone(m.meta.timezone)
	ruleInput := strings.TrimSpace(m.meta.rule)
	rule := strings.TrimSpace(ruleInput)
	interval := parseInterval(m.meta.interval)
	recurring := rule != "" || interval > 0
	if spec, ok := parseRecurrenceSpec(ruleInput); ok {
		rule = spec.label
		interval = 0
		recurring = true
	}
	if strings.EqualFold(ruleInput, "none") || strings.EqualFold(ruleInput, "off") {
		rule = ""
		interval = 0
		recurring = false
	}
	if strings.TrimSpace(rule) == "" || strings.EqualFold(rule, "none") {
		if interval > 0 {
			rule = fmt.Sprintf("every %d days", interval)
			interval = 0
		} else {
			rule = ""
		}
	}
	if !recurring && m.meta.taskID != 0 && m.meta.recurring {
		recurring = true
	}

	if taskID == 0 {
		newID, err := m.store.AddTask(title)
		if err != nil {
			return m, err
		}
		taskID = newID
	}
	if err := m.store.UpdateTaskMetadata(taskID, m.meta.topic, m.meta.tags, m.meta.assignee, m.meta.reporter, timezone, priority, due, start, end, recurring); err != nil {
		return m, err
	}
	if err := m.store.UpdateRecurrence(taskID, rule, interval); err != nil {
		return m, err
	}
	if err := m.store.UpdateTitle(taskID, title); err != nil {
		return m, err
	}
	// The modal's Notes field is single-line, so it flattens any existing
	// multi-line note. Only persist when it actually changed, otherwise leave
	// the original (possibly multi-line) note intact for the full notes editor.
	if flattenInline(m.meta.notes) != flattenInline(m.meta.notesOrig) {
		if err := m.store.UpdateTaskNotes(taskID, m.meta.notes); err != nil {
			return m, err
		}
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

func parseDateTime(v string) (sql.NullTime, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return sql.NullTime{}, nil
	}
	layouts := []string{"2006-01-02 15:04", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			return sql.NullTime{Time: t, Valid: true}, nil
		}
	}
	return sql.NullTime{}, fmt.Errorf("expected YYYY-MM-DD or YYYY-MM-DD HH:MM")
}

func formatDate(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}

func formatDateTime(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	if t.Time.Hour() == 0 && t.Time.Minute() == 0 && t.Time.Second() == 0 {
		return t.Time.Format("2006-01-02")
	}
	return t.Time.Format("2006-01-02 15:04")
}

func displayDate(t sql.NullTime) string {
	if t.Valid {
		return formatDateTime(t)
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
		m.meta.assignee,
		m.meta.reporter,
		m.meta.start,
		m.meta.end,
		m.meta.timezone,
		m.meta.rule,
		m.meta.interval,
		m.meta.notes,
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

// valueOf returns the raw stored value for a field index.
func (ms metaState) valueOf(idx int) string {
	switch idx {
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
		return ms.assignee
	case 6:
		return ms.reporter
	case 7:
		return ms.start
	case 8:
		return ms.end
	case 9:
		return ms.timezone
	case 10:
		return ms.rule
	case 11:
		return ms.interval
	case 12:
		return ms.notes
	default:
		return ""
	}
}

// renderMetaModalView draws the Create/Edit Task dialog floated over the task
// list, with list rows still visible above and below.
func (m Model) renderMetaModalView() string {
	inner := m.panelInnerWidth()

	listMax := 0
	if m.height > 0 {
		listMax = (m.height - 1) - 3 // panel borders (2) + legend (1)
		if listMax < 1 {
			listMax = 1
		}
	}
	var bodyLines []string
	if m.height > 0 {
		bodyLines = strings.Split(m.renderTaskListWithHeight(listMax), "\n")
		for len(bodyLines) < listMax {
			bodyLines = append(bodyLines, "")
		}
		if len(bodyLines) > listMax {
			bodyLines = bodyLines[:listMax]
		}
	} else {
		bodyLines = strings.Split(m.renderTaskList(), "\n")
	}

	box := m.renderCreateModal()
	boxLines := strings.Split(box, "\n")
	boxW := 0
	for _, l := range boxLines {
		if w := lipgloss.Width(l); w > boxW {
			boxW = w
		}
	}

	rows := len(bodyLines)
	top := (rows - len(boxLines)) / 2
	if top < 0 {
		top = 0
	}
	left := (inner - boxW) / 2
	if left < 0 {
		left = 0
	}
	for i, bl := range boxLines {
		r := top + i
		if r < 0 || r >= rows {
			continue
		}
		w := lipgloss.Width(bl)
		line := strings.Repeat(" ", left) + bl
		if pad := inner - left - w; pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		bodyLines[r] = line
	}

	return m.panel("bada · Tasks", strings.Join(bodyLines, "\n")) + "\n" + m.legendBar()
}

// renderCreateModal builds the bordered dialog box (title, fields, toggle,
// validation, hint).
func (m Model) renderCreateModal() string {
	title := "Create Task"
	if m.meta.taskID != 0 {
		title = "Edit Task"
	}

	labelW := 9
	boxInner := 54
	if max := m.panelInnerWidth() - 6; boxInner > max {
		boxInner = max
	}
	if boxInner < 30 {
		boxInner = 30
	}
	// prefix = "▌ " marker (2) + label column + "  " gap (2)
	prefix := 2 + labelW + 2
	valueW := boxInner - prefix - 1
	if valueW < 8 {
		valueW = 8
	}

	now := time.Now()
	var lines []string
	for _, f := range m.meta.order() {
		active := f == m.meta.index
		if f == fieldMore {
			lines = append(lines, m.renderModalToggleRow(active))
			continue
		}
		lines = append(lines, m.renderModalFieldRow(f, active, labelW, valueW, now))
	}
	lines = append(lines, "")
	if m.meta.validation != "" {
		lines = append(lines, m.styles.Warning.Render("⚠ "+m.meta.validation))
	}
	lines = append(lines, m.modalHintLine())

	return m.modalFrame(title, lines, boxInner)
}

func (m Model) renderModalFieldRow(f int, active bool, labelW, valueW int, now time.Time) string {
	label := metaShortLabel(f)
	if f == 0 {
		label += " *" // required
	}
	labelStr := fmt.Sprintf("%-*s", labelW, label)
	if active {
		labelStr = m.styles.Accent.Render(labelStr)
	} else {
		labelStr = m.styles.Heading.Render(labelStr)
	}
	m.input.Width = valueW

	var value string
	switch f {
	case 3: // priority — stepper
		value = m.priorityDisplay(active)
	case 4: // due — date stepper (prefilled today; +/- adjusts, ←/→ pick part)
		if active {
			value = m.renderDueStepper(now)
		} else {
			value = m.dueDisplay(now)
		}
	case 10: // recurrence
		if active {
			value = m.input.View()
		} else {
			value = recurDisplay(m, m.meta.rule)
		}
	default:
		if active {
			value = m.input.View()
		} else {
			value = orDash(m, m.meta.valueOf(f))
		}
	}

	marker := "  "
	if active {
		marker = m.styles.Accent.Render("▌ ")
	}
	return marker + labelStr + "  " + value
}

// modalHintLine renders the keys relevant to the current field.
func (m Model) modalHintLine() string {
	switch m.meta.index {
	case 4: // Due stepper
		return m.styles.Muted.Render("type digits or +/-:set · ←→:part · x:no due · tab:next · ⏎:save")
	case 3: // Priority stepper
		return m.styles.Muted.Render("+/-:priority · tab:next · ⏎:save · esc:cancel")
	default:
		return m.styles.Muted.Render("tab/↑↓:move · ⏎:save · esc:cancel · ^n/^p:complete")
	}
}

func (m Model) renderModalToggleRow(active bool) string {
	text := "▸ More details"
	if m.meta.expanded {
		text = "▾ Fewer details"
	}
	marker := "  "
	if active {
		marker = m.styles.Accent.Render("▌ ")
		text = m.styles.Accent.Render(text)
	} else {
		text = m.styles.Muted.Render(text)
	}
	return marker + text
}

func (m Model) priorityDisplay(active bool) string {
	disp := "—"
	if v := strings.TrimSpace(m.meta.priority); v != "" {
		disp = "P" + v
	}
	if active {
		return m.styles.Accent.Render("‹ " + disp + " ›")
	}
	return disp
}

// renderDueStepper draws the active Due field as Y-M-D H:M with the selected
// component highlighted, plus the weekday for orientation.
func (m Model) renderDueStepper(now time.Time) string {
	if strings.TrimSpace(m.meta.due) == "" {
		return m.styles.Muted.Render("no due date  ") + m.styles.Accent.Render("type / +-") + m.styles.Muted.Render(" to set")
	}
	t := m.dueTime(now)
	parts := []string{t.Format("2006"), t.Format("01"), t.Format("02"), t.Format("15"), t.Format("04")}
	seps := []string{"-", "-", " ", ":"}
	var b strings.Builder
	for i, p := range parts {
		if i == m.meta.dueComponent {
			b.WriteString(m.styles.Selection.Render(p))
		} else {
			b.WriteString(p)
		}
		if i < len(seps) {
			b.WriteString(seps[i])
		}
	}
	b.WriteString(m.styles.Muted.Render("  " + t.Format("Mon") + "  ·  x:no due"))
	return b.String()
}

// dueDisplay renders the inactive Due value: a resolved date, raw text, or —.
func (m Model) dueDisplay(now time.Time) string {
	if strings.TrimSpace(m.meta.due) == "" {
		return m.styles.Muted.Render("—")
	}
	if prev := dueePreview(m.meta.due, now); prev != "" && prev != "invalid" {
		return prev
	}
	return m.meta.due
}

func recurDisplay(m Model, rule string) string {
	if strings.TrimSpace(rule) == "" {
		return m.styles.Muted.Render("off")
	}
	return rule
}

func orDash(m Model, v string) string {
	if strings.TrimSpace(v) == "" {
		return m.styles.Muted.Render("—")
	}
	return v
}

// modalFrame wraps body lines in a rounded box with an embedded title, sized to
// a fixed inner width.
func (m Model) modalFrame(title string, lines []string, inner int) string {
	bs := m.styles.Border
	var b strings.Builder
	b.WriteString(m.panelTop(title, inner))
	b.WriteString("\n")
	left := bs.Render("│")
	right := bs.Render("│")
	for _, line := range lines {
		w := lipgloss.Width(line)
		if w < inner {
			line += strings.Repeat(" ", inner-w)
		} else if w > inner {
			line = truncateANSI(line, inner)
		}
		b.WriteString(left + line + right)
		b.WriteString("\n")
	}
	b.WriteString(bs.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

func (m *Model) refreshReport() {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.Add(24 * time.Hour)
	soon := today.Add(72 * time.Hour)

	var overdue, todayList, upcoming, recurring []storage.Task
	for _, t := range m.tasks {
		if isRecurringTask(t) && isActive(t) {
			recurring = append(recurring, t)
		}
		if isDone(t) || !t.Due.Valid {
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
				due := formatDateTime(t.Due)
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
				due = fmt.Sprintf("due %s", formatDateTime(t.Due))
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
	m.reportScroll = 0
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
		{label: "Assignee", value: ""},
		{label: "Reporter", value: ""},
		{label: "Priority", value: ""},
		{label: "Due", value: ""},
		{label: "Start", value: ""},
		{label: "End", value: ""},
		{label: "Timezone", value: ""},
		{label: "Recurrence", value: ""},
	}
	if ok {
		rows[0].value = task.Title
		rows[1].value = emptyPlaceholder(strings.Join(task.Topics, ", "))
		rows[2].value = emptyPlaceholder(task.Tags)
		rows[3].value = emptyPlaceholder(task.Assignee)
		rows[4].value = emptyPlaceholder(task.Reporter)
		rows[5].value = fmt.Sprintf("%d", task.Priority)
		rows[6].value = emptyPlaceholder(formatDateTime(task.Due))
		rows[7].value = defaultStart(task)
		rows[8].value = emptyPlaceholder(formatDateTime(task.End))
		rows[9].value = defaultTimezone(task.Timezone)
		if recSummary := recurrenceSummary(task); recSummary != "" {
			if next, ok := nextRecurrenceDate(task); ok {
				rows[10].value = fmt.Sprintf("%s • Next: %s", recSummary, next.Format("2006-01-02"))
			} else {
				rows[10].value = recSummary
			}
		} else {
			rows[10].value = "off"
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

func (m Model) noteMetaBlockLines() []string {
	if m.note == nil {
		return nil
	}
	type row struct {
		label string
		value string
	}
	rows := []row{}
	switch m.note.target.kind {
	case noteTask:
		idx := m.findTaskIndex(m.note.target.taskID)
		if idx < 0 {
			return nil
		}
		task := m.tasks[idx]
		recurrence := "off"
		if recSummary := recurrenceSummary(task); recSummary != "" {
			if next, ok := nextRecurrenceDate(task); ok {
				recurrence = fmt.Sprintf("%s • Next: %s", recSummary, next.Format("2006-01-02"))
			} else {
				recurrence = recSummary
			}
		}
		rows = []row{
			{label: "Topics", value: emptyPlaceholder(strings.Join(task.Topics, ", "))},
			{label: "Tags", value: emptyPlaceholder(task.Tags)},
			{label: "Assignee", value: emptyPlaceholder(task.Assignee)},
			{label: "Reporter", value: emptyPlaceholder(task.Reporter)},
			{label: "Priority", value: fmt.Sprintf("%d", task.Priority)},
			{label: "Due", value: emptyPlaceholder(formatDateTime(task.Due))},
			{label: "Start", value: emptyPlaceholder(formatDate(task.Start))},
			{label: "End", value: emptyPlaceholder(formatDateTime(task.End))},
			{label: "Timezone", value: emptyPlaceholder(defaultTimezone(task.Timezone))},
			{label: "Recurrence", value: recurrence},
		}
	case noteTopic:
		stats := m.topicStats()[m.note.target.topic]
		rows = []row{
			{label: "Topic", value: m.note.target.topic},
			{label: "Tasks", value: fmt.Sprintf("%d", stats.total)},
			{label: "Overdue", value: fmt.Sprintf("%d", stats.overdue)},
		}
	default:
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	labelWidth := 0
	for _, r := range rows {
		if len(r.label) > labelWidth {
			labelWidth = len(r.label)
		}
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		label := fmt.Sprintf("%-*s", labelWidth, r.label)
		lines = append(lines, fmt.Sprintf("%s : %s", label, r.value))
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

func (m Model) noteMetaSeparator() string {
	width := m.width
	if width <= 0 {
		width = 24
	}
	return m.styles.Border.Render(m.ruleLine(width))
}

func (m Model) noteAvailableHeight() int {
	if m.height <= 0 {
		return -1
	}
	metaLines := m.noteMetaBlockLines()
	headerLines := 2 + len(metaLines)
	if len(metaLines) > 0 {
		headerLines += 2
	}
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

func (m Model) renderInfoBlock(lines []string) string {
	if len(lines) == 0 {
		return ""
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
		b.WriteString(text)
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
	if m.width > 0 {
		style = style.Width(m.width).MaxWidth(m.width)
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
	statusBar := m.renderStatusBar()
	if m.height <= 0 {
		return body + "\n" + statusBar
	}
	target := m.height - 1
	if target < 1 {
		target = 1
	}
	lines := strings.Split(body, "\n")
	for len(lines) < target {
		lines = append(lines, "")
	}
	if len(lines) > target {
		lines = lines[:target]
	}
	return strings.Join(lines, "\n") + "\n" + statusBar
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
	case modeConfig:
		return "CONFIG"
	case modeSearch:
		return "SEARCH"
	case modeTrash:
		return "TRASH"
	case modeNote:
		return "NOTE"
	case modeReport:
		return "REPORT"
	case modeHelp:
		return "HELP"
	case modeCalendar:
		return "CALENDAR"
	case modeGantt:
		return "GANTT"
	case modeStats:
		return "STATS"
	default:
		return "?"
	}
}

func (m Model) startCommand() (tea.Model, tea.Cmd) {
	m.mode = modeCommand
	m.input.SetValue("")
	m.input.Placeholder = ""
	m.input.Focus()
	m.status = "Command: type a command (tab to autocomplete), Enter to run, Esc to cancel"
	return m, nil
}

func (m Model) startConfig() (Model, tea.Cmd) {
	m.mode = modeConfig
	m.configStage = configStagePath
	m.pendingCfgPath = ""
	m.pendingDBPath = ""
	m.input.SetValue(m.configPath)
	m.input.Placeholder = "Config path"
	m.input.Focus()
	m.input.CursorEnd()
	m.status = "Config path: Enter to continue, Esc to cancel"
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
	case "tab":
		m.input.SetValue(completeCommand(m.input.Value()))
		m.input.CursorEnd()
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		cmd := strings.TrimSpace(m.input.Value())
		cmdLower := strings.TrimPrefix(strings.ToLower(cmd), ":")
		switch cmdLower {
		case "q", "quit", "wq", "x":
			return m, tea.Quit
		case "help":
			return m.enterHelpView()
		case "agenda":
			return m.enterReportView()
		case "calendar":
			return m.enterCalendarView()
		case "gantt":
			return m.enterGanttView()
		case "stats":
			return m.enterStatsView()
		case "config":
			return m.startConfig()
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

func completeCommand(input string) string {
	raw := strings.TrimSpace(input)
	prefix := ""
	if strings.HasPrefix(raw, ":") {
		prefix = ":"
		raw = strings.TrimPrefix(raw, ":")
	}
	cmd := strings.ToLower(raw)
	commands := []string{"agenda", "calendar", "config", "gantt", "help", "quit", "stats"}
	if cmd == "" {
		return prefix + commands[0]
	}
	if cmd == commands[0] {
		return prefix + commands[1]
	}
	if cmd == commands[1] {
		return prefix + commands[0]
	}
	var matches []string
	for _, c := range commands {
		if strings.HasPrefix(c, cmd) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 1 {
		return prefix + matches[0]
	}
	if len(matches) > 1 {
		return prefix + matches[0]
	}
	return input
}

func (m Model) updateConfigMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel, "esc":
		m.mode = modeList
		m.input.Blur()
		m.status = "Config cancelled"
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		value := strings.TrimSpace(m.input.Value())
		if m.configStage == configStagePath {
			if value == "" {
				value = m.configPath
			}
			m.pendingCfgPath = value
			m.configStage = configStageDB
			m.input.SetValue(m.cfg.DBPath)
			m.input.Placeholder = "DB path"
			m.input.CursorEnd()
			m.status = "DB path: Enter to save, Esc to cancel"
			return m, nil
		}
		if value == "" {
			value = m.cfg.DBPath
		}
		m.pendingDBPath = value
		return m.applyConfigChanges()
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
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

func (m Model) applyConfigChanges() (tea.Model, tea.Cmd) {
	newConfigPath := strings.TrimSpace(m.pendingCfgPath)
	if newConfigPath == "" {
		newConfigPath = m.configPath
	}
	newDBPath := strings.TrimSpace(m.pendingDBPath)
	if newDBPath == "" {
		newDBPath = m.cfg.DBPath
	}

	oldConfigPath := m.configPath
	oldDBPath := m.cfg.DBPath

	cfg := m.cfg
	cfg.DBPath = newDBPath

	var newStore *storage.Store
	if newDBPath != oldDBPath {
		store, err := storage.Open(newDBPath, cfg.TrashDir)
		if err != nil {
			m.mode = modeList
			m.input.Blur()
			m.status = fmt.Sprintf("db reopen failed: %v", err)
			return m, nil
		}
		newStore = store
	}

	if err := config.Save(newConfigPath, cfg); err != nil {
		if newStore != nil {
			_ = newStore.Close()
		}
		m.mode = modeList
		m.input.Blur()
		m.status = fmt.Sprintf("config save failed: %v", err)
		return m, nil
	}

	if newConfigPath != oldConfigPath {
		if err := config.SetConfigPath(newConfigPath); err != nil {
			if newStore != nil {
				_ = newStore.Close()
			}
			m.mode = modeList
			m.input.Blur()
			m.status = fmt.Sprintf("config path update failed: %v", err)
			return m, nil
		}
		m.configPath = newConfigPath
	}

	m.cfg = cfg
	if newStore != nil {
		_ = m.store.Close()
		m.store = newStore
		tasks, err := m.store.FetchTasks()
		if err != nil {
			m.mode = modeList
			m.input.Blur()
			m.status = fmt.Sprintf("reload failed: %v", err)
			return m, nil
		}
		m.tasks = tasks
		m.sortTasks()
		m.refreshReport()
		m.cursor = clampCursor(m.cursor, len(m.visibleItems()))
	}

	m.mode = modeList
	m.input.Blur()
	if newConfigPath != oldConfigPath || newDBPath != oldDBPath {
		m.status = "Config updated"
	} else {
		m.status = "Config unchanged"
	}
	return m, nil
}

func (m Model) startRename(t storage.Task) (tea.Model, tea.Cmd) {
	m.renameID = t.ID
	m.input.SetValue(t.Title)
	m.input.CursorEnd()
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
	m.input.CursorEnd()
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

func (m *Model) sortTasks() {
	switch m.sortMode {
	case "auto":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			a := m.tasks[i]
			b := m.tasks[j]
			if stateRank(a) != stateRank(b) {
				return stateRank(a) < stateRank(b)
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
			if stateRank(a) != stateRank(b) {
				return stateRank(a) < stateRank(b)
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

func stateRank(t storage.Task) int {
	if isDone(t) {
		return 3
	}
	if isOverdue(t) {
		return 0
	}
	if t.Status == "IN-PROGRESS" {
		return 1
	}
	return 2
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
	if isDone(t) {
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

func (m *Model) processScrollKey(key string, max int, scroll *int) bool {
	if key == "" {
		return false
	}
	if key == "g" {
		if m.navBuf == "g" {
			*scroll = 0
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
	if key == "G" {
		*scroll = max
		m.status = "Bottom"
		return true
	}
	return false
}

func taskStatusLabel(t storage.Task) string {
	status := strings.ToUpper(strings.TrimSpace(t.Status))
	if status == "" {
		if t.Done {
			status = "DONE"
		} else {
			status = "PENDING"
		}
	}
	if status == "DONE" {
		return "DONE"
	}
	if isOverdue(t) {
		return "OVERDUE"
	}
	if status == "IN-PROGRESS" {
		return "IN-PROGRESS"
	}
	return "PENDING"
}

func isDone(t storage.Task) bool {
	return t.Done || strings.ToUpper(strings.TrimSpace(t.Status)) == "DONE"
}

func isActive(t storage.Task) bool {
	return !isDone(t)
}

func nextTaskStatus(t storage.Task) string {
	switch strings.ToUpper(strings.TrimSpace(t.Status)) {
	case "PENDING", "":
		return "IN-PROGRESS"
	case "IN-PROGRESS":
		return "DONE"
	default:
		return "PENDING"
	}
}

func topicListLabel(topics []string) string {
	topics = uniqueTopics(topics)
	if len(topics) == 0 {
		return "-"
	}
	return strings.Join(topics, ",")
}

func emptyDash(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "-"
	}
	return v
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

func filterDateTime(v string) string {
	var b strings.Builder
	for _, r := range v {
		if (r >= '0' && r <= '9') || r == '-' || r == ':' || r == ' ' {
			b.WriteRune(r)
		}
		if b.Len() >= 16 {
			break
		}
	}
	return b.String()
}

// filterDueInput keeps characters valid for either an explicit date/time
// (digits, '-', ':', space) or a natural-language phrase (letters, '+').
func filterDueInput(v string) string {
	var b strings.Builder
	for _, r := range v {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			r == '-' || r == '+' || r == ':' || r == ' ' {
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	return b.String()
}

// parseRelativeDate resolves natural-language day references like "today",
// "tomorrow", weekday names, "in 3d", "+2w", "3d" relative to now.
func parseRelativeDate(token string, now time.Time) (time.Time, bool) {
	t := strings.ToLower(strings.TrimSpace(token))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch t {
	case "today", "tod":
		return today, true
	case "tomorrow", "tmr", "tom":
		return today.AddDate(0, 0, 1), true
	case "yesterday":
		return today.AddDate(0, 0, -1), true
	}
	if wd, ok := parseWeekday(t); ok {
		delta := (int(wd) - int(today.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7 // "monday" means the next one, not today
		}
		return today.AddDate(0, 0, delta), true
	}
	// "in 3d", "+3d", "3d", "2w", "in 2 weeks"
	re := regexp.MustCompile(`(?i)^(?:in\s+|\+)?(\d+)\s*(d|day|days|w|week|weeks)$`)
	if mm := re.FindStringSubmatch(t); mm != nil {
		n, err := strconv.Atoi(mm[1])
		if err == nil {
			unit := strings.ToLower(mm[2])
			if strings.HasPrefix(unit, "w") {
				n *= 7
			}
			return today.AddDate(0, 0, n), true
		}
	}
	return time.Time{}, false
}

// parseDueInput parses the Due field, accepting natural language plus explicit
// "YYYY-MM-DD" / "YYYY-MM-DD HH:MM". An optional trailing "HH:MM" may follow a
// relative day (e.g. "tomorrow 14:30").
func parseDueInput(v string, now time.Time) (sql.NullTime, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return sql.NullTime{}, nil
	}
	// Explicit forms first.
	if t, err := parseDateTime(v); err == nil {
		return t, nil
	}
	// Natural language, with an optional trailing clock time.
	dayPart := v
	clock := ""
	if i := strings.LastIndex(v, " "); i >= 0 {
		tail := strings.TrimSpace(v[i+1:])
		if regexp.MustCompile(`^\d{1,2}:\d{2}$`).MatchString(tail) {
			dayPart = strings.TrimSpace(v[:i])
			clock = tail
		}
	}
	day, ok := parseRelativeDate(dayPart, now)
	if !ok {
		return sql.NullTime{}, fmt.Errorf("expected a date or words like today/tomorrow/in 3d")
	}
	if clock != "" {
		hm, err := time.Parse("15:04", clock)
		if err == nil {
			day = time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, day.Location())
		}
	}
	return sql.NullTime{Time: day, Valid: true}, nil
}

// dueePreview renders a small confirmation of how the Due field will be
// interpreted, used live in the modal.
func dueePreview(v string, now time.Time) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	t, err := parseDueInput(v, now)
	if err != nil {
		return "invalid"
	}
	if !t.Valid {
		return ""
	}
	if t.Time.Hour() == 0 && t.Time.Minute() == 0 {
		return t.Time.Format("Mon 2006-01-02")
	}
	return t.Time.Format("Mon 2006-01-02 15:04")
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

func filterTimezone(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r == '+' || r == '-' || r == ':' || r == ' ' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func defaultTimezone(v string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return localTimezoneOffset()
}

func normalizeTimezone(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return localTimezoneOffset()
	}
	re := regexp.MustCompile(`(?i)^(utc)?\s*([+-])\s*(\d{1,2})(?::?(\d{2}))?$`)
	m := re.FindStringSubmatch(v)
	if m == nil {
		return v
	}
	hours, err := strconv.Atoi(m[3])
	if err != nil {
		return v
	}
	mins := 0
	if strings.TrimSpace(m[4]) != "" {
		if val, err := strconv.Atoi(m[4]); err == nil {
			mins = val
		}
	}
	if hours < 0 || hours > 23 || mins < 0 || mins > 59 {
		return v
	}
	return fmt.Sprintf("UTC%s%02d:%02d", m[2], hours, mins)
}

func localTimezoneOffset() string {
	_, offset := time.Now().In(time.Local).Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	hours := offset / 3600
	mins := (offset % 3600) / 60
	return fmt.Sprintf("UTC%s%02d:%02d", sign, hours, mins)
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
		if isDone(t) {
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
		if isDone(t) || !t.Due.Valid {
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
		for _, t := range m.tasks {
			items = append(items, listItem{kind: itemTask, task: t, topic: strings.Join(t.Topics, ",")})
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
	fields := []string{t.Title, strings.Join(t.Topics, " "), t.Tags, t.Assignee, t.Reporter}
	if t.Due.Valid {
		fields = append(fields, formatDateTime(t.Due))
	}
	if t.End.Valid {
		fields = append(fields, formatDateTime(t.End))
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

func (m Model) sortedTags() []string {
	set := map[string]struct{}{}
	for _, t := range m.tasks {
		for _, tag := range strings.Split(t.Tags, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				set[tag] = struct{}{}
			}
		}
	}
	tags := make([]string, 0, len(set))
	for k := range set {
		tags = append(tags, k)
	}
	sort.Strings(tags)
	return tags
}

func commonTimezones() []string {
	return []string{
		"UTC+00:00", "UTC+01:00", "UTC+02:00", "UTC+03:00", "UTC+04:00",
		"UTC+05:00", "UTC+05:30", "UTC+06:00", "UTC+07:00", "UTC+08:00",
		"UTC+09:00", "UTC+10:00", "UTC+11:00", "UTC+12:00",
		"UTC-01:00", "UTC-02:00", "UTC-03:00", "UTC-04:00", "UTC-05:00",
		"UTC-06:00", "UTC-07:00", "UTC-08:00", "UTC-09:00", "UTC-10:00",
		"UTC-11:00", "UTC-12:00",
	}
}

func commonRecurrenceRules() []string {
	return []string{
		"daily", "weekly", "monthly", "yearly",
		"every 2 days", "every 3 days", "every 7 days",
		"every 2 weeks", "every month", "every year",
		"weekdays", "weekends",
	}
}

func (m Model) metaCompletions(fieldIndex int, prefix string) []string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var candidates []string
	switch fieldIndex {
	case 1: // Topics
		candidates = m.sortedTopics()
	case 2: // Tags
		candidates = m.sortedTags()
	case 9: // Timezone
		candidates = commonTimezones()
	case 10: // Rule
		candidates = commonRecurrenceRules()
	default:
		return nil
	}

	if prefix == "" {
		return candidates
	}

	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), prefix) {
			matches = append(matches, c)
		}
	}
	return matches
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
