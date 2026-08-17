package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"bada/internal/git"
	"bada/internal/storage"
)

// gitTimeout bounds every git invocation so a slow, huge, or network-backed
// repository can't wedge the UI.
const gitTimeout = 5 * time.Second

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
		m.status = "Projects closed"
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
	case "n":
		return m.startDashboardEdit("new")
	case "g":
		return m.startDashboardEdit("repo")
	case "L":
		if topic, ok := m.dashboardCurrentTopic(); ok {
			return m.enterGitLogView(topic, modeDashboard)
		}
		return m, nil
	case m.cfg.Keys.Delete, "D":
		if topic, ok := m.dashboardCurrentTopic(); ok {
			m.pendingTopic = topic
			m.confirmTopic = true
			m.status = fmt.Sprintf("Delete project %q? Its tasks are kept but untagged. (y/n)", topic)
		}
		return m, nil
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
	// Creating a project is the one edit that doesn't need a row under the
	// cursor — it's how the first project gets made.
	if field == "new" {
		m.dashEditing = field
		m.input.SetValue("")
		m.input.Focus()
		m.status = "New project name (enter to create, esc to cancel)"
		return m, nil
	}
	topic, ok := m.dashboardCurrentTopic()
	if !ok {
		return m, nil
	}
	meta := m.topicMeta[topic]
	m.dashEditing = field
	m.dashComplete = nil
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
	case "repo":
		m.input.SetValue(meta.RepoPath)
		m.status = "Git repo path for " + topic + " (tab to complete, blank to clear)"
	}
	m.input.Focus()
	return m, nil
}

func (m Model) updateDashboardEditing(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "tab":
		// Only the repo prompt names something on disk, so it's the only field
		// with anything to complete against.
		if m.dashEditing != "repo" {
			return m, nil
		}
		completed, matches := git.CompleteDir(m.input.Value())
		m.input.SetValue(completed)
		m.input.CursorEnd()
		m.dashComplete = matches
		switch {
		case len(matches) == 0:
			m.status = "No matching directory"
		case len(matches) == 1:
			m.status = "tab again to go deeper · ⏎ to link"
		default:
			m.status = fmt.Sprintf("%d matches · tab to extend · ⏎ to link", len(matches))
		}
		return m, nil
	case "esc":
		m.dashEditing = ""
		m.input.SetValue("")
		m.input.Blur()
		m.status = "Cancelled"
		return m, nil
	case "enter":
		val := strings.TrimSpace(m.input.Value())
		switch m.dashEditing {
		case "new":
			return m.finishCreateProject(val)
		case "repo":
			return m.finishSetProjectRepo(val)
		}
		topic, ok := m.dashboardCurrentTopic()
		if !ok {
			m.dashEditing = ""
			return m, nil
		}
		meta := m.topicMeta[topic]
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
		// Any edit invalidates the listed candidates.
		m.dashComplete = nil
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

// finishCreateProject registers a project by name and parks the cursor on it.
func (m Model) finishCreateProject(name string) (tea.Model, tea.Cmd) {
	if name == "" {
		m.status = "Project name cannot be empty"
		return m, nil
	}
	// Topics are comma-separated on tasks, so a name containing one could never
	// be selected again as a single project.
	if strings.Contains(name, ",") {
		m.status = "Project name cannot contain a comma"
		return m, nil
	}
	existing := false
	for _, t := range m.sortedTopics() {
		if strings.EqualFold(t, name) {
			existing = true
			name = t
			break
		}
	}
	if !existing {
		if err := m.store.CreateTopic(name); err != nil {
			m.status = fmt.Sprintf("create failed: %v", err)
			return m, nil
		}
	}
	m.refreshTopicMeta()
	m.finishDashboardEdit()
	for i, t := range m.sortedTopics() {
		if t == name {
			m.dashboardCursor = i
			break
		}
	}
	if existing {
		m.status = "Project " + name + " already exists"
	} else {
		m.status = "Created project " + name
	}
	return m, nil
}

// finishSetProjectRepo points the selected project at a local git repository.
// The path is resolved to the repo's top level so later log calls don't depend
// on which subdirectory was typed.
func (m Model) finishSetProjectRepo(path string) (tea.Model, tea.Cmd) {
	topic, ok := m.dashboardCurrentTopic()
	if !ok {
		m.finishDashboardEdit()
		return m, nil
	}
	resolved := ""
	if path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		top, err := git.Resolve(ctx, path)
		if err != nil {
			// Keep the prompt open so the path can be corrected in place.
			m.status = fmt.Sprintf("%v", err)
			return m, nil
		}
		resolved = top
	}
	if err := m.store.SetTopicRepo(topic, resolved); err != nil {
		m.status = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.refreshTopicMeta()
	m.finishDashboardEdit()
	if resolved == "" {
		m.status = "Cleared repo for " + topic
	} else {
		m.status = topic + " → " + resolved
	}
	return m, nil
}

// runProjectCommand backs ":project". With no argument it opens the overview;
// "new <name>" (or "add <name>") registers a project that has no tasks yet.
func (m Model) runProjectCommand(arg string) (tea.Model, tea.Cmd) {
	if arg == "" {
		return m.enterDashboardView()
	}
	verb, rest, _ := strings.Cut(arg, " ")
	switch strings.ToLower(verb) {
	case "new", "add":
		name := strings.TrimSpace(rest)
		if name == "" {
			m.status = "Usage: :project new <name>"
			return m, nil
		}
		dash, _ := m.enterDashboardView()
		next, ok := dash.(Model)
		if !ok {
			return dash, nil
		}
		return next.finishCreateProject(name)
	default:
		m.status = fmt.Sprintf("unknown :project subcommand %q (try: new)", verb)
		return m, nil
	}
}

// finishDashboardEdit closes the inline metadata prompt.
func (m *Model) finishDashboardEdit() {
	m.dashEditing = ""
	m.dashComplete = nil
	m.input.SetValue("")
	m.input.Blur()
}

// refreshTopicMeta reloads project metadata after a write. A failure here only
// means the view is momentarily stale, so it's deliberately not surfaced.
func (m *Model) refreshTopicMeta() {
	if tm, err := m.store.AllTopicMeta(); err == nil {
		m.topicMeta = tm
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
	// Two rows: the full set no longer fits on one line at a typical width, and
	// hintBar clips rather than wraps.
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "move"},
		{"enter", "scope"},
		{"n", "new"},
		{"w", "workflow"},
		{"L", "git log"},
		{m.cfg.Keys.Cancel, "close"},
	}) + "\n" + m.hintBar([]keyHint{
		{"e", "desc"},
		{"t", "target"},
		{"g", "repo"},
		{"a", "archive"},
		{"D", "delete"},
	})
}

func (m Model) dashboardContent() string {
	topics := m.sortedTopics()
	if len(topics) == 0 {
		body := m.styles.Muted.Render("  No projects yet. Press n to create one, or add a topic to a task.")
		if m.dashEditing != "" {
			body += "\n\n" + m.dashboardPrompt()
		}
		return body
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
	if m.dashEditing != "" {
		b.WriteString("\n")
		b.WriteString(m.dashboardPrompt())
	}
	return b.String()
}

// dashboardPrompt renders the inline editor for whichever project field is
// being typed, so the text being entered is visible in the panel itself.
func (m Model) dashboardPrompt() string {
	var label string
	switch m.dashEditing {
	case "new":
		label = "New project"
	case "desc":
		label = "Description"
	case "target":
		label = "Target date"
	case "repo":
		label = "Git repo"
	default:
		return ""
	}
	line := "  " + m.styles.Accent.Render(label+": ") + m.input.View()
	if len(m.dashComplete) < 2 {
		// A lone candidate is already written into the field; listing it again
		// would just be noise.
		return line
	}
	return line + "\n" + m.dashCompletionList()
}

// dashCompletionList renders the directories Tab matched, wrapped to the panel
// width. Directories that are already git repositories are marked, so the one
// worth linking stands out from its neighbours.
func (m Model) dashCompletionList() string {
	const maxShown = 24
	shown := m.dashComplete
	hidden := 0
	if len(shown) > maxShown {
		hidden = len(shown) - maxShown
		shown = shown[:maxShown]
	}
	// Leave room for the two-space indent and the panel's own frame.
	width := m.width - 6
	if width < 20 {
		width = 20
	}
	var b strings.Builder
	b.WriteString("  ")
	col := 0
	for i, d := range shown {
		name := d.Name + "/"
		cell := name
		if d.IsRepo {
			cell = name + " ●"
		}
		if col > 0 && col+runewidth.StringWidth(cell)+2 > width {
			b.WriteString("\n  ")
			col = 0
		} else if i > 0 {
			b.WriteString("  ")
			col += 2
		}
		if d.IsRepo {
			b.WriteString(m.styles.Accent.Render(cell))
		} else {
			b.WriteString(m.styles.Muted.Render(cell))
		}
		col += runewidth.StringWidth(cell)
	}
	if hidden > 0 {
		b.WriteString(m.styles.Muted.Render(fmt.Sprintf("  +%d more", hidden)))
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
	repo := strings.TrimSpace(meta.RepoPath)
	if repo == "" {
		repo = m.styles.Muted.Render("(none — press g to link a git repo)")
	} else {
		repo = shortenPath(repo)
	}
	b.WriteString("  " + m.styles.Muted.Render("Repo: ") + repo + "\n")
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

// shortenPath re-abbreviates a path under the home directory back to "~/…",
// which is how the user most likely typed it.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~/" + rest
	}
	return path
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
