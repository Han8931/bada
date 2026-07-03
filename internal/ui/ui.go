package ui

import (
	"fmt"
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
	modeConfig
	modeSearch
	modeTrash
	modeNote
	modeReport
	modeCalendar
	modeHelp
	modeGantt
	modeStats
	modeFortune
	modeDashboard
	modeWorkflow
	modeBoard
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

// configEditedMsg is emitted after the external editor opened by :config exits.
type configEditedMsg struct {
	err error
}

type uiStyles struct {
	Title          lipgloss.Style
	Heading        lipgloss.Style
	Accent         lipgloss.Style
	Muted          lipgloss.Style
	Border         lipgloss.Style
	Selection      lipgloss.Style
	Done           lipgloss.Style
	Danger         lipgloss.Style
	Warning        lipgloss.Style
	Success        lipgloss.Style
	Status         lipgloss.Style
	StatusAlt      lipgloss.Style
	StatusOverdue  lipgloss.Style // filled status badge: overdue (red)
	StatusProgress lipgloss.Style // filled status badge: in-progress (amber)
	StatusDone     lipgloss.Style // filled status badge: done (green)
	StatusPending  lipgloss.Style // plain muted status: pending (no fill)
	RowStripe      lipgloss.Style // faint background for alternating task rows
	Panel          lipgloss.Style // rounded border for framed panels
	PanelTitle     lipgloss.Style // title text shown in a panel's top border
	TableHeader    lipgloss.Style // colored column-header bar
	KeyCap         lipgloss.Style // highlighted key glyph in hint bars
	KeyLabel       lipgloss.Style // muted label following a KeyCap
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
	dropdownOpen  bool   // Topic/Tags dropdown of previously-used values is showing
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

type agendaReportLine struct {
	text   string
	style  lipgloss.Style
	taskID int
}

type Model struct {
	store             *storage.Store
	cfg               config.Config
	configPath        string
	tasks             []storage.Task
	trash             []storage.TrashEntry
	cursor            int
	navBuf            string
	trashCursor       int
	mode              mode
	report            string
	reportRows        []agendaReportLine
	reportTaskIDs     []int
	reportCursor      int
	recentLimit       int
	input             textinput.Model
	status            string
	filterDone        string
	sortMode          string
	sortReversed      bool
	sortBuf           string
	pendingSort       bool
	currentTopic      string
	searchQuery       string
	searchFuzzy       bool
	searchCursor      int // highlighted row in the live fuzzy-find preview
	commandHistory    []string
	commandHistoryIdx int
	styles            uiStyles
	width             int
	height            int
	noteScroll        int
	noteReturnMode    mode // view to restore when the note/detail view closes
	noteConfirm       bool
	notePending       noteTarget
	confirmDel        bool
	pendingDel        *storage.Task
	pendingBatch      []storage.Task
	reportScroll      int
	trashScroll       int
	confirmTopic      bool
	pendingTopic      string
	trashSelected     map[int]bool
	trashConfirm      bool
	trashPending      []storage.TrashEntry
	selectedTasks     map[int]bool
	meta              *metaState
	note              *noteState
	renameID          int
	renameTopic       string
	renameIsTopic     bool
	calendarMonth     time.Time
	calendarDay       time.Time
	calendarDetail    bool
	ganttOffsetDays   int
	helpScroll        int
	statsScroll       int
	fortuneScroll     int
	configStage       configStage
	pendingCfgPath    string
	pendingDBPath     string
	workflows         map[string][]storage.Stage
	topicMeta         map[string]storage.TopicMeta
	workflowEdit      *workflowEditState
	dashboardCursor   int
	dashboardScroll   int
	dashEditing       string // "", "desc", or "target": which topic-meta field is being typed
	boardTopic        string // project shown in the kanban board
	boardCol          int
	boardRow          int
	undo              *undoEntry
	agendaHeaderFold  bool // hide the banner + I Ching reading to give the agenda body more room
}

// undoEntry holds a single reversible snapshot for the u key.
type undoEntry struct {
	task storage.Task // the task's state before the last in-place edit
	desc string       // human description, e.g. "status change"
}

// workflowEditState backs the per-topic workflow editor (modeWorkflow).
type workflowEditState struct {
	topic      string
	stages     []storage.Stage
	cursor     int
	editing    bool // a stage name is being typed
	adding     bool // the in-progress edit is a new stage (vs. rename)
	returnMode mode // view to restore when the editor closes
	dirty      bool // unsaved changes pending
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
		store:             store,
		cfg:               cfg,
		configPath:        configPath,
		tasks:             tasks,
		cursor:            clampCursor(0, len(tasks)),
		trashSelected:     map[int]bool{},
		selectedTasks:     map[int]bool{},
		status:            "",
		input:             ti,
		mode:              modeReport,
		recentLimit:       5,
		filterDone:        normalizeQuickFilter(cfg.DefaultFilter),
		sortMode:          "auto",
		currentTopic:      "",
		commandHistoryIdx: -1,
		styles:            buildStyles(cfg.Theme),
		workflows:         map[string][]storage.Stage{},
		topicMeta:         map[string]storage.TopicMeta{},
	}
	if wf, err := store.AllTopicWorkflows(); err == nil {
		m.workflows = wf
	}
	if tm, err := store.AllTopicMeta(); err == nil {
		m.topicMeta = tm
	}
	m.sortTasks()
	m.refreshReport()
	if firstLaunch {
		m, _ = m.startConfig()
	}

	// Run in the alternate screen buffer so quitting restores the terminal to
	// whatever was on screen before launch, leaving a clean screen on exit.
	program := tea.NewProgram(m, tea.WithAltScreen())
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
	case configEditedMsg:
		return m.handleConfigEdited(msg)
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
		if m.mode == modeFortune {
			return m.updateFortuneMode(msg.String())
		}
		if m.mode == modeDashboard {
			return m.updateDashboardMode(msg.String(), msg)
		}
		if m.mode == modeWorkflow {
			return m.updateWorkflowMode(msg.String(), msg)
		}
		if m.mode == modeBoard {
			return m.updateBoardMode(msg.String())
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

func (m Model) listViewScopedActive() bool {
	return m.searchActive() || m.quickFilterActive() || m.currentTopic != ""
}

func (m *Model) resetToOriginalListView() {
	m.searchQuery = ""
	m.searchFuzzy = false
	m.filterDone = "all"
	m.currentTopic = ""
	m.cursor = clampCursor(0, len(m.visibleItems()))
	m.status = "Back to original list"
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
	case "ctrl+c":
		return m, tea.Quit
	case m.cfg.Keys.Quit:
		if m.listViewScopedActive() {
			m.resetToOriginalListView()
			return m, nil
		}
		return m, tea.Quit
	case ":":
		return m.startCommand()
	case m.cfg.Keys.Search, "/":
		return m.startSearch()
	case "F":
		return m.startFuzzySearch()
	case m.cfg.Keys.Cancel, "esc":
		if m.listViewScopedActive() {
			m.resetToOriginalListView()
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
		next, err := m.advanceTaskStatus(task)
		if err != nil {
			m.status = fmt.Sprintf("status failed: %v", err)
			return m, nil
		}
		vis = m.visibleItems()
		m.cursor = clampCursor(m.cursor, len(vis))
		m.status = "Status: " + next
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
	case "u":
		return m.applyUndo()
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
		info := fmt.Sprintf("Task #%d • %s • %s", task.ID, task.Title, m.taskStatusLabel(task))
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
			info += " • priority:" + priorityLabel(task.Priority)
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
		m.applySortMode("due")
		m.status = "Sorted by due date (" + m.sortDirLabel() + ")"
	case m.cfg.Keys.SortPriority:
		m.applySortMode("priority")
		m.status = "Sorted by priority (" + m.sortDirLabel() + ")"
	case m.cfg.Keys.SortCreated:
		m.applySortMode("created")
		m.status = "Sorted by created time (" + m.sortDirLabel() + ")"
	case m.cfg.Keys.ThemeToggle:
		return m.toggleTheme()
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

// toggleTheme cycles to the next built-in preset (light → dark → purple → …),
// rebuilds the styles so the change is visible immediately, and persists it.
func (m Model) toggleTheme() (tea.Model, tea.Cmd) {
	names := config.ThemePresetNames()
	next := names[0]
	for i, n := range names {
		if strings.EqualFold(n, m.cfg.Theme.Preset) {
			next = names[(i+1)%len(names)]
			break
		}
	}
	return m.applyThemeCommand(next)
}

func (m Model) View() string {
	var b strings.Builder

	if m.meta != nil {
		return m.fillView(m.renderMetaModalView())
	}

	if m.mode == modeSearch && m.searchFuzzy {
		return m.fillView(m.renderFuzzySearchModalView())
	}

	if m.mode == modeNote {
		b.WriteString(m.renderNoteView())
		b.WriteString("\n\n")
		return m.fillView(b.String())
	}

	if m.mode == modeReport {
		footer := m.renderReportFooter()
		if m.height <= 0 {
			return m.fillView(m.renderReportHeader() + m.report + "\n" + footer)
		}
		// Assemble as explicit lines and pad before the footer so the "Press …"
		// hint stays pinned to the bottom (just above the status bar) regardless of
		// how short the agenda is.
		headerLines := strings.Split(strings.TrimRight(m.renderReportHeader(), "\n"), "\n")
		footerLines := strings.Split(footer, "\n")
		target := m.height - 1 // rows above the status bar
		bodyMax := target - len(headerLines) - len(footerLines) - 1
		if bodyMax < 0 {
			bodyMax = 0
		}
		out := make([]string, 0, target)
		out = append(out, headerLines...)
		out = append(out, strings.Split(m.renderReportWithHeight(bodyMax), "\n")...)
		for len(out) < target-len(footerLines) {
			out = append(out, "")
		}
		out = append(out, footerLines...)
		if len(out) > target {
			out = out[:target]
		}
		return m.fillView(strings.Join(out, "\n"))
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

	if m.mode == modeFortune {
		b.WriteString(m.renderFortuneView())
		return m.fillView(b.String())
	}

	if m.mode == modeDashboard {
		b.WriteString(m.renderDashboardView())
		return m.fillView(b.String())
	}

	if m.mode == modeWorkflow {
		b.WriteString(m.renderWorkflowView())
		return m.fillView(b.String())
	}

	if m.mode == modeBoard {
		b.WriteString(m.renderBoardView())
		return m.fillView(b.String())
	}

	footer := strings.TrimRight(m.renderFooterPanel(), "\n")
	showHints := m.mode == modeList && m.meta == nil
	hints := ""
	if showHints {
		hints = m.hintBar(m.listHints())
	}

	// overhead is everything except the Tasks box body: its two borders, the
	// key-hint line, and the bottom Detail pane. The Tasks box is then enlarged
	// to fill the remaining height instead of shrinking to wrap the list.
	overhead := 2 + countLines(footer) // 2 = panel borders
	if showHints {
		overhead += countLines(hints)
	}

	listMax := 0
	if m.height > 0 {
		listMax = (m.height - 1) - overhead
		if listMax < 1 {
			listMax = 1
		}
	}

	var body string
	if m.height > 0 {
		body = m.renderTaskListWithHeight(listMax)
		// Pad so the Tasks box fills its full height rather than wrapping a
		// short list.
		bodyLines := strings.Split(body, "\n")
		for len(bodyLines) < listMax {
			bodyLines = append(bodyLines, "")
		}
		body = strings.Join(bodyLines, "\n")
	} else {
		body = m.renderTaskList()
	}

	// Top: the enlarged task list. Then the key-hint line sits just above the
	// Detail pane, which is pinned to the bottom.
	top := m.panel(m.taskPanelTitle(), body)

	if m.height <= 0 {
		out := top
		if showHints {
			out += "\n" + hints
		}
		return m.fillView(out + "\n" + footer)
	}

	lines := strings.Split(top, "\n")
	if showHints {
		lines = append(lines, strings.Split(hints, "\n")...)
	}
	lines = append(lines, strings.Split(footer, "\n")...)
	return m.fillView(strings.Join(lines, "\n"))
}

// listHints returns the key-hint chips shown beneath the task list.
func (m Model) taskPanelTitle() string {
	title := "bada ∙ Tasks"
	if m.quickFilterActive() {
		title += " · " + m.filterDone
	}
	if m.searchActive() {
		if m.searchFuzzy {
			title += " · fuzzy"
		} else {
			title += " · search"
		}
	}
	return title
}

func (m Model) listHints() []keyHint {
	k := m.cfg.Keys
	quitLabel := "quit"
	if m.listViewScopedActive() {
		quitLabel = "back"
	}
	return []keyHint{
		{k.Quit, quitLabel},
		{k.Add, "add"},
		{k.Search, "search"},
		{"F/,f", "fuzzy"},
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
		return m.renderReportFooter()
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
		label := "Search: "
		if m.searchFuzzy {
			label = "Fuzzy search: "
		}
		b.WriteString(m.styles.Heading.Render(label))
		b.WriteString(m.input.View())
		return b.String()
	default:
		// List view: show the current task's details in its own framed pane.
		return m.panel("bada ∙ Detail", strings.TrimRight(m.renderMetadataPanel(), "\n"))
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

// reload refreshes tasks, workflows, and topic metadata from the store and
// re-sorts. Workflows/meta are reloaded too so edits made elsewhere (workflow
// editor, topic rename) are reflected immediately.
func (m *Model) reload() error {
	tasks, err := m.store.FetchTasks()
	if err != nil {
		return err
	}
	m.tasks = tasks
	if wf, err := m.store.AllTopicWorkflows(); err == nil {
		m.workflows = wf
	}
	if tm, err := m.store.AllTopicMeta(); err == nil {
		m.topicMeta = tm
	}
	m.sortTasks()
	return nil
}

// advanceTaskStatus rotates a task to the next status in its governing workflow
// (or the legacy PENDING→IN-PROGRESS→DONE cycle), persists it with the correct
// done flag, and reloads. Returns the new status label. Centralizing this is
// what keeps done-derivation consistent across every rotate entry point.
func (m *Model) advanceTaskStatus(t storage.Task) (string, error) {
	m.snapshotUndo(t, "status change")
	next := m.nextTaskStatus(t)
	done := m.statusMeansDone(t, next)
	if err := m.store.SetStatus(t.ID, next, done); err != nil {
		return "", err
	}
	if err := m.reload(); err != nil {
		return next, err
	}
	return next, nil
}

// snapshotUndo records a task's current state so the next u can revert the edit
// about to happen. Single level: each snapshot replaces the previous one.
func (m *Model) snapshotUndo(t storage.Task, desc string) {
	m.undo = &undoEntry{task: t, desc: desc}
}

// applyUndo reverts the last recorded in-place task edit.
func (m Model) applyUndo() (tea.Model, tea.Cmd) {
	if m.undo == nil {
		m.status = "Nothing to undo"
		return m, nil
	}
	entry := m.undo
	if err := m.store.OverwriteTask(entry.task); err != nil {
		m.status = fmt.Sprintf("undo failed: %v", err)
		return m, nil
	}
	m.undo = nil
	if err := m.reload(); err != nil {
		m.status = fmt.Sprintf("reload failed: %v", err)
		return m, nil
	}
	m.cursor = clampCursor(m.findVisibleTaskIndex(entry.task.ID), len(m.visibleItems()))
	m.refreshReport()
	m.status = "Undid " + entry.desc + " (#" + fmt.Sprintf("%d", entry.task.ID) + ")"
	return m, nil
}

func (m Model) toggleCurrentTaskStatus() (tea.Model, tea.Cmd) {
	task, ok := m.currentTask()
	if !ok {
		m.status = "No task selected"
		return m, nil
	}
	next, err := m.advanceTaskStatus(task)
	if err != nil {
		m.status = fmt.Sprintf("status failed: %v", err)
		return m, nil
	}
	m.cursor = clampCursor(m.cursor, len(m.visibleItems()))
	m.status = "Status: " + next
	return m, nil
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
