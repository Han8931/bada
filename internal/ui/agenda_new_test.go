package ui

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"bada/internal/storage"
)

func agendaSeed(t *testing.T) Model {
	m := newTestModel(t)
	m.width, m.height = 100, 30
	due := func(d int) sql.NullTime { return sql.NullTime{Time: time.Now().AddDate(0, 0, d), Valid: true} }
	add := func(title, topic string, prio, dd int, dated bool) {
		id, _ := m.store.AddTask(title)
		var d sql.NullTime
		if dated {
			d = due(dd)
		}
		m.store.UpdateTaskMetadata(id, topic, "", "", "", "", prio, d, sql.NullTime{}, sql.NullTime{}, false)
		m.store.SetPrimaryTopic(id, topic)
	}
	add("Overdue thing", "Finance", 3, -2, true)
	add("Reply advisor", "Thesis", 2, -1, true)
	add("Undated important", "Ideas", 3, 0, false) // no date, prioritized
	m.reload()
	m.mode = modeReport
	return m
}

func TestAgendaSummaryAndSections(t *testing.T) {
	m := agendaSeed(t)
	m.refreshReport()
	head := stripAnsiTest(m.renderReportHeader())
	if !strings.Contains(head, "overdue") {
		t.Errorf("header missing summary strip:\n%s", head)
	}
	if !strings.Contains(head, "Next 7 days") {
		t.Error("header missing week sparkline")
	}
	body := stripAnsiTest(m.report)
	if !strings.Contains(body, "No date (1)") {
		t.Errorf("expected No date section, got:\n%s", body)
	}
	// project chip appears on rows
	if !strings.Contains(body, "Finance") {
		t.Error("row missing project chip")
	}
}

func TestAgendaScopeFilters(t *testing.T) {
	m := agendaSeed(t)
	m.currentTopic = "Thesis"
	scoped := m.agendaTasks()
	for _, tk := range scoped {
		if !taskHasTopic(tk, "Thesis") {
			t.Errorf("scoped agenda included non-Thesis task %q", tk.Title)
		}
	}
	if len(scoped) != 1 {
		t.Errorf("scoped agenda tasks = %d, want 1", len(scoped))
	}
	if !strings.Contains(stripAnsiTest(m.renderReportHeader()), "Agenda · Thesis") {
		t.Error("scoped header should show 'Agenda · Thesis'")
	}
}

func TestAgendaFoldHidesBanner(t *testing.T) {
	m := agendaSeed(t)
	full := countLinesTest(m.renderReportHeader())
	m.agendaHeaderFold = true
	folded := countLinesTest(m.renderReportHeader())
	if folded >= full {
		t.Errorf("folded header (%d lines) should be shorter than full (%d)", folded, full)
	}
}

func TestAgendaWidthSafe(t *testing.T) {
	m := agendaSeed(t)
	m.width = 80
	m.store.SetTopicWorkflow("Thesis", []storage.Stage{{Name: "reviewing-the-manuscript", Category: storage.StageActive}})
	m.reload()
	m.refreshReport()
	for i, l := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("agenda line %d width %d > %d: %q", i, w, m.width, l)
		}
	}
}

func countLinesTest(s string) int { return len(strings.Split(strings.TrimRight(s, "\n"), "\n")) }
