package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/git"
)

// gitLogPageSize is how many commits are fetched at a time. Enough to fill any
// terminal, small enough that opening the view on a huge repository is instant;
// "m" loads another page.
const gitLogPageSize = 100

// gitLogState backs modeGitLog: the commit list for one project's repository,
// plus the optional `git show` pane for the selected commit.
type gitLogState struct {
	topic      string
	repo       string
	commits    []git.Commit
	cursor     int
	scroll     int
	limit      int
	loading    bool
	err        error
	returnMode mode

	// Commit detail. Non-empty detailFor means the detail pane is showing.
	detailFor    string
	detail       string
	detailScroll int
	detailErr    error
}

// gitLogLoadedMsg carries a finished log read back to the event loop.
type gitLogLoadedMsg struct {
	topic   string
	repo    string
	commits []git.Commit
	err     error
}

// gitCommitLoadedMsg carries a finished `git show` back to the event loop.
type gitCommitLoadedMsg struct {
	topic string
	hash  string
	text  string
	err   error
}

// loadGitLogCmd reads a repository's log off the event loop, so a slow repo
// leaves the UI responsive instead of freezing it mid-keystroke.
func loadGitLogCmd(topic, repo string, limit int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		commits, err := git.Log(ctx, repo, limit)
		return gitLogLoadedMsg{topic: topic, repo: repo, commits: commits, err: err}
	}
}

func loadGitCommitCmd(topic, repo, hash string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		text, err := git.Show(ctx, repo, hash)
		return gitCommitLoadedMsg{topic: topic, hash: hash, text: text, err: err}
	}
}

// enterGitLogView opens the commit log for a project. An empty topic falls back
// to the scoped project, then to the one under the dashboard cursor.
func (m Model) enterGitLogView(topic string, returnMode mode) (tea.Model, tea.Cmd) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		topic = m.scopedTopicName()
	}
	if topic == "" {
		if t, ok := m.dashboardCurrentTopic(); ok {
			topic = t
		}
	}
	if topic == "" {
		m.status = "No project selected — open :projects and pick one"
		return m, nil
	}
	// Match a project case-insensitively so ":gitlog bada" finds "Bada".
	resolved := topic
	for _, t := range m.sortedTopics() {
		if strings.EqualFold(t, topic) {
			resolved = t
			break
		}
	}
	repo := strings.TrimSpace(m.topicMeta[resolved].RepoPath)
	if repo == "" {
		m.status = fmt.Sprintf("%s has no git repo — open :projects and press g to link one", resolved)
		return m, nil
	}
	m.mode = modeGitLog
	m.gitLog = &gitLogState{
		topic:      resolved,
		repo:       repo,
		limit:      gitLogPageSize,
		loading:    true,
		returnMode: returnMode,
	}
	m.status = "Loading " + resolved + " history…"
	return m, loadGitLogCmd(resolved, repo, gitLogPageSize)
}

func (m Model) handleGitLogLoaded(msg gitLogLoadedMsg) (tea.Model, tea.Cmd) {
	g := m.gitLog
	// A result for a project the user has since navigated away from is stale.
	if g == nil || g.topic != msg.topic || g.repo != msg.repo {
		return m, nil
	}
	g.loading = false
	g.err = msg.err
	if msg.err != nil {
		m.status = fmt.Sprintf("git log failed: %v", msg.err)
		return m, nil
	}
	g.commits = msg.commits
	g.cursor = clampCursor(g.cursor, len(g.commits))
	g.scroll = 0
	switch {
	case len(g.commits) == 0:
		m.status = g.topic + " — no commits yet"
	case len(g.commits) == 1:
		m.status = g.topic + " — 1 commit"
	default:
		m.status = fmt.Sprintf("%s — %d commits", g.topic, len(g.commits))
	}
	return m, nil
}

func (m Model) handleGitCommitLoaded(msg gitCommitLoadedMsg) (tea.Model, tea.Cmd) {
	g := m.gitLog
	if g == nil || g.topic != msg.topic || g.detailFor != msg.hash {
		return m, nil
	}
	g.detailErr = msg.err
	g.detail = msg.text
	g.detailScroll = 0
	if msg.err != nil {
		m.status = fmt.Sprintf("git show failed: %v", msg.err)
	}
	return m, nil
}

func (m Model) updateGitLogMode(key string) (tea.Model, tea.Cmd) {
	g := m.gitLog
	if g == nil {
		m.mode = modeList
		return m, nil
	}
	if g.detailFor != "" {
		return m.updateGitCommitDetail(key)
	}
	if m.processScrollKey(key, maxInt(len(g.commits)-1, 0), &g.cursor) {
		g.scroll = scrollToShow(g.cursor, g.scroll, m.gitLogBodyMax())
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		ret := g.returnMode
		m.gitLog = nil
		m.mode = ret
		m.status = "Git log closed"
		return m, nil
	case m.cfg.Keys.Up, "up", "k":
		if g.cursor > 0 {
			g.cursor--
		}
	case m.cfg.Keys.Down, "down", "j":
		g.cursor = clampCursor(g.cursor+1, len(g.commits))
	case m.cfg.Keys.Confirm, "enter":
		return m.openGitCommitDetail()
	case "r":
		if g.loading {
			return m, nil
		}
		g.loading = true
		m.status = "Reloading " + g.topic + " history…"
		return m, loadGitLogCmd(g.topic, g.repo, g.limit)
	case "m":
		if g.loading {
			return m, nil
		}
		// Nothing more to fetch once the log came back shorter than asked for.
		if len(g.commits) < g.limit {
			m.status = "That's the whole history"
			return m, nil
		}
		g.limit += gitLogPageSize
		g.loading = true
		m.status = fmt.Sprintf("Loading up to %d commits…", g.limit)
		return m, loadGitLogCmd(g.topic, g.repo, g.limit)
	default:
		return m, nil
	}
	g.scroll = scrollToShow(g.cursor, g.scroll, m.gitLogBodyMax())
	return m, nil
}

func (m Model) openGitCommitDetail() (tea.Model, tea.Cmd) {
	g := m.gitLog
	if len(g.commits) == 0 || g.cursor < 0 || g.cursor >= len(g.commits) {
		return m, nil
	}
	c := g.commits[g.cursor]
	g.detailFor = c.Hash
	g.detail = ""
	g.detailErr = nil
	g.detailScroll = 0
	m.status = "Loading " + c.Short + "…"
	return m, loadGitCommitCmd(g.topic, g.repo, c.Hash)
}

func (m Model) updateGitCommitDetail(key string) (tea.Model, tea.Cmd) {
	g := m.gitLog
	maxScroll := m.gitCommitMaxScroll()
	if m.processScrollKey(key, maxScroll, &g.detailScroll) {
		return m, nil
	}
	switch key {
	case "esc", m.cfg.Keys.Quit, "q":
		g.detailFor = ""
		g.detail = ""
		g.detailErr = nil
		g.detailScroll = 0
		m.status = g.topic + " history"
		return m, nil
	case m.cfg.Keys.Up, "up", "k":
		if g.detailScroll > 0 {
			g.detailScroll--
		}
	case m.cfg.Keys.Down, "down", "j":
		g.detailScroll = clampInt(g.detailScroll+1, 0, maxScroll)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (m Model) renderGitLogView() string {
	g := m.gitLog
	if g == nil {
		return ""
	}
	title := "bada ∙ Git ∙ " + g.topic
	if g.detailFor != "" {
		title += " ∙ commit"
	}
	footer := m.gitLogFooter()
	bodyMax := 0
	if m.height > 0 {
		bodyMax = m.height - 1 - 2 - countLines(footer) // 2 = panel borders
		if bodyMax < 1 {
			bodyMax = 1
		}
	}
	var body string
	if g.detailFor != "" {
		body = m.gitCommitBody(bodyMax)
	} else {
		body = m.gitLogBody(bodyMax)
	}
	return m.panel(title, body) + "\n" + footer
}

func (m Model) gitLogFooter() string {
	g := m.gitLog
	if g != nil && g.detailFor != "" {
		return m.hintBar([]keyHint{
			{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "scroll"},
			{m.cfg.Keys.Cancel, "back"},
		})
	}
	return m.hintBar([]keyHint{
		{m.cfg.Keys.Up + "/" + m.cfg.Keys.Down, "move"},
		{"enter", "show"},
		{"r", "refresh"},
		{"m", "more"},
		{m.cfg.Keys.Cancel, "close"},
	})
}

// gitLogBodyMax is how many commit rows fit in the panel, used to keep the
// cursor on screen.
func (m Model) gitLogBodyMax() int {
	if m.height <= 0 {
		return 0
	}
	// The header line and its blank spacer sit above the rows.
	bodyMax := m.height - 1 - 2 - countLines(m.gitLogFooter()) - 2
	if bodyMax < 1 {
		return 1
	}
	return bodyMax
}

func (m Model) gitLogBody(maxLines int) string {
	g := m.gitLog
	var b strings.Builder
	b.WriteString(m.styles.Muted.Render("  " + shortenPath(g.repo)))
	b.WriteString("\n\n")
	switch {
	case g.err != nil:
		b.WriteString("  " + m.styles.Danger.Render(g.err.Error()) + "\n")
		b.WriteString("  " + m.styles.Muted.Render("Press r to retry, or relink the repo with g in :projects") + "\n")
		return b.String()
	case g.loading && len(g.commits) == 0:
		b.WriteString("  " + m.styles.Muted.Render("Loading history…") + "\n")
		return b.String()
	case len(g.commits) == 0:
		b.WriteString("  " + m.styles.Muted.Render("No commits yet in this repository.") + "\n")
		return b.String()
	}

	rows := maxLines
	if rows <= 0 {
		rows = len(g.commits)
	}
	scroll := clampInt(g.scroll, 0, maxInt(len(g.commits)-rows, 0))
	end := minInt(scroll+rows, len(g.commits))
	// Width left for the subject after the sha, date, and author columns.
	subjectWidth := m.panelInnerWidth() - 2 - 8 - 12 - 16 - 3
	if subjectWidth < 10 {
		subjectWidth = 10
	}
	for i := scroll; i < end; i++ {
		c := g.commits[i]
		cursor := "  "
		subjectStyle := m.styles.Title
		if i == g.cursor {
			cursor = m.styles.Accent.Render("▌ ")
			subjectStyle = m.styles.Selection
		}
		// Author and subject are width-padded, not byte-padded: names and commit
		// messages can hold wide CJK runes, which %-16s would misalign.
		line := cursor +
			m.styles.Warning.Render(fmt.Sprintf("%-8s", c.Short)) + " " +
			m.styles.Muted.Render(fmt.Sprintf("%-12s", gitLogDate(c.When))) + " " +
			m.styles.Muted.Render(padRightWidth(truncateTextWidth(c.Author, 16), 16)) + " " +
			subjectStyle.Render(truncateTextWidth(c.Subject, subjectWidth))
		b.WriteString(line)
		b.WriteString("\n")
	}
	if g.loading {
		b.WriteString("  " + m.styles.Muted.Render("Loading…") + "\n")
	} else if len(g.commits) > end || scroll > 0 {
		b.WriteString("  " + m.styles.Muted.Render(fmt.Sprintf("%d–%d of %d  ·  m loads more", scroll+1, end, len(g.commits))) + "\n")
	}
	return b.String()
}

func (m Model) gitCommitLines() []string {
	g := m.gitLog
	if g.detailErr != nil {
		return []string{"  " + m.styles.Danger.Render(g.detailErr.Error())}
	}
	if g.detail == "" {
		return []string{"  " + m.styles.Muted.Render("Loading commit…")}
	}
	raw := strings.Split(strings.TrimRight(g.detail, "\n"), "\n")
	inner := m.panelInnerWidth() - 2
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		// git's own output is already wrapped for a wide terminal; clip rather
		// than re-wrap so diffstat columns stay lined up.
		out = append(out, "  "+truncateTextWidth(line, inner))
	}
	return out
}

func (m Model) gitCommitMaxScroll() int {
	if m.height <= 0 || m.gitLog == nil {
		return 0
	}
	bodyMax := m.height - 1 - 2 - countLines(m.gitLogFooter())
	if bodyMax <= 0 {
		return 0
	}
	lines := m.gitCommitLines()
	if len(lines) <= bodyMax {
		return 0
	}
	return len(lines) - bodyMax
}

func (m Model) gitCommitBody(maxLines int) string {
	lines := m.gitCommitLines()
	if maxLines <= 0 {
		return strings.Join(lines, "\n")
	}
	maxScroll := maxInt(len(lines)-maxLines, 0)
	scroll := clampInt(m.gitLog.detailScroll, 0, maxScroll)
	end := minInt(scroll+maxLines, len(lines))
	return strings.Join(lines[scroll:end], "\n")
}

// gitLogDate renders a commit's instant as a local date, dropping the year for
// commits from the current year to keep the column narrow.
func gitLogDate(when time.Time) string {
	if when.IsZero() {
		return ""
	}
	local := when.Local()
	if local.Year() == time.Now().Year() {
		return local.Format("Jan 02 15:04")
	}
	return local.Format("2006-01-02")
}

// scrollToShow nudges the viewport so index stays visible in a window of the
// given height.
func scrollToShow(index, scroll, height int) int {
	if height <= 0 {
		return scroll
	}
	if index < scroll {
		return index
	}
	if index >= scroll+height {
		return index - height + 1
	}
	return scroll
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
