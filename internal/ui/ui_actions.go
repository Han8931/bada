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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"bada/internal/config"
	"bada/internal/storage"
)

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
	m.noteReturnMode = m.mode // return here (e.g. the gantt) when the note closes
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
	var hint string
	if m.note.target.kind == noteTask {
		hint = fmt.Sprintf("Press %s/%s/enter to close, %s to edit fields, n to edit note, d to delete note",
			m.cfg.Keys.Cancel, m.cfg.Keys.Quit, m.cfg.Keys.Edit)
	} else {
		hint = fmt.Sprintf("Press %s/%s/enter to close, %s to edit note, d to delete note",
			m.cfg.Keys.Cancel, m.cfg.Keys.Quit, m.cfg.Keys.Edit)
	}
	footerLine := m.styles.Muted.Render(hint)

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
	shown := bodyLines[start:end]
	for _, line := range shown {
		b.WriteString(line)
		b.WriteString("\n")
	}
	// Pad the body region out to its full height so the "Press …" hint stays
	// pinned to the bottom (just above the status bar) instead of floating
	// directly under a short note.
	for i := len(shown); i < available; i++ {
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

	styles.StatusOverdue = applyFg(applyBg(lipgloss.NewStyle().Bold(true), theme.Danger), readableTextColor(theme.Danger, theme.StatusAltFg))
	styles.StatusProgress = applyFg(applyBg(lipgloss.NewStyle().Bold(true), theme.Warning), readableTextColor(theme.Warning, theme.StatusAltFg))
	styles.StatusDone = applyFg(applyBg(lipgloss.NewStyle().Bold(true), theme.Success), readableTextColor(theme.Success, theme.StatusAltFg))
	styles.StatusPending = applyFg(lipgloss.NewStyle(), theme.Muted)

	styles.RowStripe = applyBg(lipgloss.NewStyle(), theme.RowStripeBg)

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

func readableTextColor(bg, fallback string) string {
	bg = strings.TrimPrefix(strings.TrimSpace(bg), "#")
	if len(bg) != 6 {
		if strings.TrimSpace(fallback) != "" {
			return fallback
		}
		return "#FFFFFF"
	}
	r, rErr := strconv.ParseInt(bg[0:2], 16, 64)
	g, gErr := strconv.ParseInt(bg[2:4], 16, 64)
	b, bErr := strconv.ParseInt(bg[4:6], 16, 64)
	if rErr != nil || gErr != nil || bErr != nil {
		if strings.TrimSpace(fallback) != "" {
			return fallback
		}
		return "#FFFFFF"
	}
	if (299*r + 587*g + 114*b) > 140000 {
		return "#0D1117"
	}
	return "#FFFFFF"
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
		topic:     strings.Join(orderTopicsPrimaryFirst(t.Topics, t.PrimaryTopic), ","),
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
	m.meta.dropdownOpen = false
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
	onSuggestable := m.meta.index == 1 || m.meta.index == 2 // Topics, Tags

	// While the Topic/Tags dropdown is open it captures navigation and selection;
	// any other key falls through so typing keeps filtering the list.
	if m.meta.dropdownOpen {
		switch key {
		case m.cfg.Keys.Cancel, "esc":
			m.meta.dropdownOpen = false
			m.status = "List closed"
			return m, nil
		case "up", "shift+tab", "ctrl+p":
			if n := len(m.meta.completions); n > 0 {
				m.meta.completionIdx = wrapIndex(m.meta.completionIdx-1, n)
			}
			return m, nil
		case "down", "tab", "ctrl+n":
			if n := len(m.meta.completions); n > 0 {
				m.meta.completionIdx = wrapIndex(m.meta.completionIdx+1, n)
			}
			return m, nil
		case m.cfg.Keys.Confirm, "enter":
			m.applyDropdownSelection()
			return m, nil
		}
	}

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
	case "tab":
		// On Topic/Tags, Tab opens a dropdown of previously-used values instead
		// of advancing the field.
		if onSuggestable {
			m.openDropdown()
			return m, nil
		}
		m.metaMove(1)
		return m, nil
	case "down":
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
	// Keep the open dropdown filtered to what's being typed in the last token.
	if m.meta.dropdownOpen {
		m.meta.completions = m.metaCompletions(m.meta.index, dropdownPrefix(m.input.Value()))
		if m.meta.completionIdx >= len(m.meta.completions) {
			m.meta.completionIdx = 0
		}
	}
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

// dropdownPrefix returns the token currently being typed in a comma-separated
// field (Topics/Tags) — the text after the last comma — so the dropdown filters
// on it while keeping any already-entered values intact.
func dropdownPrefix(s string) string {
	if i := strings.LastIndex(s, ","); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}

// openDropdown shows the list of previously-used Topics/Tags for the current
// field, filtered by what's already typed.
func (m *Model) openDropdown() {
	if m.meta == nil {
		return
	}
	m.meta.setCurrentValue(m.input.Value())
	m.meta.completions = m.metaCompletions(m.meta.index, dropdownPrefix(m.input.Value()))
	m.meta.completionIdx = 0
	if len(m.meta.completions) == 0 {
		m.meta.dropdownOpen = false
		m.status = "No previously-used values"
		return
	}
	m.meta.dropdownOpen = true
	m.status = "↑↓ select · ⏎ use · esc close"
}

// applyDropdownSelection inserts the highlighted suggestion into the field,
// replacing the last comma-separated token, then closes the dropdown.
func (m *Model) applyDropdownSelection() {
	if m.meta == nil {
		return
	}
	if len(m.meta.completions) == 0 {
		m.meta.dropdownOpen = false
		return
	}
	sel := m.meta.completions[m.meta.completionIdx]
	cur := m.input.Value()
	newVal := sel
	if i := strings.LastIndex(cur, ","); i >= 0 {
		newVal = strings.TrimRight(cur[:i+1], " ") + " " + sel
	}
	m.input.SetValue(newVal)
	m.input.CursorEnd()
	m.meta.setCurrentValue(newVal)
	m.meta.dropdownOpen = false
	m.status = "Selected: " + sel
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

// stepPriority nudges the Priority field within the 0–3 range.
func (m *Model) stepPriority(delta int) {
	if m.meta == nil {
		return
	}
	val, _ := strconv.Atoi(filterDigits(m.meta.priority))
	val += delta
	if val < 0 {
		val = 0
	}
	if val > maxPriority {
		val = maxPriority
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

	var prevTask *storage.Task
	if taskID == 0 {
		newID, err := m.store.AddTask(title)
		if err != nil {
			return m, err
		}
		taskID = newID
	} else if idx := m.findTaskIndex(taskID); idx >= 0 {
		// Remember the pre-edit state; it becomes the undo snapshot once the
		// writes below have succeeded.
		prev := m.tasks[idx]
		prevTask = &prev
	}
	if err := m.store.UpdateTaskMetadata(taskID, m.meta.topic, m.meta.tags, m.meta.assignee, m.meta.reporter, timezone, priority, due, start, end, recurring); err != nil {
		return m, err
	}
	// The first listed topic governs the task's status workflow (its "project").
	if err := m.store.SetPrimaryTopic(taskID, firstTopic(m.meta.topic)); err != nil {
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
	if prevTask != nil {
		m.snapshotUndo(*prevTask, "edit")
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
	if val > maxPriority {
		val = maxPriority
	}
	return val, nil
}

func parseDate(v string) (sql.NullTime, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", v, time.Local)
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
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
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
func (m Model) renderFuzzySearchModalView() string {
	inner := m.panelInnerWidth()
	listMax := 0
	if m.height > 0 {
		listMax = (m.height - 1) - 2
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

	box := m.renderFuzzySearchModal()
	boxLines := strings.Split(box, "\n")
	boxW := 0
	for _, l := range boxLines {
		if w := lipgloss.Width(l); w > boxW {
			boxW = w
		}
	}
	rows := len(bodyLines)
	// Telescope-style: float near the upper middle so results open below input.
	top := rows / 5
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
	return m.panel(m.taskPanelTitle(), strings.Join(bodyLines, "\n"))
}

func (m Model) renderFuzzySearchModal() string {
	boxInner := 62
	if max := m.panelInnerWidth() - 6; boxInner > max {
		boxInner = max
	}
	if boxInner < 32 {
		boxInner = 32
	}

	matches := m.fuzzyMatches()
	total := len(matches)
	cursor := 0
	if total > 0 {
		cursor = clampInt(m.searchCursor, 0, total-1)
	}

	// Window the results around the cursor so it stays visible.
	start := 0
	if cursor >= fuzzyVisibleRows {
		start = cursor - fuzzyVisibleRows + 1
	}
	if start > total-fuzzyVisibleRows {
		start = total - fuzzyVisibleRows
	}
	if start < 0 {
		start = 0
	}
	end := start + fuzzyVisibleRows
	if end > total {
		end = total
	}

	lines := []string{
		m.styles.Accent.Render(padRightWidth("› "+m.input.View(), boxInner)),
		m.styles.Muted.Render(padRightWidth(fmt.Sprintf("%d result(s) · ↑/↓ move · Enter jump · Esc cancel", total), boxInner)),
	}
	if total == 0 {
		lines = append(lines, m.styles.Muted.Render(padRightWidth("No matches", boxInner)))
	} else {
		for i := start; i < end; i++ {
			it := matches[i]
			if it.kind != itemTask {
				continue
			}
			status := m.taskStatusLabel(it.task)
			line := fmt.Sprintf("#%-4d %-12s %s", it.task.ID, status, truncateText(it.task.Title, boxInner-19))
			meta := "      " + truncateText(fuzzyResultMeta(it.task), boxInner-6)
			if i == cursor {
				lines = append(lines, m.styles.Selection.Render(padRightWidth(line, boxInner)))
				lines = append(lines, m.styles.Selection.Render(padRightWidth(meta, boxInner)))
			} else {
				lines = append(lines, padRightWidth(line, boxInner))
				lines = append(lines, m.styles.Muted.Render(padRightWidth(meta, boxInner)))
			}
		}
	}
	return m.modalFrame("Fuzzy Find", lines, boxInner)
}

func fuzzyResultMeta(t storage.Task) string {
	parts := make([]string, 0, 8)
	if len(t.Topics) > 0 {
		parts = append(parts, "topic:"+strings.Join(t.Topics, ","))
	}
	if strings.TrimSpace(t.Tags) != "" {
		parts = append(parts, "tags:"+t.Tags)
	}
	if strings.TrimSpace(t.Assignee) != "" {
		parts = append(parts, "assignee:"+t.Assignee)
	}
	if strings.TrimSpace(t.Reporter) != "" {
		parts = append(parts, "reporter:"+t.Reporter)
	}
	if t.Priority > 0 {
		parts = append(parts, fmt.Sprintf("p%d", t.Priority), priorityLabel(t.Priority))
	}
	if t.Due.Valid {
		parts = append(parts, "due:"+formatDateTime(t.Due))
	}
	if t.Recurring {
		parts = append(parts, "recurring")
	}
	if strings.TrimSpace(t.Notes) != "" {
		parts = append(parts, "notes:"+strings.TrimSpace(t.Notes))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

func (m Model) renderMetaModalView() string {
	inner := m.panelInnerWidth()

	listMax := 0
	if m.height > 0 {
		listMax = (m.height - 1) - 2 // panel borders (2)
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

	return m.panel("bada ∙ Tasks", strings.Join(bodyLines, "\n"))
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
		if active && m.meta.dropdownOpen {
			lines = append(lines, m.renderModalDropdown()...)
		}
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

// renderModalDropdown lists previously-used Topic/Tag values under the active
// field, highlighting the current selection and windowing long lists.
func (m Model) renderModalDropdown() []string {
	items := m.meta.completions
	label := "tags"
	if m.meta.index == 1 {
		label = "topics"
	}
	if len(items) == 0 {
		return []string{m.styles.Muted.Render("    (no matching " + label + ")")}
	}
	const maxShow = 6
	start := 0
	if m.meta.completionIdx >= maxShow {
		start = m.meta.completionIdx - maxShow + 1
	}
	end := start + maxShow
	if end > len(items) {
		end = len(items)
	}
	out := make([]string, 0, maxShow+2)
	out = append(out, m.styles.Muted.Render(fmt.Sprintf("    previously used %s:", label)))
	for i := start; i < end; i++ {
		if i == m.meta.completionIdx {
			out = append(out, m.styles.Selection.Render("  ▸ "+items[i]))
		} else {
			out = append(out, m.styles.Muted.Render("    "+items[i]))
		}
	}
	if len(items) > end {
		out = append(out, m.styles.Muted.Render(fmt.Sprintf("    … +%d more", len(items)-end)))
	}
	return out
}

// modalHintLine renders the keys relevant to the current field.
func (m Model) modalHintLine() string {
	if m.meta.dropdownOpen {
		return m.styles.Muted.Render("↑↓:select · ⏎:use · esc:close · type to filter")
	}
	switch m.meta.index {
	case 4: // Due stepper
		return m.styles.Muted.Render("type digits or +/-:set · ←→:part · x:no due · tab:next · ⏎:save")
	case 3: // Priority stepper
		return m.styles.Muted.Render("+/-:priority · tab:next · ⏎:save · esc:cancel")
	case 1, 2: // Topics, Tags
		return m.styles.Muted.Render("tab:pick from list · ↑↓:move · ⏎:save · esc:cancel")
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
	disp := m.styles.Muted.Render("— none")
	if v := strings.TrimSpace(m.meta.priority); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			if p <= 0 {
				disp = m.styles.Muted.Render("— none")
			} else {
				disp = m.priorityBadge(p) + " " + m.priorityStyle(p).Render(priorityLabel(p))
			}
		} else {
			disp = v
		}
	}
	if active {
		return m.styles.Accent.Render("‹ ") + disp + m.styles.Accent.Render(" ›")
	}
	return disp
}

// maxPriority is the highest priority level. Priority is a 3-level scale:
// 0 = none, 1 = Low, 2 = Med, 3 = High.
const maxPriority = 3

// priorityLabel names a priority level: "" (none), Low, Med, or High.
func priorityLabel(p int) string {
	switch {
	case p <= 0:
		return ""
	case p == 1:
		return "Low"
	case p == 2:
		return "Med"
	default:
		return "High"
	}
}

// priorityStyle maps a priority (higher = more urgent; 0 = none) to its color.
// The ramp runs muted → green (Low) → amber (Med) → red (High).
func (m Model) priorityStyle(p int) lipgloss.Style {
	switch {
	case p <= 0:
		return m.styles.Muted
	case p == 1:
		return m.styles.Success
	case p == 2:
		return m.styles.Warning
	default: // 3 = High
		return m.styles.Danger
	}
}

// priorityBadge renders a colored flag instead of the unintuitive "P0/P1" text;
// the color alone conveys urgency. Priority 0 shows a muted outline flag.
func (m Model) priorityBadge(p int) string {
	if p <= 0 {
		return m.styles.Muted.Render("⚐")
	}
	return m.priorityStyle(p).Render("⚑")
}

// priorityCell renders the flag plus its Low/Med/High label, for detail panels.
func (m Model) priorityCell(p int) string {
	if p <= 0 {
		return m.styles.Muted.Render("⚐ none")
	}
	return m.priorityBadge(p) + " " + m.priorityStyle(p).Render(priorityLabel(p))
}

// statusCell renders a task's real status (or custom workflow stage) as a colored
// badge for detail panels, with a separate "overdue" tag when applicable — so the
// underlying status isn't masked the way the list's folded OVERDUE badge does.
func (m Model) statusCell(t storage.Task) string {
	var label string
	switch {
	case m.hasWorkflow(t):
		stages, _ := m.governingWorkflow(t)
		label = stages[currentStageIndex(stages, t.Status)].Name
	case isDone(t):
		label = "DONE"
	case strings.EqualFold(strings.TrimSpace(t.Status), "IN-PROGRESS"):
		label = "IN-PROGRESS"
	default:
		label = "PENDING"
	}
	cat := m.stageCategory(t)
	var badge string
	switch cat {
	case storage.StageDone:
		badge = m.styles.StatusDone.Render(" " + label + " ")
	case storage.StagePending:
		badge = m.styles.StatusPending.Render(label)
	default:
		badge = m.styles.StatusProgress.Render(" " + label + " ")
	}
	if cat != storage.StageDone && isOverdue(t) {
		badge += " " + m.styles.StatusOverdue.Render(" overdue ")
	}
	return badge
}

// hasWorkflow reports whether a task is governed by a custom workflow.
func (m Model) hasWorkflow(t storage.Task) bool {
	_, ok := m.governingWorkflow(t)
	return ok
}

// priorityField returns the fixed 4-wide task-list cell for a priority, colored
// only when the surrounding row isn't itself recolored (selection/done), so the
// flag's color never fights the row highlight.
func (m Model) priorityField(p int, colored bool) string {
	txt := "⚐"
	if p > 0 {
		txt = "⚑"
	}
	pad := 4 - lipgloss.Width(txt)
	if pad < 0 {
		pad = 0
	}
	cell := txt
	if colored {
		if p <= 0 {
			cell = m.styles.Muted.Render(txt)
		} else {
			cell = m.priorityStyle(p).Render(txt)
		}
	}
	return cell + strings.Repeat(" ", pad)
}

// statusField returns the status-column cell padded to width. When colored, the
// state renders as a filled badge (overdue red, in-progress amber, done green);
// pending is plain muted text. Like priorityField, it's only colored on default
// rows — selection/done rows recolor the whole line, so it returns a plain label
// there to avoid clashing. In-progress is abbreviated so the padded badge fits.
func (m Model) statusField(t storage.Task, width int, colored bool) string {
	if stages, ok := m.governingWorkflow(t); ok {
		return m.workflowStatusField(t, stages, width, colored)
	}
	label := taskStatusLabel(t)
	if !colored {
		return fmt.Sprintf("%-*s", width, label)
	}
	var rendered string
	switch label {
	case "OVERDUE":
		rendered = m.styles.StatusOverdue.Render(" OVERDUE ")
	case "IN-PROGRESS":
		rendered = m.styles.StatusProgress.Render(" IN-PROG ")
	case "DONE":
		rendered = m.styles.StatusDone.Render(" DONE ")
	default: // PENDING
		rendered = m.styles.StatusPending.Render(label)
	}
	pad := width - lipgloss.Width(rendered)
	if pad < 0 {
		pad = 0
	}
	return rendered + strings.Repeat(" ", pad)
}

// workflowStatusField renders a custom-workflow stage badge. The color comes
// from the stage's category; an overdue, not-yet-done task overlays the red
// Overdue style with a trailing "!" while keeping the stage name visible.
// Long custom names are truncated to fit the column.
func (m Model) workflowStatusField(t storage.Task, stages []storage.Stage, width int, colored bool) string {
	idx := currentStageIndex(stages, t.Status)
	stage := stages[idx]
	overdue := stage.Category != storage.StageDone && isOverdue(t)
	label := stage.Name
	// Reserve room for the surrounding spaces (and the "!" when overdue).
	max := width - 2
	if overdue {
		max--
	}
	if max < 1 {
		max = 1
	}
	label = truncateText(label, max)
	if !colored {
		text := label
		if overdue {
			text += "!"
		}
		return fmt.Sprintf("%-*s", width, text)
	}
	var rendered string
	switch {
	case overdue:
		rendered = m.styles.StatusOverdue.Render(" " + label + "! ")
	case stage.Category == storage.StageDone:
		rendered = m.styles.StatusDone.Render(" " + label + " ")
	case stage.Category == storage.StagePending:
		rendered = m.styles.StatusPending.Render(label)
	default: // active
		rendered = m.styles.StatusProgress.Render(" " + label + " ")
	}
	pad := width - lipgloss.Width(rendered)
	if pad < 0 {
		pad = 0
	}
	return rendered + strings.Repeat(" ", pad)
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
		{label: "Status", value: ""},
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
		rows[1].value = m.statusCell(task)
		rows[2].value = emptyPlaceholder(strings.Join(task.Topics, ", "))
		rows[3].value = emptyPlaceholder(task.Tags)
		rows[4].value = emptyPlaceholder(task.Assignee)
		rows[5].value = emptyPlaceholder(task.Reporter)
		rows[6].value = m.priorityCell(task.Priority)
		rows[7].value = emptyPlaceholder(formatDateTime(task.Due))
		if task.Due.Valid {
			if name, ok := m.holidayName(normalizeDate(task.Due.Time)); ok && name != "" {
				rows[7].value += "  " + m.styles.Danger.Render("· "+name)
			}
		}
		rows[8].value = defaultStart(task)
		rows[9].value = emptyPlaceholder(formatDateTime(task.End))
		rows[10].value = defaultTimezone(task.Timezone)
		if recSummary := recurrenceSummary(task); recSummary != "" {
			if next, ok := nextRecurrenceDate(task); ok {
				rows[11].value = fmt.Sprintf("%s • Next: %s", recSummary, next.Format("2006-01-02"))
			} else {
				rows[11].value = recSummary
			}
		} else {
			rows[11].value = "off"
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
			{label: "Status", value: m.statusCell(task)},
			{label: "Topics", value: emptyPlaceholder(strings.Join(task.Topics, ", "))},
			{label: "Tags", value: emptyPlaceholder(task.Tags)},
			{label: "Assignee", value: emptyPlaceholder(task.Assignee)},
			{label: "Reporter", value: emptyPlaceholder(task.Reporter)},
			{label: "Priority", value: m.priorityCell(task.Priority)},
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
		"██████╗  █████╗ ██████╗  █████╗ ",
		"██╔══██╗██╔══██╗██╔══██╗██╔══██╗",
		"██████╔╝███████║██║  ██║███████║",
		"██╔══██╗██╔══██║██║  ██║██╔══██║",
		"██████╔╝██║  ██║██████╔╝██║  ██║",
		"╚═════╝ ╚═╝  ╚═╝╚═════╝ ╚═╝  ╚═╝",
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
	base := m.styles.Status
	if m.mode == modeTrash || m.mode == modeNote {
		base = m.styles.StatusAlt
	}
	th := m.cfg.Theme

	// chip renders a bold, distinctly-backed badge; seg/sp keep the status
	// background while tinting the foreground so sections stay separable.
	chip := func(s, bg, fg string) string {
		st := base.Bold(true)
		if bg != "" {
			st = st.Background(lipgloss.Color(bg))
		}
		if fg != "" {
			st = st.Foreground(lipgloss.Color(fg))
		}
		return st.Render(" " + s + " ")
	}
	seg := func(s, fg string) string {
		st := base
		if fg != "" {
			st = st.Foreground(lipgloss.Color(fg))
		}
		return st.Render(s)
	}
	sp := base.Render("  ")
	gap := base.Render(" ")

	brand := chip("bada", th.Accent, "#FFFFFF")
	modeC := chip(m.modeLabel(), th.StatusAltBg, th.StatusAltFg)
	head := brand + gap + modeC

	var content string
	switch {
	case m.mode == modeReport:
		content = head + sp + seg(m.status, "")
	case m.mode == modeNote:
		target := ""
		if m.note != nil {
			target = m.note.target.label()
		}
		content = head + sp + seg(target, th.Heading) + sp + seg(m.status, "")
	case m.mode == modeTrash:
		sel := m.selectedTrashCount()
		total := len(m.trash)
		cur := 0
		if total > 0 {
			cur = m.trashCursor + 1
		}
		content = head + sp +
			seg(fmt.Sprintf("%d/%d", cur, total), th.Success) + gap +
			seg(fmt.Sprintf("sel:%d", sel), th.Warning) + sp +
			seg("path:"+m.store.TrashDir(), th.Muted) + sp +
			seg(m.status, "")
	default:
		total := len(m.visibleItems())
		cursor := 0
		if total > 0 {
			cursor = clampCursor(m.cursor, total) + 1
		}
		// Small triangles (not ↑/↓, which are East-Asian ambiguous width and
		// render as two cells in a CJK terminal) keep the status bar from
		// overflowing.
		sortArrow := "▴"
		if m.sortReversed {
			sortArrow = "▾"
		}
		content = head + sp +
			seg("sort:", th.Muted) + seg(m.sortMode+sortArrow, th.Accent) + sp +
			seg(fmt.Sprintf("%d/%d", cursor, total), th.Success)
		if m.quickFilterActive() {
			content += sp + seg("filter:", th.Muted) + seg(m.filterDone, th.Warning)
		}
		if m.searchActive() {
			label := "search"
			if m.searchFuzzy {
				label = "fuzzy"
			}
			content += sp + seg(fmt.Sprintf("%s:%q", label, m.searchQuery), th.Warning)
		}
		if strings.TrimSpace(m.status) != "" {
			content += sp + seg(m.status, th.Heading)
		}
	}
	return m.padStatusBar(base, content)
}

// padStatusBar fills the status line to the full width with the bar background
// (or truncates it), so the colored bar spans the whole row.
func (m Model) padStatusBar(base lipgloss.Style, content string) string {
	if m.width <= 0 {
		return content
	}
	// Target two cells short of the full width. The status bar is the bottom row;
	// writing near the last cell can leave some terminals in a pending-wrap state
	// that scrolls the alt-screen by a line on the next repaint — which shows up as
	// the top row vanishing and the status bar appearing duplicated, most often in
	// the Gantt view where navigation triggers dense full-row re-renders.
	target := m.width - 2
	if target < 1 {
		target = 1
	}
	w := lipgloss.Width(content)
	if w >= target {
		return truncateANSI(content, target)
	}
	return content + base.Render(strings.Repeat(" ", target-w))
}

func (m Model) fillView(body string) string {
	statusBar := m.renderStatusBar()
	if m.height <= 0 {
		return clearLineEnds(body + "\n" + statusBar)
	}
	target := m.height - 1
	if target < 1 {
		target = 1
	}
	lines := strings.Split(body, "\n")
	// Clip every line two cells short of the terminal width. Framed views should
	// already fit this, but unframed/ANSI-heavy views (notably Gantt) must never
	// write near the right edge: doing so can trigger pending-wrap and make the
	// next repaint look like an extra status bar was inserted at the bottom.
	if m.width > 0 {
		maxWidth := m.width - 2
		if maxWidth < 1 {
			maxWidth = 1
		}
		for i, l := range lines {
			if lipgloss.Width(l) > maxWidth {
				lines[i] = truncateANSI(l, maxWidth)
			}
		}
	}
	for len(lines) < target {
		lines = append(lines, "")
	}
	if len(lines) > target {
		lines = lines[:target]
	}
	return clearLineEnds(strings.Join(lines, "\n") + "\n" + statusBar)
}

func clearLineEnds(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.HasSuffix(line, "\x1b[K") {
			continue
		}
		lines[i] = line + "\x1b[K"
	}
	return strings.Join(lines, "\n")
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
	case modeFortune:
		return "LESSON"
	default:
		return "?"
	}
}

func (m Model) startCommand() (tea.Model, tea.Cmd) {
	m.mode = modeCommand
	m.input.SetValue("")
	m.input.Placeholder = ""
	m.input.Focus()
	m.commandHistoryIdx = len(m.commandHistory)
	m.status = "Command: type a command (tab to autocomplete), ↑/↓ history, Enter to run, Esc to cancel"
	return m, nil
}

// openConfigFile launches $EDITOR (or $VISUAL, else vi) on the active config
// file. When the editor exits, configEditedMsg triggers a live reload so theme
// and keybinding changes take effect without a restart.
func (m Model) openConfigFile() (tea.Model, tea.Cmd) {
	// Leave command mode before suspending for the editor, so returning from it
	// lands back in the normal list view rather than the ":" prompt.
	m.mode = modeList
	m.input.Blur()
	path := strings.TrimSpace(m.configPath)
	if path == "" {
		m.status = "No config file path is set"
		return m, nil
	}
	// LoadOrCreate on startup writes the file, but guard in case it was removed.
	if _, err := config.LoadOrCreate(path); err != nil {
		m.status = fmt.Sprintf("config open failed: %v", err)
		return m, nil
	}
	editor := resolveEditor()
	if len(editor) == 0 {
		m.status = "No editor set ($EDITOR/$VISUAL)"
		return m, nil
	}
	cmd := exec.Command(editor[0], append(editor[1:], path)...)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return configEditedMsg{err: err}
	})
}

// handleConfigEdited reloads the config after the editor closes and re-applies
// the theme (keybindings are read from m.cfg on each keystroke, so they update
// too). db_path/trash_dir changes need a restart since the store is already open.
func (m Model) handleConfigEdited(msg configEditedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("config edit failed: %v", msg.err)
		return m, nil
	}
	cfg, err := config.LoadOrCreate(m.configPath)
	if err != nil {
		m.status = fmt.Sprintf("config reload failed: %v", err)
		return m, nil
	}
	m.cfg = cfg
	m.styles = buildStyles(cfg.Theme)
	m.status = "Config reloaded"
	return m, nil
}

// applyThemeCommand backs the :theme command (and the theme-toggle key). With an
// empty name it reports the available palettes and the current one; with a name
// it switches to that preset, restyles immediately, and saves the choice.
func (m Model) applyThemeCommand(name string) (tea.Model, tea.Cmd) {
	names := config.ThemePresetNames()
	if name == "" {
		current := strings.TrimSpace(m.cfg.Theme.Preset)
		if current == "" {
			current = "custom"
		}
		m.status = fmt.Sprintf("Themes: %s (current: %s) — :theme <name>", strings.Join(names, ", "), current)
		return m, nil
	}
	theme, ok := config.PresetTheme(name)
	if !ok {
		m.status = fmt.Sprintf("Unknown theme %q. Available: %s", name, strings.Join(names, ", "))
		return m, nil
	}
	m.cfg.Theme = theme
	m.styles = buildStyles(theme)
	if err := config.Save(m.configPath, m.cfg); err != nil {
		m.status = fmt.Sprintf("Theme: %s (not saved: %v)", theme.Preset, err)
		return m, nil
	}
	m.status = "Theme: " + theme.Preset
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
	m.searchFuzzy = false
	m.input.SetValue(m.searchQuery)
	m.input.Placeholder = "Search tasks"
	m.input.Focus()
	m.status = "Search: type a query, Enter to apply, Esc to cancel"
	return m, nil
}

func (m Model) startFuzzySearch() (tea.Model, tea.Cmd) {
	m.mode = modeSearch
	m.searchFuzzy = true
	m.searchCursor = 0
	m.input.SetValue(m.searchQuery)
	m.input.Placeholder = "Fuzzy search tasks"
	m.input.Focus()
	m.status = "Fuzzy: type to filter, ↑/↓ to move, Enter to jump, Esc to cancel"
	return m, nil
}

// fuzzyMatches returns the current fuzzy-find candidates for the typed query
// (all of them, unwindowed), shared by the modal renderer and the key handler.
func (m Model) fuzzyMatches() []listItem {
	query := strings.TrimSpace(m.input.Value())
	candidates := m.applyQuickFilterToItems(m.defaultVisibleItems())
	matches := filterItemsByQuery(candidates, query, true)
	if query == "" {
		matches = candidates
	}
	return matches
}

// fuzzyVisibleRows is how many result rows the fuzzy modal shows at once.
const fuzzyVisibleRows = 8

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
	case "up":
		m.recallCommandHistory(-1)
		return m, nil
	case "down":
		m.recallCommandHistory(1)
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		cmd := strings.TrimSpace(m.input.Value())
		m.pushCommandHistory(cmd)
		cmdLower := strings.TrimPrefix(strings.ToLower(cmd), ":")
		// "stage <name>" / "board <topic>" take an argument, so handle prefixes
		// before the exact-match switch.
		if arg, ok := strings.CutPrefix(cmdLower, "stage "); ok {
			m.applyQuickFilter("stage:" + strings.TrimSpace(arg))
			m.mode = modeList
			m.input.Blur()
			return m, nil
		}
		// ":theme" lists available palettes; ":theme <name>" switches to one.
		if cmdLower == "theme" || strings.HasPrefix(cmdLower, "theme ") {
			m.mode = modeList
			m.input.Blur()
			return m.applyThemeCommand(strings.TrimSpace(strings.TrimPrefix(cmdLower, "theme")))
		}
		// The stage board takes an optional project argument. ":board" is a
		// legacy alias for ":kanban".
		for _, pfx := range []string{"kanban", "board"} {
			if arg, ok := strings.CutPrefix(cmdLower, pfx); ok {
				return m.enterBoardView(strings.TrimSpace(strings.TrimPrefix(arg, " ")))
			}
		}
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
		case "dashboard", "projects", "topics":
			return m.enterDashboardView()
		case "config":
			return m.openConfigFile()
		case "all", "clear", "reset", "overdue", "pending", "done", "completed", "progress", "in-progress", "today", "week":
			m.applyQuickFilter(cmdLower)
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

func (m *Model) pushCommandHistory(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	if len(m.commandHistory) > 0 && m.commandHistory[len(m.commandHistory)-1] == cmd {
		m.commandHistoryIdx = len(m.commandHistory)
		return
	}
	m.commandHistory = append(m.commandHistory, cmd)
	if len(m.commandHistory) > 100 {
		m.commandHistory = m.commandHistory[len(m.commandHistory)-100:]
	}
	m.commandHistoryIdx = len(m.commandHistory)
}

func (m *Model) recallCommandHistory(delta int) {
	if len(m.commandHistory) == 0 {
		return
	}
	if m.commandHistoryIdx < 0 || m.commandHistoryIdx > len(m.commandHistory) {
		m.commandHistoryIdx = len(m.commandHistory)
	}
	m.commandHistoryIdx += delta
	if m.commandHistoryIdx < 0 {
		m.commandHistoryIdx = 0
	}
	if m.commandHistoryIdx >= len(m.commandHistory) {
		m.commandHistoryIdx = len(m.commandHistory)
		m.input.SetValue("")
		m.input.CursorEnd()
		return
	}
	m.input.SetValue(m.commandHistory[m.commandHistoryIdx])
	m.input.CursorEnd()
}

func completeCommand(input string) string {
	raw := strings.TrimSpace(input)
	prefix := ""
	if strings.HasPrefix(raw, ":") {
		prefix = ":"
		raw = strings.TrimPrefix(raw, ":")
	}
	cmd := strings.ToLower(raw)
	// ":theme <Tab>" completes/cycles the available palette names, so users can
	// discover the options without knowing them in advance.
	if cmd == "theme" || strings.HasPrefix(cmd, "theme ") {
		names := config.ThemePresetNames()
		partial := strings.TrimSpace(strings.TrimPrefix(cmd, "theme"))
		// An exact match advances to the next name, so repeated Tab cycles them.
		for i, n := range names {
			if n == partial {
				return prefix + "theme " + names[(i+1)%len(names)]
			}
		}
		for _, n := range names {
			if strings.HasPrefix(n, partial) {
				return prefix + "theme " + n
			}
		}
		return prefix + "theme " + names[0]
	}
	commands := []string{"agenda", "all", "calendar", "config", "done", "gantt", "help", "in-progress", "kanban", "overdue", "pending", "projects", "quit", "stage", "stats", "theme", "today", "week"}
	if cmd == "" {
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
	case "up", "ctrl+p":
		if m.searchFuzzy {
			if m.searchCursor > 0 {
				m.searchCursor--
			}
			return m, nil
		}
	case "down", "ctrl+n":
		if m.searchFuzzy {
			if n := len(m.fuzzyMatches()); n > 0 {
				m.searchCursor = clampInt(m.searchCursor+1, 0, n-1)
			}
			return m, nil
		}
	case m.cfg.Keys.Confirm, "enter":
		// In fuzzy mode, Enter jumps straight to the highlighted result.
		if m.searchFuzzy {
			matches := m.fuzzyMatches()
			if m.searchCursor >= 0 && m.searchCursor < len(matches) && matches[m.searchCursor].kind == itemTask {
				target := matches[m.searchCursor].task.ID
				m.searchQuery = ""
				m.searchFuzzy = false
				m.mode = modeList
				m.input.Blur()
				m.cursor = clampCursor(m.findVisibleTaskIndex(target), len(m.visibleItems()))
				m.status = fmt.Sprintf("Jumped to #%d", target)
				return m, nil
			}
		}
		m.searchQuery = strings.TrimSpace(m.input.Value())
		m.mode = modeList
		m.input.Blur()
		if m.searchActive() {
			if m.searchFuzzy {
				m.status = fmt.Sprintf("Fuzzy search: %s", m.searchQuery)
			} else {
				m.status = fmt.Sprintf("Search: %s", m.searchQuery)
			}
		} else {
			m.searchFuzzy = false
			m.status = "Search cleared"
		}
		m.cursor = clampCursor(0, len(m.visibleItems()))
		return m, nil
	}
	// Typing (any other key) edits the query; a changed result set resets the
	// highlight to the top.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.searchCursor = 0
	return m, cmd
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
	if newPrio > maxPriority {
		newPrio = maxPriority
	}
	if err := m.store.UpdatePriority(t.ID, newPrio); err != nil {
		m.status = fmt.Sprintf("priority failed: %v", err)
		return m, nil
	}
	m.snapshotUndo(t, "priority change")
	if idx := m.findTaskIndex(t.ID); idx >= 0 && idx < len(m.tasks) {
		m.tasks[idx].Priority = newPrio
	}
	m.pendingSort = true
	if newPrio <= 0 {
		m.status = "Priority cleared"
	} else {
		m.status = "Priority: " + priorityLabel(newPrio)
	}
	return m, nil
}

func (m Model) shiftDue(days int) (tea.Model, tea.Cmd) {
	t, ok := m.currentTask()
	if !ok {
		return m, nil
	}
	newTime, err := m.store.ShiftDue(t.ID, days)
	if err != nil {
		m.status = fmt.Sprintf("shift due failed: %v", err)
		return m, nil
	}
	m.snapshotUndo(t, "due change")
	if idx := m.findTaskIndex(t.ID); idx >= 0 && idx < len(m.tasks) {
		m.tasks[idx].Due = sql.NullTime{Time: newTime, Valid: true}
	}
	m.pendingSort = true
	m.status = fmt.Sprintf("Due shifted by %+dd", days)
	return m, nil
}

// applySortMode sets the sort key, or—if it's already active—flips the
// direction, so pressing the same sort again reverses the order.
func (m *Model) applySortMode(mode string) {
	if m.sortMode == mode {
		m.sortReversed = !m.sortReversed
	} else {
		m.sortMode = mode
		m.sortReversed = false
	}
	m.sortTasks()
}

// sortDirLabel describes the current direction for status messages.
func (m Model) sortDirLabel() string {
	if m.sortReversed {
		return "reversed"
	}
	return "default order"
}

func (m *Model) sortTasks() {
	switch m.sortMode {
	case "auto":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			a := m.tasks[i]
			b := m.tasks[j]
			if m.stateRank(a) != m.stateRank(b) {
				return m.stateRank(a) < m.stateRank(b)
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
			if m.stateRank(a) != m.stateRank(b) {
				return m.stateRank(a) < m.stateRank(b)
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
	case "title":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			ti := strings.ToLower(strings.TrimSpace(m.tasks[i].Title))
			tj := strings.ToLower(strings.TrimSpace(m.tasks[j].Title))
			if ti != tj {
				return ti < tj
			}
			return m.tasks[i].ID < m.tasks[j].ID
		})
	case "created":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			return m.tasks[i].CreatedAt.Before(m.tasks[j].CreatedAt)
		})
	case "topic":
		sort.SliceStable(m.tasks, func(i, j int) bool {
			ti, tj := topicSortKey(m.tasks[i]), topicSortKey(m.tasks[j])
			if ti != tj {
				if ti == "" { // untopiced tasks sort last
					return false
				}
				if tj == "" {
					return true
				}
				return ti < tj
			}
			return m.tasks[i].ID < m.tasks[j].ID
		})
	case "stage":
		// Order by position within the governing workflow; tasks with no workflow
		// sort after those that have one, then by id for stability.
		sort.SliceStable(m.tasks, func(i, j int) bool {
			ri, oki := m.stagePosition(m.tasks[i])
			rj, okj := m.stagePosition(m.tasks[j])
			if oki != okj {
				return oki // workflow-governed tasks first
			}
			if oki && ri != rj {
				return ri < rj
			}
			return m.tasks[i].ID < m.tasks[j].ID
		})
	default:
		sort.SliceStable(m.tasks, func(i, j int) bool {
			return m.tasks[i].ID < m.tasks[j].ID
		})
	}
	if m.sortReversed {
		for i, j := 0, len(m.tasks)-1; i < j; i, j = i+1, j-1 {
			m.tasks[i], m.tasks[j] = m.tasks[j], m.tasks[i]
		}
	}
}

// topicSortKey is the lowercased topic string a task sorts under (empty when it
// has no topics, so those fall to the end).
func topicSortKey(t storage.Task) string {
	if len(t.Topics) == 0 {
		return ""
	}
	return strings.ToLower(strings.Join(t.Topics, ","))
}

// stateRank orders tasks for the auto/state sorts: overdue first, then active,
// then pending, then done. Under a custom workflow the "active vs pending"
// distinction comes from the current stage's category.
func (m Model) stateRank(t storage.Task) int {
	if isDone(t) {
		return 3
	}
	if isOverdue(t) {
		return 0
	}
	if stages, ok := m.governingWorkflow(t); ok {
		switch stages[currentStageIndex(stages, t.Status)].Category {
		case storage.StagePending:
			return 2
		default: // active (done is handled by isDone above)
			return 1
		}
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
	due := t.Due.Time
	// Date-only dues (midnight, shown without a clock time) count as due
	// through the end of that day — matching the agenda, which only calls a
	// task overdue once its due day has passed.
	if due.Hour() == 0 && due.Minute() == 0 && due.Second() == 0 {
		due = due.AddDate(0, 0, 1)
	}
	return time.Now().After(due)
}

func (m *Model) processSortKey(key string) bool {
	// simple 2-key sequence: s + d/p/t (due/priority/created-time)
	if key == "" {
		return false
	}
	// Complete a pending sequence first, so the second key of `ss` reaches the
	// state case below rather than re-arming the `s` prefix.
	if m.sortBuf == "s" {
		switch key {
		case "d":
			m.applySortMode("due")
			m.pendingSort = false
			m.status = "Sorted by due date (" + m.sortDirLabel() + ")"
		case "p":
			m.applySortMode("priority")
			m.pendingSort = false
			m.status = "Sorted by priority (" + m.sortDirLabel() + ")"
		case "t":
			m.applySortMode("title")
			m.pendingSort = false
			m.status = "Sorted by title (" + m.sortDirLabel() + ")"
		case "c":
			m.applySortMode("created")
			m.pendingSort = false
			m.status = "Sorted by created time (" + m.sortDirLabel() + ")"
		case "o":
			m.applySortMode("topic")
			m.pendingSort = false
			m.status = "Sorted by topic (" + m.sortDirLabel() + ")"
		case "a":
			m.applySortMode("auto")
			m.pendingSort = false
			m.status = "Sorted by auto (" + m.sortDirLabel() + ")"
		case "s":
			m.applySortMode("state")
			m.pendingSort = false
			m.status = "Sorted by state (" + m.sortDirLabel() + ")"
		case "w":
			m.applySortMode("stage")
			m.pendingSort = false
			m.status = "Sorted by workflow stage (" + m.sortDirLabel() + ")"
		default:
			m.status = "Sort cancelled"
		}
		m.sortBuf = ""
		return true
	}
	if key == "s" {
		m.sortBuf = "s"
		m.status = "Sort: d (due), p (priority), t (title), c (created), o (topic), w (stage), a (auto), s (state) - repeat to reverse"
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
	if key == "," {
		m.navBuf = ","
		m.status = ", (press f for fuzzy search)"
		return true
	}
	if m.navBuf == "," {
		m.navBuf = ""
		if key == "f" {
			m.mode = modeSearch
			m.searchFuzzy = true
			m.input.SetValue(m.searchQuery)
			m.input.Placeholder = "Fuzzy search tasks"
			m.input.Focus()
			m.status = "Fuzzy search: type a query, Enter to apply, Esc to cancel"
			return true
		}
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

// governingWorkflow returns the workflow that governs a task's status — that of
// its primary topic — and whether one exists. Tasks with no primary topic, or
// whose primary topic has no custom workflow, fall back to legacy behavior.
func (m Model) governingWorkflow(t storage.Task) ([]storage.Stage, bool) {
	pt := strings.TrimSpace(t.PrimaryTopic)
	if pt == "" {
		return nil, false
	}
	stages := m.workflows[pt]
	if len(stages) == 0 {
		return nil, false
	}
	return stages, true
}

// stageIndex finds a status's position within an ordered workflow, or -1 when
// the status isn't one of the workflow's stage names (e.g. a legacy value).
func stageIndex(stages []storage.Stage, status string) int {
	status = strings.TrimSpace(status)
	for i, s := range stages {
		if strings.EqualFold(s.Name, status) {
			return i
		}
	}
	return -1
}

// currentStageIndex resolves a task's stage within its workflow, mapping an
// unknown/legacy status to the initial stage (index 0).
func currentStageIndex(stages []storage.Stage, status string) int {
	if idx := stageIndex(stages, status); idx >= 0 {
		return idx
	}
	return 0
}

// taskStatusLabel returns the display label for a task's status, honoring its
// governing workflow when one exists.
func (m Model) taskStatusLabel(t storage.Task) string {
	if stages, ok := m.governingWorkflow(t); ok {
		return stages[currentStageIndex(stages, t.Status)].Name
	}
	return taskStatusLabel(t)
}

// nextTaskStatus returns the status a task rotates to next, honoring its
// governing workflow (wrapping at the end) when one exists.
func (m Model) nextTaskStatus(t storage.Task) string {
	if stages, ok := m.governingWorkflow(t); ok {
		idx := stageIndex(stages, t.Status)
		if idx < 0 {
			return stages[0].Name
		}
		return stages[(idx+1)%len(stages)].Name
	}
	return nextTaskStatus(t)
}

// stagePosition returns a task's index within its governing workflow and whether
// one governs it. Used by the "stage" sort.
func (m Model) stagePosition(t storage.Task) (int, bool) {
	if stages, ok := m.governingWorkflow(t); ok {
		return currentStageIndex(stages, t.Status), true
	}
	return 0, false
}

// stageCategory returns the category ("pending"/"active"/"done") of a task's
// current stage. For legacy (no governing workflow) it derives the category
// from the well-known status names.
func (m Model) stageCategory(t storage.Task) string {
	if stages, ok := m.governingWorkflow(t); ok {
		return stages[currentStageIndex(stages, t.Status)].Category
	}
	switch strings.ToUpper(strings.TrimSpace(t.Status)) {
	case "DONE":
		return storage.StageDone
	case "IN-PROGRESS":
		return storage.StageActive
	default:
		return storage.StagePending
	}
}

// statusMeansDone reports whether a given status value marks the task complete
// under its governing workflow (a done-category stage), or legacy "DONE".
func (m Model) statusMeansDone(t storage.Task, status string) bool {
	if stages, ok := m.governingWorkflow(t); ok {
		return stages[currentStageIndex(stages, status)].Category == storage.StageDone
	}
	return strings.EqualFold(strings.TrimSpace(status), "DONE")
}

// firstTopic returns the first topic in a comma-separated topic field — the one
// that governs the task's status workflow.
func firstTopic(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(part); t != "" {
			return t
		}
	}
	return ""
}

// orderTopicsPrimaryFirst returns topics with the primary topic moved to the
// front, so the modal's Topic field round-trips which topic is primary.
func orderTopicsPrimaryFirst(topics []string, primary string) []string {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		return topics
	}
	found := false
	for _, t := range topics {
		if strings.EqualFold(strings.TrimSpace(t), primary) {
			found = true
			break
		}
	}
	if !found {
		return topics
	}
	out := make([]string, 0, len(topics))
	out = append(out, primary)
	for _, t := range topics {
		if !strings.EqualFold(strings.TrimSpace(t), primary) {
			out = append(out, t)
		}
	}
	return out
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

func dateCell(t sql.NullTime) string {
	if !t.Valid {
		return "-"
	}
	return t.Time.Format("2006-01-02")
}

// relativeDueCell renders a due date relative to today — "today", "tomorrow",
// "yesterday", "in Nd", or "Nd ago". An empty due renders "-". Days are counted
// on local calendar boundaries to match the absolute date cells.
func relativeDueCell(t sql.NullTime) string {
	if !t.Valid {
		return "-"
	}
	days := dayIndexUTC(t.Time, time.Now())
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	case days == -1:
		return "yesterday"
	case days > 0:
		return fmt.Sprintf("in %dd", days)
	default:
		return fmt.Sprintf("%dd ago", -days)
	}
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
	done    int
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
	items := m.applyQuickFilterToItems(m.defaultVisibleItems())
	if m.searchActive() {
		return filterItemsByQuery(items, m.searchQuery, m.searchFuzzy)
	}
	return items
}

func (m *Model) applyQuickFilter(cmd string) {
	filter := normalizeQuickFilter(cmd)
	m.filterDone = filter
	m.cursor = clampCursor(0, len(m.visibleItems()))
	if m.quickFilterActive() {
		m.status = fmt.Sprintf("Filter: %s", filter)
	} else {
		m.status = "Filter cleared"
	}
}

func normalizeQuickFilter(v string) string {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), ":")
	// "stage:<name>" filters by a custom workflow stage; keep the name intact.
	if rest, ok := strings.CutPrefix(v, "stage:"); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "all"
		}
		return "stage:" + rest
	}
	switch v {
	case "", "all", "clear", "reset":
		return "all"
	case "completed":
		return "done"
	case "progress":
		return "in-progress"
	default:
		return v
	}
}

func (m Model) quickFilterActive() bool {
	return normalizeQuickFilter(m.filterDone) != "all"
}

func (m Model) applyQuickFilterToItems(candidates []listItem) []listItem {
	filter := normalizeQuickFilter(m.filterDone)
	if filter == "all" {
		return candidates
	}
	items := make([]listItem, 0, len(candidates))
	for _, it := range candidates {
		if it.kind != itemTask || m.taskMatchesQuickFilter(it.task, filter) {
			items = append(items, it)
		}
	}
	return items
}

func (m Model) taskMatchesQuickFilter(t storage.Task, filter string) bool {
	norm := normalizeQuickFilter(filter)
	if name, ok := strings.CutPrefix(norm, "stage:"); ok {
		stages, hasWF := m.governingWorkflow(t)
		if !hasWF {
			return false
		}
		return strings.EqualFold(stages[currentStageIndex(stages, t.Status)].Name, name)
	}
	switch norm {
	case "overdue":
		return isOverdue(t)
	case "pending":
		return !isDone(t) && m.stageCategory(t) == storage.StagePending
	case "done":
		return isDone(t)
	case "in-progress":
		return !isDone(t) && m.stageCategory(t) == storage.StageActive
	case "today":
		return !isDone(t) && t.Due.Valid && normalizeDate(t.Due.Time).Equal(normalizeDate(time.Now()))
	case "week":
		if isDone(t) || !t.Due.Valid {
			return false
		}
		today := normalizeDate(time.Now())
		due := normalizeDate(t.Due.Time)
		return !due.Before(today) && due.Before(today.AddDate(0, 0, 7))
	default:
		return true
	}
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

func filterItemsByQuery(candidates []listItem, query string, fuzzy bool) []listItem {
	query = strings.TrimSpace(query)
	if query == "" {
		return candidates
	}
	q := strings.ToLower(query)
	items := make([]listItem, 0, len(candidates))
	for _, it := range candidates {
		if it.kind != itemTask {
			continue
		}
		if fuzzy {
			if taskMatchesFuzzyQuery(it.task, q) {
				items = append(items, it)
			}
		} else if taskMatchesQuery(it.task, q) {
			items = append(items, it)
		}
	}
	return items
}

func (m Model) searchActive() bool {
	return strings.TrimSpace(m.searchQuery) != ""
}

func taskMatchesFuzzyQuery(t storage.Task, query string) bool {
	if query == "" {
		return true
	}
	candidate := strings.Join(taskSearchFields(t), " ")
	for _, token := range strings.Fields(query) {
		if !fuzzyMatch(candidate, token) {
			return false
		}
	}
	return true
}

func fuzzyMatch(candidate, query string) bool {
	candidate = strings.ToLower(candidate)
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	j := 0
	qr := []rune(query)
	for _, r := range candidate {
		if r == qr[j] {
			j++
			if j == len(qr) {
				return true
			}
		}
	}
	return false
}

func taskMatchesQuery(t storage.Task, query string) bool {
	for _, field := range taskSearchFields(t) {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func taskSearchFields(t storage.Task) []string {
	fields := []string{
		t.Title,
		fmt.Sprintf("#%d", t.ID),
		fmt.Sprintf("id:%d", t.ID),
		taskStatusLabel(t),
		t.Status,
		strings.Join(t.Topics, " "),
		t.Tags,
		t.Assignee,
		t.Reporter,
		fmt.Sprintf("priority:%d", t.Priority),
		fmt.Sprintf("p%d", t.Priority),
		priorityLabel(t.Priority),
		t.Timezone,
		t.Notes,
	}
	if t.Recurring {
		fields = append(fields, "recurring", t.RecurrenceRule, fmt.Sprintf("interval:%d", t.RecurrenceInterval))
	}
	for _, nt := range []struct {
		name string
		val  sql.NullTime
	}{
		{"due", t.Due},
		{"start", t.Start},
		{"end", t.End},
		{"completed", t.CompletedAt},
	} {
		if nt.val.Valid {
			fields = append(fields, formatDateTime(nt.val), nt.name+":"+formatDateTime(nt.val), nt.name+":"+nt.val.Time.Format("2006-01-02"))
		}
	}
	if !t.CreatedAt.IsZero() {
		fields = append(fields, t.CreatedAt.Format("2006-01-02"), "created:"+t.CreatedAt.Format("2006-01-02"))
	}
	return fields
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
		done := isDone(t)
		for _, topic := range uniqueTopics(t.Topics) {
			stat := stats[topic]
			stat.total++
			if overdue {
				stat.overdue++
			}
			if done {
				stat.done++
			}
			stats[topic] = stat
		}
	}
	return stats
}

// stageCount pairs a workflow stage with how many tasks currently sit in it.
type stageCount struct {
	Stage storage.Stage
	Count int
}

// topicStageStats returns the per-stage task distribution for a topic's custom
// workflow. Only tasks whose primary topic is this topic are counted (those are
// the ones the workflow governs); an unknown/legacy status maps to the initial
// stage. Returns nil when the topic has no workflow.
func (m Model) topicStageStats(topic string) []stageCount {
	stages := m.workflows[strings.TrimSpace(topic)]
	if len(stages) == 0 {
		return nil
	}
	counts := make([]stageCount, len(stages))
	for i, s := range stages {
		counts[i] = stageCount{Stage: s}
	}
	for _, t := range m.tasks {
		if strings.TrimSpace(t.PrimaryTopic) != strings.TrimSpace(topic) {
			continue
		}
		counts[currentStageIndex(stages, t.Status)].Count++
	}
	return counts
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
