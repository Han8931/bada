package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/storage"
)

// key feeds a keypress to the dashboard the way the event loop would.
func dashKey(t *testing.T, m Model, k string) Model {
	t.Helper()
	res, _ := m.updateDashboardMode(k, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	next, ok := res.(Model)
	if !ok {
		t.Fatalf("updateDashboardMode(%q) did not return a Model", k)
	}
	return next
}

func typeInto(m Model, text string) Model {
	m.input.SetValue(text)
	return m
}

func enter(t *testing.T, m Model) Model {
	t.Helper()
	res, _ := m.updateDashboardMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := res.(Model)
	if !ok {
		t.Fatalf("enter did not return a Model")
	}
	return next
}

// A project created with no tasks must show up in the overview — that's the
// whole point of registering one.
func TestCreateProjectWithoutTasksIsListed(t *testing.T) {
	m := newTestModel(t)
	dash, _ := m.enterDashboardView()
	m = dash.(Model)

	m = dashKey(t, m, "n")
	if m.dashEditing != "new" {
		t.Fatalf("dashEditing = %q, want \"new\"", m.dashEditing)
	}
	m = typeInto(m, "bada")
	m = enter(t, m)

	if m.dashEditing != "" {
		t.Errorf("prompt still open: %q", m.dashEditing)
	}
	topics := m.sortedTopics()
	if len(topics) != 1 || topics[0] != "bada" {
		t.Fatalf("sortedTopics = %v, want [bada]", topics)
	}
	if !strings.Contains(m.dashboardContent(), "bada") {
		t.Error("new project missing from the dashboard body")
	}
	// And it survives a reload from the database, not just in memory.
	all, err := m.store.AllTopicMeta()
	if err != nil {
		t.Fatalf("AllTopicMeta: %v", err)
	}
	if _, ok := all["bada"]; !ok {
		t.Error("new project was not persisted")
	}
}

func TestCreateProjectRejectsBlankAndCommaNames(t *testing.T) {
	m := newTestModel(t)
	dash, _ := m.enterDashboardView()
	m = dash.(Model)

	m = dashKey(t, m, "n")
	m = typeInto(m, "   ")
	m = enter(t, m)
	if m.dashEditing != "new" {
		t.Error("blank name should leave the prompt open")
	}
	if len(m.sortedTopics()) != 0 {
		t.Errorf("blank name created a project: %v", m.sortedTopics())
	}

	m = typeInto(m, "work,home")
	m = enter(t, m)
	if len(m.sortedTopics()) != 0 {
		t.Errorf("comma name created a project: %v", m.sortedTopics())
	}
	if !strings.Contains(m.status, "comma") {
		t.Errorf("status = %q, want it to explain the comma rule", m.status)
	}
}

func TestCreateProjectTwiceDoesNotDuplicate(t *testing.T) {
	m := newTestModel(t)
	dash, _ := m.enterDashboardView()
	m = dash.(Model)

	m = dashKey(t, m, "n")
	m = typeInto(m, "bada")
	m = enter(t, m)
	m = dashKey(t, m, "n")
	m = typeInto(m, "bada")
	m = enter(t, m)

	if got := m.sortedTopics(); len(got) != 1 {
		t.Fatalf("sortedTopics = %v, want one entry", got)
	}
	if !strings.Contains(m.status, "already exists") {
		t.Errorf("status = %q, want an 'already exists' notice", m.status)
	}
}

// A project implied by a task and one registered on its own are the same
// project — the overview must not list it twice.
func TestTaskTopicAndRegisteredProjectDoNotDuplicate(t *testing.T) {
	m := newTestModel(t)
	m.tasks = []storage.Task{{ID: 1, Title: "ship", Topics: []string{"bada"}, PrimaryTopic: "bada"}}
	if err := m.store.CreateTopic("bada"); err != nil {
		t.Fatal(err)
	}
	m.refreshTopicMeta()

	if got := m.sortedTopics(); len(got) != 1 || got[0] != "bada" {
		t.Fatalf("sortedTopics = %v, want [bada]", got)
	}
}

func TestSetProjectRepoStoresTopLevel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	dash, _ := m.enterDashboardView()
	m = dash.(Model)
	m = dashKey(t, m, "n")
	m = typeInto(m, "bada")
	m = enter(t, m)

	// Point it at a subdirectory; the repo's top level is what gets stored.
	m = dashKey(t, m, "g")
	if m.dashEditing != "repo" {
		t.Fatalf("dashEditing = %q, want \"repo\"", m.dashEditing)
	}
	m = typeInto(m, sub)
	m = enter(t, m)

	if m.dashEditing != "" {
		t.Fatalf("repo prompt still open, status = %q", m.status)
	}
	got := m.topicMeta["bada"].RepoPath
	wantTop, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	gotTop, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", got, err)
	}
	if gotTop != wantTop {
		t.Errorf("RepoPath = %q, want the repo top level %q", gotTop, wantTop)
	}
	if !strings.Contains(m.dashboardDetail("bada"), "Repo:") {
		t.Error("detail panel does not show the repo")
	}
}

// A bad path must not be silently accepted — the prompt stays open so it can be
// corrected in place.
func TestSetProjectRepoRejectsNonRepoPath(t *testing.T) {
	m := newTestModel(t)
	dash, _ := m.enterDashboardView()
	m = dash.(Model)
	m = dashKey(t, m, "n")
	m = typeInto(m, "bada")
	m = enter(t, m)

	m = dashKey(t, m, "g")
	m = typeInto(m, filepath.Join(t.TempDir(), "does-not-exist"))
	m = enter(t, m)

	if m.dashEditing != "repo" {
		t.Errorf("dashEditing = %q, want the prompt to stay open", m.dashEditing)
	}
	if m.topicMeta["bada"].RepoPath != "" {
		t.Errorf("RepoPath = %q, want it unset", m.topicMeta["bada"].RepoPath)
	}
	if m.status == "" {
		t.Error("expected an explanatory status message")
	}
}

func TestClearProjectRepo(t *testing.T) {
	m := newTestModel(t)
	if err := m.store.CreateTopic("bada"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetTopicRepo("bada", "/tmp/repo"); err != nil {
		t.Fatal(err)
	}
	m.refreshTopicMeta()
	dash, _ := m.enterDashboardView()
	m = dash.(Model)

	m = dashKey(t, m, "g")
	m = typeInto(m, "")
	m = enter(t, m)

	if m.topicMeta["bada"].RepoPath != "" {
		t.Errorf("RepoPath = %q, want cleared", m.topicMeta["bada"].RepoPath)
	}
}

func TestProjectCommandCreatesAndKeepsCase(t *testing.T) {
	m := newTestModel(t)

	res, _ := m.runProjectCommand("new Bada TUI")
	m = res.(Model)

	topics := m.sortedTopics()
	if len(topics) != 1 || topics[0] != "Bada TUI" {
		t.Fatalf("sortedTopics = %v, want [Bada TUI]", topics)
	}
	if m.mode != modeDashboard {
		t.Errorf("mode = %v, want the dashboard", m.mode)
	}
}

func TestProjectCommandWithoutArgsOpensOverview(t *testing.T) {
	m := newTestModel(t)
	res, _ := m.runProjectCommand("")
	m = res.(Model)
	if m.mode != modeDashboard {
		t.Errorf("mode = %v, want the dashboard", m.mode)
	}
}

func TestProjectCommandRejectsUnknownSubcommand(t *testing.T) {
	m := newTestModel(t)
	res, _ := m.runProjectCommand("frobnicate thing")
	m = res.(Model)
	if len(m.sortedTopics()) != 0 {
		t.Errorf("unknown subcommand created a project: %v", m.sortedTopics())
	}
	if !strings.Contains(m.status, "unknown") {
		t.Errorf("status = %q, want an 'unknown' notice", m.status)
	}
}

// Deleting a project must also drop its registration, or with projects now
// sourced from metadata as well as tasks it would come back.
func TestDeleteProjectRemovesItFromTheOverview(t *testing.T) {
	m := newTestModel(t)
	if err := m.store.CreateTopic("bada"); err != nil {
		t.Fatal(err)
	}
	m.refreshTopicMeta()
	dash, _ := m.enterDashboardView()
	m = dash.(Model)

	m = dashKey(t, m, "D")
	if !m.confirmTopic || m.pendingTopic != "bada" {
		t.Fatalf("delete did not ask for confirmation (confirm=%v, pending=%q)", m.confirmTopic, m.pendingTopic)
	}
	res, _ := m.updateDeleteTopicConfirm("y")
	m = res.(Model)

	if got := m.sortedTopics(); len(got) != 0 {
		t.Errorf("sortedTopics = %v, want empty after delete", got)
	}
}

// tab feeds a Tab keypress to the dashboard the way the event loop would.
func dashTab(t *testing.T, m Model) Model {
	t.Helper()
	res, _ := m.updateDashboardMode("tab", tea.KeyMsg{Type: tea.KeyTab})
	next, ok := res.(Model)
	if !ok {
		t.Fatalf("tab did not return a Model")
	}
	return next
}

// openRepoPrompt registers a project and opens its repo prompt.
func openRepoPrompt(t *testing.T, name string) Model {
	t.Helper()
	m := newTestModel(t)
	dash, _ := m.enterDashboardView()
	m = dash.(Model)
	m = dashKey(t, m, "n")
	m = typeInto(m, name)
	m = enter(t, m)
	m = dashKey(t, m, "g")
	if m.dashEditing != "repo" {
		t.Fatalf("dashEditing = %q, want \"repo\"", m.dashEditing)
	}
	return m
}

// Tab must both fill in the field and show what else is there — finding the
// directory is the hard part of linking a repo.
func TestRepoPromptTabCompletesAndListsCandidates(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"project-one", "project-two", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	m := openRepoPrompt(t, "bada")
	m = typeInto(m, filepath.Join(root, "pro"))
	m = dashTab(t, m)

	if want := filepath.Join(root, "project-"); m.input.Value() != want {
		t.Errorf("input = %q, want the shared prefix %q", m.input.Value(), want)
	}
	if len(m.dashComplete) != 2 {
		t.Fatalf("dashComplete = %d entries, want 2", len(m.dashComplete))
	}
	prompt := m.dashboardPrompt()
	for _, want := range []string{"project-one/", "project-two/"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt does not list %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "notes/") {
		t.Errorf("prompt lists a non-matching directory:\n%s", prompt)
	}
	if m.dashEditing != "repo" {
		t.Error("tab closed the prompt; it must stay open")
	}
}

// A single match is written straight into the field, so re-listing it below
// would be noise.
func TestRepoPromptTabOnUniqueMatchDoesNotList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bada"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := openRepoPrompt(t, "bada")
	m = typeInto(m, filepath.Join(root, "ba"))
	m = dashTab(t, m)

	if want := filepath.Join(root, "bada") + "/"; m.input.Value() != want {
		t.Errorf("input = %q, want %q", m.input.Value(), want)
	}
	if got := strings.Count(m.dashboardPrompt(), "\n"); got != 0 {
		t.Errorf("prompt spans %d extra lines, want a single line:\n%s", got, m.dashboardPrompt())
	}
}

// Typing after a Tab must drop the stale list.
func TestRepoPromptEditingClearsCandidates(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"alpha", "amber"} {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	m := openRepoPrompt(t, "bada")
	m = typeInto(m, filepath.Join(root, "a"))
	m = dashTab(t, m)
	if len(m.dashComplete) != 2 {
		t.Fatalf("dashComplete = %d entries, want 2", len(m.dashComplete))
	}

	res, _ := m.updateDashboardMode("x", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = res.(Model)
	if m.dashComplete != nil {
		t.Errorf("dashComplete survived an edit: %v", m.dashComplete)
	}
}

// Tab is only meaningful on the repo prompt; elsewhere it must not scribble
// filesystem paths into the field.
func TestTabDoesNothingOnOtherProjectPrompts(t *testing.T) {
	m := newTestModel(t)
	dash, _ := m.enterDashboardView()
	m = dash.(Model)
	m = dashKey(t, m, "n")
	m = typeInto(m, "bada")
	m = enter(t, m)
	m = dashKey(t, m, "e") // description
	m = typeInto(m, "us")
	m = dashTab(t, m)

	if m.input.Value() != "us" {
		t.Errorf("description = %q, want it untouched", m.input.Value())
	}
	if m.dashComplete != nil {
		t.Errorf("dashComplete = %v, want none", m.dashComplete)
	}
}
