package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/config"
	"bada/internal/storage"
)

func TestParseDueInput(t *testing.T) {
	// Fixed reference: Sunday, 2026-06-14.
	now := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want string // "" means invalid-expected via wantErr
		err  bool
	}{
		{"", "", false},
		{"today", "2026-06-14", false},
		{"tomorrow", "2026-06-15", false},
		{"in 3d", "2026-06-17", false},
		{"+2w", "2026-06-28", false},
		{"5d", "2026-06-19", false},
		{"monday", "2026-06-15", false}, // next Monday
		{"2026-07-01", "2026-07-01", false},
		{"2026-07-01 14:30", "2026-07-01 14:30", false},
		{"tomorrow 14:30", "2026-06-15 14:30", false},
		{"banana", "", true},
	}
	for _, c := range cases {
		got, err := parseDueInput(c.in, now)
		if c.err {
			if err == nil {
				t.Errorf("parseDueInput(%q): expected error, got %v", c.in, got.Time)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDueInput(%q): unexpected error %v", c.in, err)
			continue
		}
		if c.want == "" {
			if got.Valid {
				t.Errorf("parseDueInput(%q): expected empty, got %v", c.in, got.Time)
			}
			continue
		}
		layout := "2006-01-02"
		if len(c.want) > 10 {
			layout = "2006-01-02 15:04"
		}
		if got.Time.Format(layout) != c.want {
			t.Errorf("parseDueInput(%q) = %v, want %s", c.in, got.Time.Format(layout), c.want)
		}
	}
}

func newTestModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "bada.db"), filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg, err := config.LoadOrCreate(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ := store.FetchTasks()
	m := newModelForTest(store, cfg, tasks)
	return m
}

func newModelForTest(store *storage.Store, cfg config.Config, tasks []storage.Task) Model {
	m := Model{
		store:         store,
		cfg:           cfg,
		tasks:         tasks,
		trashSelected: map[int]bool{},
		selectedTasks: map[int]bool{},
		mode:          modeList,
		recentLimit:   5,
		filterDone:    "all",
		sortMode:      "auto",
		styles:        buildStyles(cfg.Theme),
		width:         96,
		height:        30,
		workflows:     map[string][]storage.Stage{},
		topicMeta:     map[string]storage.TopicMeta{},
	}
	if wf, err := store.AllTopicWorkflows(); err == nil {
		m.workflows = wf
	}
	if tm, err := store.AllTopicMeta(); err == nil {
		m.topicMeta = tm
	}
	ti := textinput.New()
	ti.Prompt = ""
	m.input = ti
	return m
}

// TestQuickAddEnterSaves verifies the fast path: open the dialog, type a title,
// press Enter, and the task is created without walking every field.
func TestQuickAddEnterSaves(t *testing.T) {
	m := newTestModel(t)

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.input.SetValue("Buy milk")

	res, _ := m.updateMetadataMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)

	if m.meta != nil {
		t.Fatalf("expected modal to close after save, meta still set")
	}
	if m.mode != modeList {
		t.Fatalf("expected modeList after save, got %v", m.mode)
	}
	if len(m.tasks) != 1 || m.tasks[0].Title != "Buy milk" {
		t.Fatalf("expected one task 'Buy milk', got %+v", m.tasks)
	}
}

// TestEmptyTitleBlocksSave keeps the dialog open and flags validation when the
// title is blank.
func TestEmptyTitleBlocksSave(t *testing.T) {
	m := newTestModel(t)

	am, _ := m.startMetadataAdd()
	m = am.(Model)

	res, _ := m.updateMetadataMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)

	if m.meta == nil {
		t.Fatalf("expected modal to stay open on empty title")
	}
	if m.meta.validation == "" {
		t.Fatalf("expected a validation message for empty title")
	}
	if len(m.tasks) != 0 {
		t.Fatalf("expected no task created, got %d", len(m.tasks))
	}
}

// TestEscCancelsWithoutSaving confirms a single Esc discards the form.
func TestEscCancelsWithoutSaving(t *testing.T) {
	m := newTestModel(t)

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.input.SetValue("Should not persist")

	res, _ := m.updateMetadataMode("esc", tea.KeyMsg{Type: tea.KeyEsc})
	m = res.(Model)
	if m.meta != nil {
		t.Fatalf("expected modal closed after esc")
	}
	if len(m.tasks) != 0 {
		t.Fatalf("expected no task created on cancel, got %d", len(m.tasks))
	}
}

// TestTabNavigation confirms Tab/Shift+Tab move between fields in the plain form.
func TestTabNavigation(t *testing.T) {
	m := newTestModel(t)

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.meta.expanded = true // make all fields navigable

	// Tab: Title(0) → Topic(1).
	res, _ := m.updateMetadataMode("tab", tea.KeyMsg{Type: tea.KeyTab})
	m = res.(Model)
	if m.meta.index != 1 {
		t.Fatalf("after tab expected index 1 (Topic), got %d", m.meta.index)
	}

	// Shift+Tab: back to Title(0).
	res, _ = m.updateMetadataMode("shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab})
	m = res.(Model)
	if m.meta.index != 0 {
		t.Fatalf("after shift+tab expected index 0 (Title), got %d", m.meta.index)
	}
}

// TestTypingGoesToField confirms letters are typed into text fields (no vim modes).
func TestTypingGoesToField(t *testing.T) {
	m := newTestModel(t)

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	for _, k := range []string{"h", "i"} {
		res, _ := m.updateMetadataMode(k, keyRunes(k))
		m = res.(Model)
	}
	if m.input.Value() != "hi" {
		t.Fatalf("expected 'hi' typed into Title, got %q", m.input.Value())
	}
}

// TestPriorityStepperArrows confirms +/- and arrows drive the Priority stepper.
func TestPriorityStepperArrows(t *testing.T) {
	m := newTestModel(t)

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.meta.index = 3 // Priority
	m.focusMetaField()

	res, _ := m.updateMetadataMode("+", keyRunes("+"))
	m = res.(Model)
	res, _ = m.updateMetadataMode("right", tea.KeyMsg{Type: tea.KeyRight})
	m = res.(Model)
	if m.meta.priority != "2" {
		t.Fatalf("after + and → expected priority 2, got %q", m.meta.priority)
	}
	res, _ = m.updateMetadataMode("-", keyRunes("-"))
	m = res.(Model)
	if m.meta.priority != "1" {
		t.Fatalf("after - expected priority 1, got %q", m.meta.priority)
	}
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestDueDefaultsToToday confirms a new task's Due field is prefilled with the
// current date so the user can step from it instead of starting blank.
func TestDueDefaultsToToday(t *testing.T) {
	m := newTestModel(t)
	am, _ := m.startMetadataAdd()
	m = am.(Model)

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if got := m.dueTime(now).Format("2006-01-02"); got != today.Format("2006-01-02") {
		t.Fatalf("expected Due prefilled to today (%s), got %s", today.Format("2006-01-02"), got)
	}
}

// TestDueStepper verifies stepping from the prefilled date and clearing it.
func TestDueStepper(t *testing.T) {
	m := newTestModel(t)
	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.meta.index = 4 // Due
	m.focusMetaField()

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Prefilled to today; day part selected, so '+' advances one day.
	res, _ := m.updateMetadataMode("+", keyRunes("+"))
	m = res.(Model)
	if got := m.dueTime(now).Format("2006-01-02"); got != today.AddDate(0, 0, 1).Format("2006-01-02") {
		t.Fatalf("expected tomorrow, got %s", got)
	}

	// Move selection to the month component and step it.
	res, _ = m.updateMetadataMode("left", tea.KeyMsg{Type: tea.KeyLeft}) // day -> month
	m = res.(Model)
	if m.meta.dueComponent != 1 {
		t.Fatalf("expected month component (1), got %d", m.meta.dueComponent)
	}
	res, _ = m.updateMetadataMode("+", keyRunes("+"))
	m = res.(Model)
	// The stepper applied day+ then month+, so mirror that order here. Computing
	// today.AddDate(0,1,1) instead diverges at month-end boundaries (e.g. when
	// "today" is the 30th/31st) because Go normalizes overflowing days.
	want := today.AddDate(0, 0, 1).AddDate(0, 1, 0).Format("2006-01-02")
	if got := m.dueTime(now).Format("2006-01-02"); got != want {
		t.Fatalf("after month +: want %s, got %s", want, got)
	}

	// 'x' clears the due entirely (back to no due).
	res, _ = m.updateMetadataMode("x", keyRunes("x"))
	m = res.(Model)
	if m.meta.due != "" {
		t.Fatalf("expected due cleared, got %q", m.meta.due)
	}
}

// TestDueStepperSavesDate confirms a stepped due actually persists on the task.
func TestDueStepperSavesDate(t *testing.T) {
	m := newTestModel(t)
	am, _ := m.startMetadataAdd()
	m = am.(Model)
	m.input.SetValue("Pay rent")
	m.meta.title = "Pay rent"

	m.meta.index = 4 // Due (prefilled today)
	m.focusMetaField()
	// +1 day from today
	res, _ := m.updateMetadataMode("+", keyRunes("+"))
	m = res.(Model)
	res, _ = m.updateMetadataMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)

	if len(m.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(m.tasks))
	}
	if !m.tasks[0].Due.Valid {
		t.Fatalf("expected task to have a due date")
	}
	now := time.Now()
	want := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	if got := m.tasks[0].Due.Time.Format("2006-01-02"); got != want.Format("2006-01-02") {
		t.Fatalf("expected due %s, got %s", want.Format("2006-01-02"), got)
	}
}

// TestDetailsToggle confirms the More-details row reveals the detail fields.
func TestDetailsToggle(t *testing.T) {
	m := newTestModel(t)

	am, _ := m.startMetadataAdd()
	m = am.(Model)
	if m.meta.expanded {
		t.Fatalf("add modal should start collapsed")
	}
	if got := len(m.meta.order()); got != 5 { // 4 core + toggle
		t.Fatalf("collapsed order = %d rows, want 5", got)
	}

	// Move to the toggle row (after the 4 core fields) and expand.
	m.meta.index = fieldMore
	m.focusMetaField()
	res, _ := m.updateMetadataMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)

	if !m.meta.expanded {
		t.Fatalf("expected details expanded after enter on toggle")
	}
	if got := len(m.meta.order()); got != 14 { // 4 core + toggle + 9 detail
		t.Fatalf("expanded order = %d rows, want 14", got)
	}
}
