package ui

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"bada/internal/config"
	"bada/internal/storage"

	"github.com/charmbracelet/bubbles/textinput"
)

// TestPreviewView seeds a temp store and prints the rendered list + report
// views so we can eyeball the UI restyle. Run with:
//
//	go test ./internal/ui -run TestPreviewView -v
func TestPreviewView(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "bada.db"), filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	seed := []struct {
		title string
		pri   int
		due   time.Time
		topic string
		done  bool
	}{
		{"Review PR #1421 for the scheduler", 3, now.Add(48 * time.Hour), "work", false},
		{"Write design spec for sync backend", 5, now.Add(-24 * time.Hour), "work", false},
		{"Plan 3-day hiking trip", 1, time.Time{}, "personal", false},
		{"Buy groceries for the week", 2, now.Add(20 * time.Hour), "home", false},
		{"Renew passport", 8, now.Add(-72 * time.Hour), "personal", false},
		{"Refactor storage layer", 4, now.Add(96 * time.Hour), "work", true},
	}
	for _, s := range seed {
		id, err := store.AddTask(s.title)
		if err != nil {
			t.Fatal(err)
		}
		_ = store.UpdatePriority(id, s.pri)
		var due sql.NullTime
		if !s.due.IsZero() {
			due = sql.NullTime{Time: s.due, Valid: true}
		}
		_ = store.UpdateTaskMetadata(id, s.topic, "", "", s.pri, due, sql.NullTime{}, false)
		if s.done {
			_ = store.SetDone(id, true)
		}
	}

	tasks, err := store.FetchTasks()
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadOrCreate(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ti := textinput.New()
	ti.Prompt = ""
	m := Model{
		store:         store,
		cfg:           cfg,
		tasks:         tasks,
		trashSelected: map[int]bool{},
		selectedTasks: map[int]bool{},
		input:         ti,
		mode:          modeList,
		recentLimit:   5,
		filterDone:    "all",
		sortMode:      "auto",
		styles:        buildStyles(cfg.Theme),
		width:         96,
		height:        30,
	}
	m.sortTasks()
	m.cursor = 1

	fmt.Println("\n========== LIST VIEW (topics) ==========")
	fmt.Println(m.View())

	m.currentTopic = "work"
	m.cursor = 1
	fmt.Println("\n========== LIST VIEW (inside 'work', row 2 selected) ==========")
	fmt.Println(m.View())

	m.refreshReport()
	m.mode = modeReport
	fmt.Println("\n========== REPORT VIEW ==========")
	fmt.Println(m.View())
}
