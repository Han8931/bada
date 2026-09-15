package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/config"
	"bada/internal/git"
	"bada/internal/llm"
	"bada/internal/report"
	"bada/internal/storage"
)

// reportCommitLimit bounds how much history a report ever reads, whatever the
// period: past a few hundred commits the digest stops being a summary.
const reportCommitLimit = 500

// projectReportState backs modeProjectReport — one generated report, or the
// effective system prompt when the user ran ":report prompt".
type projectReportState struct {
	seq        int64 // identifies the request this state is waiting on
	project    string
	period     string
	body       string
	model      string
	err        error
	loading    bool
	scroll     int
	savedPath  string
	promptOnly bool // showing the system prompt, not a generated report
	returnMode mode
	cancel     context.CancelFunc // aborts the in-flight request, if any
}

// projectReportMsg carries a finished report back to the event loop.
type projectReportMsg struct {
	seq     int64
	project string
	period  string
	body    string
	model   string
	err     error
}

// nextReportSeq numbers each ":report" request so a result can be matched to
// the request that started it, rather than to whatever view happens to share
// its project and period. bubbletea's Update runs on a single goroutine, so
// no locking is needed around it.
var nextReportSeq int64

// generateReportCmd reads the git history and calls the model off the event
// loop. Everything it needs is passed by value: the store's pool is limited to
// a single connection, so touching the database from here could deadlock the
// UI's own queries. prompt is the system prompt already resolved by the
// caller, so system_prompt_file is read once per request, not twice.
func generateReportCmd(ctx context.Context, cfg config.Config, in report.Input, repo, prompt string, seq int64) tea.Cmd {
	return func() tea.Msg {
		out := projectReportMsg{seq: seq, project: in.Project, period: in.Period}
		client, err := llm.New(cfg.LLM)
		if err != nil {
			out.err = err
			return out
		}
		out.model = client.Model()

		if repo != "" {
			commits, err := git.LogSince(ctx, repo, reportCommitLimit, in.Since)
			if err != nil {
				// A broken repo link is worth reporting on, not worth aborting
				// for: the task half of the report is still useful.
				in.RepoNote = fmt.Sprintf("The linked repository could not be read (%v).", err)
			} else {
				in.Commits = commits
			}
		}

		body, err := report.Generate(ctx, client, in, cfg.Report, prompt)
		out.body, out.err = body, err
		return out
	}
}

// enterProjectReportView backs ":report [project] [period]". An empty project
// falls back to the scoped one, then to the dashboard cursor — the same order
// :gitlog uses.
func (m Model) enterProjectReportView(arg string, returnMode mode) (tea.Model, tea.Cmd) {
	project, period, err := m.parseReportArgs(arg)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	days, err := config.ParsePeriodDays(period)
	if err != nil {
		m.status = "Report: " + err.Error()
		return m, nil
	}
	// Fail before opening the view, so a missing API key reads as one clear
	// line on the list rather than an error panel. The resolved prompt is
	// reused for the actual call below, so system_prompt_file is read once.
	if err := m.cfg.LLM.Validate(); err != nil {
		m.status = "Report needs an LLM: " + err.Error()
		return m, nil
	}
	prompt, err := report.SystemPrompt(m.cfg.Report)
	if err != nil {
		m.status = "Report: " + err.Error()
		return m, nil
	}

	// A report still in flight for the view being replaced is abandoned: its
	// result would otherwise arrive later and could overwrite this one.
	if m.projectReport != nil && m.projectReport.cancel != nil {
		m.projectReport.cancel()
	}

	in := m.reportInput(project, period, days)
	seq := nextReportSeq
	nextReportSeq++
	// The whole job — git plus the model — shares one deadline, so a slow
	// repository cannot eat the time budget and leave nothing for the call.
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.LLM.RequestTimeout()+gitTimeout)
	m.mode = modeProjectReport
	m.projectReport = &projectReportState{
		seq:        seq,
		project:    project,
		period:     period,
		loading:    true,
		returnMode: returnMode,
		cancel:     cancel,
	}
	m.status = fmt.Sprintf("Writing the %s report (%s)…", project, period)
	return m, generateReportCmd(ctx, m.cfg, in, strings.TrimSpace(m.topicMeta[project].RepoPath), prompt, seq)
}

// reportInput gathers everything the report is written from. Tasks come from
// the in-memory list, so this never touches the database.
func (m Model) reportInput(project, period string, days int) report.Input {
	now := time.Now()
	var tasks []storage.Task
	for _, t := range m.tasks {
		if taskHasTopic(t, project) {
			tasks = append(tasks, t)
		}
	}
	in := report.Input{
		Project: project,
		Meta:    m.topicMeta[project],
		Stages:  m.workflows[project],
		Period:  period,
		Days:    days,
		Now:     now,
		Since:   report.Since(now, days),
		Tasks:   tasks,
	}
	if strings.TrimSpace(in.Meta.RepoPath) == "" {
		in.RepoNote = "No git repository is linked to this project."
	}
	return in
}

// parseReportArgs splits ":report [project] [period]". Either may be omitted:
// a lone argument is read as a period when it looks like one, else a project.
func (m Model) parseReportArgs(arg string) (project, period string, err error) {
	fields := strings.Fields(strings.TrimSpace(arg))
	period = m.cfg.Report.ResolvedPeriod()

	// A trailing period argument is only a period if it parses as one;
	// otherwise it is part of the project name ("Q3 planning"). When it is
	// the *only* argument and also names an existing project ("week", "3d"),
	// the project wins — otherwise that project could never be targeted by
	// name.
	if n := len(fields); n > 0 {
		last := fields[n-1]
		if _, perr := config.ParsePeriodDays(last); perr == nil {
			if _, isProject := m.topicNamed(last); n > 1 || !isProject {
				period = last
				fields = fields[:n-1]
			}
		}
	}
	project = strings.Join(fields, " ")
	if project == "" {
		project = m.scopedTopicName()
	}
	if project == "" {
		if t, ok := m.dashboardCurrentTopic(); ok {
			project = t
		}
	}
	if project == "" {
		return "", "", fmt.Errorf("no project selected — try :report <project>, or scope one from :projects")
	}
	if t, ok := m.topicNamed(project); ok {
		return t, period, nil
	}
	return "", "", fmt.Errorf("unknown project %q — see :projects", project)
}

// topicNamed matches a project name case-insensitively (so ":report bada"
// finds "Bada") and returns its canonical capitalization.
func (m Model) topicNamed(name string) (string, bool) {
	for _, t := range m.sortedTopics() {
		if strings.EqualFold(t, name) {
			return t, true
		}
	}
	return "", false
}

// showReportPrompt opens the system prompt actually in force, so it can be
// read, copied into the config, and edited.
func (m Model) showReportPrompt(returnMode mode) (tea.Model, tea.Cmd) {
	prompt, err := report.SystemPrompt(m.cfg.Report)
	if err != nil {
		m.status = "Report: " + err.Error()
		return m, nil
	}
	source := "built-in default"
	switch {
	case strings.TrimSpace(m.cfg.Report.SystemPromptFile) != "":
		source = "[report].system_prompt_file = " + strings.TrimSpace(m.cfg.Report.SystemPromptFile)
	case strings.TrimSpace(m.cfg.Report.SystemPrompt) != "":
		source = "[report].system_prompt"
	}
	if strings.TrimSpace(m.cfg.Report.ExtraInstructions) != "" {
		source += " + [report].extra_instructions"
	}
	m.mode = modeProjectReport
	m.projectReport = &projectReportState{
		project:    "system prompt",
		period:     source,
		body:       prompt,
		promptOnly: true,
		returnMode: returnMode,
	}
	m.status = "Effective report prompt (" + source + ")"
	return m, nil
}

func (m Model) handleProjectReport(msg projectReportMsg) (tea.Model, tea.Cmd) {
	r := m.projectReport
	// A result for a request the user has since navigated away from (or
	// replaced with a new one for the same project and period) is stale.
	if r == nil || r.seq != msg.seq {
		return m, nil
	}
	r.loading = false
	r.err = msg.err
	r.model = msg.model
	r.scroll = 0
	if msg.err != nil {
		m.status = "Report failed — " + collapseWhitespace(msg.err.Error())
		return m, nil
	}
	r.body = msg.body
	m.status = fmt.Sprintf("%s report ready (%s, %s) — s saves it", r.project, r.period, r.model)
	return m, nil
}

func (m Model) updateProjectReportMode(key string) (tea.Model, tea.Cmd) {
	r := m.projectReport
	if r == nil {
		m.mode = modeList
		return m, nil
	}
	if m.processScrollKey(key, m.projectReportMaxScroll(), &r.scroll) {
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		if r.cancel != nil {
			r.cancel()
		}
		m.mode = r.returnMode
		m.projectReport = nil
		m.status = "Report closed"
		return m, nil
	case m.cfg.Keys.Up, "up":
		if r.scroll > 0 {
			r.scroll--
		}
	case m.cfg.Keys.Down, "down":
		r.scroll = clampInt(r.scroll+1, 0, m.projectReportMaxScroll())
	case "s":
		return m.saveProjectReport()
	case "r":
		if r.promptOnly || r.loading {
			return m, nil
		}
		project, period := r.project, r.period
		return m.enterProjectReportView(project+" "+period, r.returnMode)
	}
	return m, nil
}

// saveProjectReport writes the report to disk. There is nothing to save while
// one is still being written, and the prompt view is not a report.
func (m Model) saveProjectReport() (tea.Model, tea.Cmd) {
	r := m.projectReport
	if r.promptOnly {
		m.status = "Nothing to save — this is the system prompt, not a report"
		return m, nil
	}
	if r.loading || strings.TrimSpace(r.body) == "" || r.err != nil {
		m.status = "Nothing to save yet"
		return m, nil
	}
	path, err := report.Save(m.cfg.Report.ResolvedDir(), r.project, r.period, r.model, r.body, time.Now())
	if err != nil {
		m.status = "Could not save the report: " + collapseWhitespace(err.Error())
		return m, nil
	}
	r.savedPath = path
	m.status = "Saved " + path
	return m, nil
}

// --- rendering ---------------------------------------------------------------

func (m Model) projectReportPanelTitle() string {
	r := m.projectReport
	if r == nil {
		return "bada ∙ Report"
	}
	if r.promptOnly {
		return "bada ∙ Report prompt"
	}
	return fmt.Sprintf("bada ∙ Report — %s (%s)", r.project, r.period)
}

func (m Model) projectReportFooter() string {
	r := m.projectReport
	hints := []keyHint{{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "scroll"}}
	if r != nil && !r.promptOnly && !r.loading && r.err == nil {
		hints = append(hints, keyHint{"s", "save .md"}, keyHint{"r", "regenerate"})
	}
	return m.hintBar(append(hints, keyHint{m.cfg.Keys.Cancel, "close"}))
}

// reportContent is the full text of the view before scrolling.
func (m Model) projectReportContent() string {
	r := m.projectReport
	if r == nil {
		return ""
	}
	switch {
	case r.loading:
		return "Reading the git history and writing the report…\n\nThis waits on the model, so it can take a few seconds.\nPress " + m.cfg.Keys.Cancel + " to close; the result is discarded."
	case r.err != nil:
		return "The report could not be written.\n\n" + wrapText(r.err.Error(), m.panelInnerWidth()-2) +
			"\n\nCheck the connection with :llm test."
	}
	body := r.body
	if r.savedPath != "" {
		body += "\n\n---\nSaved to " + r.savedPath
	}
	return body
}

func (m Model) projectReportLines() []string {
	content := strings.TrimRight(m.projectReportContent(), "\n")
	width := m.panelInnerWidth() - 2
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		lines = append(lines, strings.Split(wrapText(line, width), "\n")...)
	}
	return lines
}

func (m Model) projectReportBodyMax() int {
	if m.height <= 0 {
		return 0
	}
	bodyMax := m.height - 1 - 2 - countLines(m.projectReportFooter()) // 2 = panel borders
	if bodyMax < 1 {
		bodyMax = 1
	}
	return bodyMax
}

func (m Model) projectReportMaxScroll() int {
	bodyMax := m.projectReportBodyMax()
	if bodyMax <= 0 {
		return 0
	}
	if lines := m.projectReportLines(); len(lines) > bodyMax {
		return len(lines) - bodyMax
	}
	return 0
}

func (m Model) renderProjectReportView() string {
	footer := m.projectReportFooter()
	lines := m.projectReportLines()
	bodyMax := m.projectReportBodyMax()
	body := strings.Join(lines, "\n")
	if bodyMax > 0 && len(lines) > bodyMax {
		scroll := clampInt(m.projectReport.scroll, 0, len(lines)-bodyMax)
		body = strings.Join(lines[scroll:scroll+bodyMax], "\n")
	}
	return m.panel(m.projectReportPanelTitle(), body) + "\n" + footer
}
