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

// TestAgendaCenteredColumn confirms the launch screen is a centered column (the
// gorae / meari style): the wordmark sits in the middle of the terminal, its
// glyph rows share one left edge, and the agenda rows below are indented as a
// block with roughly equal margins on both sides.
func TestAgendaCenteredColumn(t *testing.T) {
	m := agendaSeed(t)
	m.width, m.height = 140, 40
	m.refreshReport()

	lines := strings.Split(stripAnsiTest(stripEraseTest(m.View())), "\n")
	// The wordmark is the first block in the view; its six rows run consecutively.
	top := -1
	for i, l := range lines {
		if strings.Contains(l, "██████╗") {
			top = i
			break
		}
	}
	if top < 0 || top+6 > len(lines) {
		t.Fatalf("no wordmark in the agenda view:\n%s", strings.Join(lines, "\n"))
	}
	bannerRows := lines[top : top+6]
	indent := leadingSpacesTest(bannerRows[0])
	for i, row := range bannerRows {
		if got := leadingSpacesTest(row); got != indent {
			t.Errorf("wordmark row %d starts at column %d, want %d (rows must share a left edge)", i, got, indent)
		}
	}
	// The wordmark is 32 cells wide; centered in 140 it starts near column 54.
	if want := (m.width - 32) / 2; indent < want-2 || indent > want+2 {
		t.Errorf("wordmark indent %d, want ~%d (centered)", indent, want)
	}

	// A full-width section rule shows where the column sits: it must be inset on
	// the left and stop short of the right edge by about as much.
	var rule string
	for _, l := range lines {
		if strings.Contains(l, "Overdue (") {
			rule = l
		}
	}
	if rule == "" {
		t.Fatal("no Overdue section header in the view")
	}
	left := leadingSpacesTest(rule)
	right := m.width - lipgloss.Width(strings.TrimRight(rule, " "))
	if left < 4 {
		t.Errorf("agenda column not indented (left margin %d)", left)
	}
	if diff := left - right; diff > 4 || diff < -4 {
		t.Errorf("agenda column off-center: left margin %d, right margin %d", left, right)
	}
}

func leadingSpacesTest(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

// stripEraseTest removes the trailing erase-to-end-of-line sequences the view
// appends, which stripAnsiTest (which scans to the next "m") cannot handle.
func stripEraseTest(s string) string { return strings.ReplaceAll(s, "\x1b[K", "") }

func countLinesTest(s string) int { return len(strings.Split(strings.TrimRight(s, "\n"), "\n")) }
