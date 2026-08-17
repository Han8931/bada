package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bada/internal/git"
)

// gitLogKey feeds a keypress to the git log view.
func gitLogKey(t *testing.T, m Model, k string) Model {
	t.Helper()
	res, _ := m.updateGitLogMode(k)
	next, ok := res.(Model)
	if !ok {
		t.Fatalf("updateGitLogMode(%q) did not return a Model", k)
	}
	return next
}

// makeRepo builds a repository with the given commit subjects, oldest first.
func makeRepo(t *testing.T, subjects ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test Author", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	for i, subject := range subjects {
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		run("add", "file.txt")
		run("commit", "-q", "-m", subject)
	}
	return dir
}

// linkedModel returns a model with one project pointed at a real repository,
// already sitting in the git log view with its commits loaded.
func linkedModel(t *testing.T, subjects ...string) (Model, string) {
	t.Helper()
	repo := makeRepo(t, subjects...)
	m := newTestModel(t)
	if err := m.store.CreateTopic("bada"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetTopicRepo("bada", repo); err != nil {
		t.Fatal(err)
	}
	m.refreshTopicMeta()
	m.height = 30
	m.width = 100

	res, cmd := m.enterGitLogView("bada", modeDashboard)
	m = res.(Model)
	if cmd == nil {
		t.Fatal("enterGitLogView did not schedule a load")
	}
	if m.mode != modeGitLog {
		t.Fatalf("mode = %v, want modeGitLog", m.mode)
	}
	// Run the load command the way the event loop would, then deliver its message.
	msg := cmd()
	loaded, ok := msg.(gitLogLoadedMsg)
	if !ok {
		t.Fatalf("load produced %T, want gitLogLoadedMsg", msg)
	}
	res, _ = m.handleGitLogLoaded(loaded)
	return res.(Model), repo
}

func TestGitLogLoadsCommitsNewestFirst(t *testing.T) {
	m, _ := linkedModel(t, "first commit", "second commit")

	if m.gitLog == nil {
		t.Fatal("gitLog state missing")
	}
	if m.gitLog.loading {
		t.Error("still marked loading after the result arrived")
	}
	if m.gitLog.err != nil {
		t.Fatalf("err = %v", m.gitLog.err)
	}
	if len(m.gitLog.commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(m.gitLog.commits))
	}
	if m.gitLog.commits[0].Subject != "second commit" {
		t.Errorf("first row = %q, want the newest commit", m.gitLog.commits[0].Subject)
	}
	body := m.gitLogBody(20)
	if !strings.Contains(body, "second commit") || !strings.Contains(body, "first commit") {
		t.Errorf("body missing commits:\n%s", body)
	}
	if !strings.Contains(m.renderGitLogView(), "bada") {
		t.Error("panel title missing the project name")
	}
}

func TestGitLogCursorMovesAndClamps(t *testing.T) {
	m, _ := linkedModel(t, "one", "two", "three")

	m = gitLogKey(t, m, "j")
	if m.gitLog.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.gitLog.cursor)
	}
	m = gitLogKey(t, m, "j")
	m = gitLogKey(t, m, "j") // past the end
	if m.gitLog.cursor != 2 {
		t.Errorf("cursor = %d, want it clamped to 2", m.gitLog.cursor)
	}
	m = gitLogKey(t, m, "k")
	m = gitLogKey(t, m, "k")
	m = gitLogKey(t, m, "k") // past the start
	if m.gitLog.cursor != 0 {
		t.Errorf("cursor = %d, want it clamped to 0", m.gitLog.cursor)
	}
}

func TestGitLogEnterShowsCommitDetail(t *testing.T) {
	m, _ := linkedModel(t, "first commit")

	res, cmd := m.updateGitLogMode("enter")
	m = res.(Model)
	if cmd == nil {
		t.Fatal("enter did not schedule a git show")
	}
	if m.gitLog.detailFor == "" {
		t.Fatal("detail pane did not open")
	}
	loaded, ok := cmd().(gitCommitLoadedMsg)
	if !ok {
		t.Fatal("show did not produce a gitCommitLoadedMsg")
	}
	res, _ = m.handleGitCommitLoaded(loaded)
	m = res.(Model)

	if m.gitLog.detailErr != nil {
		t.Fatalf("detailErr = %v", m.gitLog.detailErr)
	}
	body := strings.Join(m.gitCommitLines(), "\n")
	if !strings.Contains(body, "first commit") {
		t.Errorf("detail body missing the subject:\n%s", body)
	}
	if !strings.Contains(body, "file.txt") {
		t.Errorf("detail body missing the diffstat:\n%s", body)
	}

	// Esc closes the detail pane but stays in the log.
	m = gitLogKey(t, m, "esc")
	if m.gitLog.detailFor != "" {
		t.Error("detail pane still open after esc")
	}
	if m.mode != modeGitLog {
		t.Errorf("mode = %v, want to stay in the git log", m.mode)
	}
}

func TestGitLogEscReturnsToOpener(t *testing.T) {
	m, _ := linkedModel(t, "first commit")

	m = gitLogKey(t, m, "esc")
	if m.mode != modeDashboard {
		t.Errorf("mode = %v, want modeDashboard", m.mode)
	}
	if m.gitLog != nil {
		t.Error("gitLog state was not cleared")
	}
}

// A result for a project the user has navigated away from must be dropped, not
// painted over whatever they're looking at now.
func TestGitLogIgnoresStaleResults(t *testing.T) {
	m, _ := linkedModel(t, "first commit")

	stale := gitLogLoadedMsg{
		topic:   "some-other-project",
		repo:    "/elsewhere",
		commits: []git.Commit{{Hash: "deadbeef", Short: "deadbee", Subject: "wrong"}},
	}
	res, _ := m.handleGitLogLoaded(stale)
	m = res.(Model)

	for _, c := range m.gitLog.commits {
		if c.Subject == "wrong" {
			t.Fatal("stale result was applied")
		}
	}
}

func TestGitLogWithoutLinkedRepoExplainsHow(t *testing.T) {
	m := newTestModel(t)
	if err := m.store.CreateTopic("bada"); err != nil {
		t.Fatal(err)
	}
	m.refreshTopicMeta()

	res, cmd := m.enterGitLogView("bada", modeDashboard)
	m = res.(Model)
	if cmd != nil {
		t.Error("scheduled a load for a project with no repo")
	}
	if m.mode == modeGitLog {
		t.Error("opened the git log view without a repo")
	}
	if !strings.Contains(m.status, "no git repo") {
		t.Errorf("status = %q, want it to explain the missing repo", m.status)
	}
}

func TestGitLogCommandMatchesProjectCaseInsensitively(t *testing.T) {
	repo := makeRepo(t, "first commit")
	m := newTestModel(t)
	if err := m.store.CreateTopic("Bada"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetTopicRepo("Bada", repo); err != nil {
		t.Fatal(err)
	}
	m.refreshTopicMeta()

	res, cmd := m.enterGitLogView("bada", modeList)
	m = res.(Model)
	if cmd == nil {
		t.Fatalf("no load scheduled; status = %q", m.status)
	}
	if m.gitLog.topic != "Bada" {
		t.Errorf("topic = %q, want the registered spelling \"Bada\"", m.gitLog.topic)
	}
}

// An empty repository is a normal state, not an error to shout about.
func TestGitLogOnEmptyRepoSaysSo(t *testing.T) {
	m, _ := linkedModel(t)

	if m.gitLog.err != nil {
		t.Fatalf("err = %v, want none for an empty repo", m.gitLog.err)
	}
	if len(m.gitLog.commits) != 0 {
		t.Fatalf("got %d commits, want 0", len(m.gitLog.commits))
	}
	if !strings.Contains(m.gitLogBody(20), "No commits yet") {
		t.Errorf("body should say the repo is empty:\n%s", m.gitLogBody(20))
	}
}

// A repo that has moved or been deleted must surface an error, not a crash or a
// misleading "no commits".
func TestGitLogOnMissingRepoShowsError(t *testing.T) {
	m := newTestModel(t)
	if err := m.store.CreateTopic("bada"); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "moved-away")
	if err := m.store.SetTopicRepo("bada", gone); err != nil {
		t.Fatal(err)
	}
	m.refreshTopicMeta()
	m.height, m.width = 30, 100

	res, cmd := m.enterGitLogView("bada", modeDashboard)
	m = res.(Model)
	loaded := cmd().(gitLogLoadedMsg)
	res, _ = m.handleGitLogLoaded(loaded)
	m = res.(Model)

	if m.gitLog.err == nil {
		t.Fatal("expected an error for a missing repository")
	}
	body := m.gitLogBody(20)
	if !strings.Contains(body, "retry") {
		t.Errorf("error body should offer a way out:\n%s", body)
	}
}

func TestGitLogRefreshReloads(t *testing.T) {
	m, _ := linkedModel(t, "first commit")

	res, cmd := m.updateGitLogMode("r")
	m = res.(Model)
	if cmd == nil {
		t.Fatal("r did not schedule a reload")
	}
	if !m.gitLog.loading {
		t.Error("reload did not mark the view as loading")
	}
}

// "m" must not fire another request once the history is exhausted.
func TestGitLogMoreStopsAtTheEndOfHistory(t *testing.T) {
	m, _ := linkedModel(t, "one", "two")

	res, cmd := m.updateGitLogMode("m")
	m = res.(Model)
	if cmd != nil {
		t.Error("asked for more commits when the whole history was already loaded")
	}
	if !strings.Contains(m.status, "whole history") {
		t.Errorf("status = %q", m.status)
	}
	if m.gitLog.limit != gitLogPageSize {
		t.Errorf("limit = %d, want it unchanged", m.gitLog.limit)
	}
}

func TestGitLogMoreRaisesTheLimitWhenAPageIsFull(t *testing.T) {
	m, _ := linkedModel(t, "one", "two", "three")
	// Pretend the first page came back full, which is what a big repo looks like.
	m.gitLog.limit = 3

	res, cmd := m.updateGitLogMode("m")
	m = res.(Model)
	if cmd == nil {
		t.Fatal("m did not schedule a larger fetch")
	}
	if m.gitLog.limit != 3+gitLogPageSize {
		t.Errorf("limit = %d, want %d", m.gitLog.limit, 3+gitLogPageSize)
	}
}

func TestScrollToShowKeepsCursorInWindow(t *testing.T) {
	cases := []struct {
		name                  string
		index, scroll, height int
		want                  int
	}{
		{"already visible", 3, 2, 5, 2},
		{"above the window", 1, 4, 5, 1},
		{"below the window", 9, 0, 5, 5},
		{"zero height is a no-op", 9, 2, 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scrollToShow(tc.index, tc.scroll, tc.height); got != tc.want {
				t.Errorf("scrollToShow(%d, %d, %d) = %d, want %d", tc.index, tc.scroll, tc.height, got, tc.want)
			}
		})
	}
}

// The cursor must stay on screen when it walks past the bottom of the viewport.
func TestGitLogScrollFollowsCursor(t *testing.T) {
	subjects := make([]string, 40)
	for i := range subjects {
		subjects[i] = strings.Repeat("c", i+1)
	}
	m, _ := linkedModel(t, subjects...)
	m.height = 12 // a short terminal, so the list must scroll

	for i := 0; i < 30; i++ {
		m = gitLogKey(t, m, "j")
	}
	window := m.gitLogBodyMax()
	if m.gitLog.cursor < m.gitLog.scroll || m.gitLog.cursor >= m.gitLog.scroll+window {
		t.Errorf("cursor %d outside the visible window [%d, %d)", m.gitLog.cursor, m.gitLog.scroll, m.gitLog.scroll+window)
	}
}

// Rendering must survive a terminal too small to hold the panel furniture.
func TestGitLogRendersInATinyTerminal(t *testing.T) {
	m, _ := linkedModel(t, "one", "two")
	m.height, m.width = 4, 20
	if out := m.renderGitLogView(); out == "" {
		t.Error("render produced nothing")
	}
	m.height, m.width = 0, 0
	if out := m.renderGitLogView(); out == "" {
		t.Error("render produced nothing at zero size")
	}
}

func TestGitLogKeysAreNoOpsWithoutState(t *testing.T) {
	m := newTestModel(t)
	m.mode = modeGitLog
	m.gitLog = nil

	res, _ := m.updateGitLogMode("j")
	m = res.(Model)
	if m.mode != modeList {
		t.Errorf("mode = %v, want a fallback to the list", m.mode)
	}
}

func TestGitLogDateDropsYearForCurrentYear(t *testing.T) {
	now := time.Now()
	if got := gitLogDate(now); strings.Contains(got, now.Format("2006")) {
		t.Errorf("gitLogDate(%v) = %q, want the year omitted for this year", now, got)
	}
	old := now.AddDate(-3, 0, 0)
	if got := gitLogDate(old); !strings.Contains(got, old.Format("2006")) {
		t.Errorf("gitLogDate(%v) = %q, want the year included", old, got)
	}
}
