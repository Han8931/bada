package ui

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/config"
	"bada/internal/report"
	"bada/internal/storage"
)

// reportModel is a model with one project, a linked repo, and a working LLM
// config — the state ":report" needs before it will do anything.
func reportModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t)
	m.cfg.LLM = config.LLM{Provider: "ollama", Model: "llama3.2"}
	m.topicMeta["bada"] = storage.TopicMeta{Topic: "bada", Description: "TUI todo app"}
	m.topicMeta["Meari"] = storage.TopicMeta{Topic: "Meari"}
	m.tasks = []storage.Task{
		{ID: 1, Title: "Ship it", Status: "PENDING", Topics: []string{"bada"}, PrimaryTopic: "bada"},
		{ID: 2, Title: "Other project work", Status: "PENDING", Topics: []string{"Meari"}, PrimaryTopic: "Meari"},
	}
	return m
}

func TestParseReportArgs(t *testing.T) {
	m := reportModel(t)
	for _, tc := range []struct {
		arg         string
		wantProject string
		wantPeriod  string
	}{
		{"bada", "bada", "7d"},
		{"bada 30d", "bada", "30d"},
		{"bada month", "bada", "month"},
		{"BADA", "bada", "7d"},   // projects match case-insensitively
		{"meari", "Meari", "7d"}, // and keep their own capitalization
		{"  bada   week  ", "bada", "week"},
	} {
		project, period, err := m.parseReportArgs(tc.arg)
		if err != nil {
			t.Errorf("parseReportArgs(%q): %v", tc.arg, err)
			continue
		}
		if project != tc.wantProject || period != tc.wantPeriod {
			t.Errorf("parseReportArgs(%q) = %q, %q; want %q, %q",
				tc.arg, project, period, tc.wantProject, tc.wantPeriod)
		}
	}
}

// With no argument the report follows the scoped project, like :gitlog.
func TestParseReportArgsFallsBackToTheScopedProject(t *testing.T) {
	m := reportModel(t)
	m.currentTopic = "bada"
	project, period, err := m.parseReportArgs("")
	if err != nil {
		t.Fatalf("parseReportArgs: %v", err)
	}
	if project != "bada" || period != "7d" {
		t.Errorf("got %q, %q; want the scoped project and the default period", project, period)
	}

	// With nothing scoped, the dashboard cursor stands in — the same fallback
	// chain :gitlog uses.
	m.currentTopic = ""
	project, _, err = m.parseReportArgs("")
	if err != nil {
		t.Fatalf("parseReportArgs with a dashboard selection: %v", err)
	}
	if want, _ := m.dashboardCurrentTopic(); project != want {
		t.Errorf("project = %q, want the dashboard selection %q", project, want)
	}

	// With no projects at all there is genuinely nothing to report on.
	empty := newTestModel(t)
	if _, _, err := empty.parseReportArgs(""); err == nil {
		t.Error("expected an error when there is no project to report on")
	}
}

func TestParseReportArgsHonorsTheConfiguredDefaultPeriod(t *testing.T) {
	m := reportModel(t)
	m.cfg.Report.DefaultPeriod = "30d"
	if _, period, err := m.parseReportArgs("bada"); err != nil || period != "30d" {
		t.Errorf("period = %q (err %v), want the configured default", period, err)
	}
}

func TestParseReportArgsRejectsAnUnknownProject(t *testing.T) {
	m := reportModel(t)
	_, _, err := m.parseReportArgs("nope")
	if err == nil {
		t.Fatal("expected an error for an unknown project")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the project, got %v", err)
	}
}

// A project whose name ends in a word that is not a period keeps that word.
func TestParseReportArgsKeepsMultiWordProjectNames(t *testing.T) {
	m := reportModel(t)
	m.topicMeta["Q3 planning"] = storage.TopicMeta{Topic: "Q3 planning"}
	project, period, err := m.parseReportArgs("Q3 planning")
	if err != nil {
		t.Fatalf("parseReportArgs: %v", err)
	}
	if project != "Q3 planning" || period != "7d" {
		t.Errorf("got %q, %q; want the full project name", project, period)
	}
}

// A project whose name is itself a period keyword must still be reachable
// by name when it is the only argument.
func TestParseReportArgsAllowsAProjectNamedLikeAPeriod(t *testing.T) {
	m := reportModel(t)
	m.topicMeta["week"] = storage.TopicMeta{Topic: "week"}
	project, period, err := m.parseReportArgs("week")
	if err != nil {
		t.Fatalf("parseReportArgs: %v", err)
	}
	if project != "week" {
		t.Errorf("project = %q, want the project named \"week\"", project)
	}
	if period != m.cfg.Report.ResolvedPeriod() {
		t.Errorf("period = %q, want the default period", period)
	}
}

// The report is written from the named project's tasks only.
func TestReportInputSelectsTheProjectsTasks(t *testing.T) {
	m := reportModel(t)
	in := m.reportInput("bada", "7d", 7)
	if len(in.Tasks) != 1 || in.Tasks[0].Title != "Ship it" {
		t.Fatalf("tasks = %+v, want only the bada task", in.Tasks)
	}
	if in.Meta.Description != "TUI todo app" {
		t.Errorf("project metadata not carried through: %+v", in.Meta)
	}
	if in.RepoNote == "" {
		t.Error("a project with no linked repo should carry a note explaining it")
	}
	if !in.Since.Equal(report.Since(in.Now, 7)) {
		t.Errorf("Since = %s, want the start of the 7-day window", in.Since)
	}
}

// Nothing should reach the network before the config is known to be usable.
func TestReportRefusesWithoutAWorkingLLM(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	m := reportModel(t)
	m.cfg.LLM = config.LLM{}
	res, cmd := m.enterProjectReportView("bada", modeList)
	m = res.(Model)
	if cmd != nil {
		t.Error("an unconfigured LLM should not dispatch a request")
	}
	if m.mode == modeProjectReport {
		t.Error("the report view should not open when it cannot be filled")
	}
	if !strings.Contains(m.status, "LLM") {
		t.Errorf("status = %q, want it to explain the LLM is not configured", m.status)
	}
}

func TestReportRefusesABadPeriod(t *testing.T) {
	m := reportModel(t)
	res, cmd := m.enterProjectReportView("bada 7 fortnights", modeList)
	m = res.(Model)
	if cmd != nil {
		t.Error("a bad period should not dispatch a request")
	}
	if !strings.Contains(m.status, "unknown project") && !strings.Contains(m.status, "period") {
		t.Errorf("status = %q, want it to explain the bad argument", m.status)
	}
}

// A broken [report].system_prompt_file must be caught before the view opens.
func TestReportRefusesABrokenPromptFile(t *testing.T) {
	m := reportModel(t)
	m.cfg.Report.SystemPromptFile = filepath.Join(t.TempDir(), "missing.md")
	res, cmd := m.enterProjectReportView("bada", modeList)
	m = res.(Model)
	if cmd != nil {
		t.Error("an unreadable prompt file should not dispatch a request")
	}
	if !strings.Contains(m.status, "system_prompt_file") {
		t.Errorf("status = %q, want it to name the broken setting", m.status)
	}
}

func TestReportOpensAndDispatches(t *testing.T) {
	m := reportModel(t)
	res, cmd := m.enterProjectReportView("bada", modeList)
	m = res.(Model)
	if cmd == nil {
		t.Fatal("a valid report request should dispatch")
	}
	if m.mode != modeProjectReport || m.projectReport == nil {
		t.Fatal("the report view should open")
	}
	if !m.projectReport.loading {
		t.Error("the view should show that it is still working")
	}
	if !strings.Contains(m.View(), "Report") {
		t.Error("the rendered view should be the report panel")
	}
}

func TestHandleProjectReportStoresTheBody(t *testing.T) {
	m := reportModel(t)
	res, _ := m.enterProjectReportView("bada", modeList)
	m = res.(Model)

	res, _ = m.handleProjectReport(projectReportMsg{
		seq: m.projectReport.seq, project: "bada", period: "7d", model: "llama3.2", body: "## Summary\nAll good.",
	})
	m = res.(Model)
	if m.projectReport.loading {
		t.Error("the view should no longer be loading")
	}
	if !strings.Contains(m.projectReport.body, "All good.") {
		t.Errorf("body = %q", m.projectReport.body)
	}
	if !strings.Contains(m.View(), "All good.") {
		t.Error("the report body should be rendered")
	}
	if !strings.Contains(m.status, "s saves it") {
		t.Errorf("status = %q, want the save hint", m.status)
	}
}

// A result arriving after the user asked for a different report is stale.
func TestHandleProjectReportIgnoresStaleResults(t *testing.T) {
	m := reportModel(t)
	res, _ := m.enterProjectReportView("bada 30d", modeList)
	m = res.(Model)
	res, _ = m.handleProjectReport(projectReportMsg{
		seq: m.projectReport.seq - 1, project: "bada", period: "30d", body: "stale",
	})
	m = res.(Model)
	if strings.Contains(m.projectReport.body, "stale") {
		t.Error("a result for a different period must be discarded")
	}
	if !m.projectReport.loading {
		t.Error("the pending report should still be loading")
	}
}

func TestHandleProjectReportShowsFailures(t *testing.T) {
	m := reportModel(t)
	res, _ := m.enterProjectReportView("bada", modeList)
	m = res.(Model)
	res, _ = m.handleProjectReport(projectReportMsg{
		seq: m.projectReport.seq, project: "bada", period: "7d", err: errString("model is offline"),
	})
	m = res.(Model)
	if !strings.Contains(m.status, "offline") {
		t.Errorf("status = %q, want the failure", m.status)
	}
	if !strings.Contains(m.View(), "could not be written") {
		t.Error("the view should explain the failure")
	}
	if !strings.Contains(m.View(), ":llm test") {
		t.Error("the failure view should point at the connection check")
	}
}

func TestReportViewSavesToDisk(t *testing.T) {
	dir := t.TempDir()
	m := reportModel(t)
	m.cfg.Report.Dir = dir
	res, _ := m.enterProjectReportView("bada", modeList)
	m = res.(Model)
	res, _ = m.handleProjectReport(projectReportMsg{
		seq: m.projectReport.seq, project: "bada", period: "7d", model: "llama3.2", body: "## Summary\nShipped the client.",
	})
	m = res.(Model)

	res, _ = m.updateProjectReportMode("s")
	m = res.(Model)
	if !strings.Contains(m.status, "Saved") {
		t.Fatalf("status = %q, want a save confirmation", m.status)
	}
	want := filepath.Join(dir, "bada-"+time.Now().Format("2006-01-02")+".md")
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected a report at %s: %v", want, err)
	}
	if !strings.Contains(string(body), "Shipped the client.") {
		t.Errorf("saved file missing the report body:\n%s", body)
	}
	if m.projectReport.savedPath != want {
		t.Errorf("savedPath = %q, want %q", m.projectReport.savedPath, want)
	}
	// The path is shown in the view too, though the panel may wrap it.
	if !strings.Contains(m.projectReportContent(), want) {
		t.Error("the view should show where the report was saved")
	}
}

func TestReportViewRefusesToSaveNothing(t *testing.T) {
	m := reportModel(t)
	res, _ := m.enterProjectReportView("bada", modeList)
	m = res.(Model)
	res, _ = m.updateProjectReportMode("s") // still loading
	m = res.(Model)
	if !strings.Contains(m.status, "Nothing to save") {
		t.Errorf("status = %q, want a refusal", m.status)
	}
}

func TestReportViewScrollsAndCloses(t *testing.T) {
	m := reportModel(t)
	res, _ := m.enterProjectReportView("bada", modeList)
	m = res.(Model)
	res, _ = m.handleProjectReport(projectReportMsg{
		seq: m.projectReport.seq, project: "bada", period: "7d",
		body: strings.TrimSpace(strings.Repeat("a line of report text\n", 200)),
	})
	m = res.(Model)

	res, _ = m.updateProjectReportMode(m.cfg.Keys.Down)
	m = res.(Model)
	if m.projectReport.scroll != 1 {
		t.Errorf("scroll = %d, want 1", m.projectReport.scroll)
	}
	res, _ = m.updateProjectReportMode("G")
	m = res.(Model)
	if m.projectReport.scroll != m.projectReportMaxScroll() || m.projectReport.scroll == 0 {
		t.Errorf("scroll = %d, want the bottom (%d)", m.projectReport.scroll, m.projectReportMaxScroll())
	}

	res, _ = m.updateProjectReportMode("esc")
	m = res.(Model)
	if m.mode != modeList || m.projectReport != nil {
		t.Errorf("esc should close the view, mode = %v", m.mode)
	}
}

func TestReportPromptShowsTheEffectivePrompt(t *testing.T) {
	m := reportModel(t)
	res, cmd := m.showReportPrompt(modeList)
	m = res.(Model)
	if cmd != nil {
		t.Error("showing the prompt should not contact the model")
	}
	if m.mode != modeProjectReport || !m.projectReport.promptOnly {
		t.Fatal("the prompt should open in the report view")
	}
	if !strings.Contains(m.projectReport.body, "## Summary") {
		t.Errorf("body should be the default prompt, got %q", m.projectReport.body)
	}
	if !strings.Contains(m.status, "built-in default") {
		t.Errorf("status = %q, want the prompt's source", m.status)
	}
	// The prompt view is not a report, so there is nothing to save.
	res, _ = m.updateProjectReportMode("s")
	if !strings.Contains(res.(Model).status, "Nothing to save") {
		t.Errorf("status = %q, want a refusal", res.(Model).status)
	}
}

func TestReportPromptNamesAConfiguredOverride(t *testing.T) {
	m := reportModel(t)
	m.cfg.Report.SystemPrompt = "Be terse."
	m.cfg.Report.ExtraInstructions = "In Korean."
	res, _ := m.showReportPrompt(modeList)
	m = res.(Model)
	if !strings.Contains(m.status, "system_prompt") {
		t.Errorf("status = %q, want the override named", m.status)
	}
	if !strings.Contains(m.projectReport.body, "Be terse.") ||
		!strings.Contains(m.projectReport.body, "In Korean.") {
		t.Errorf("body = %q, want the override and the extras", m.projectReport.body)
	}
}

// ":report" and ":report prompt" both have to route from the command line.
func TestReportCommandDispatch(t *testing.T) {
	m := reportModel(t)
	m.mode = modeCommand
	m.input.SetValue(":report prompt")
	res, cmd := m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if cmd != nil {
		t.Error(":report prompt should not contact the model")
	}
	if m.mode != modeProjectReport || !m.projectReport.promptOnly {
		t.Fatalf("mode = %v, want the prompt view", m.mode)
	}

	m = reportModel(t)
	m.mode = modeCommand
	m.input.SetValue(":report bada 30d")
	res, cmd = m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if cmd == nil {
		t.Error(":report should dispatch")
	}
	if m.projectReport == nil || m.projectReport.period != "30d" {
		t.Errorf("period not carried through: %+v", m.projectReport)
	}
}

func TestReportIsCompletableAndDocumented(t *testing.T) {
	if got := completeCommand(":rep"); got != ":report" {
		t.Errorf("completeCommand(\":rep\") = %q, want \":report\"", got)
	}
	if !strings.Contains(newTestModel(t).helpContent(), ":report") {
		t.Error("help content should document :report")
	}
}

// errString is a minimal error for table-driven cases.
type errString string

func (e errString) Error() string { return string(e) }

var _ = sql.NullTime{}
