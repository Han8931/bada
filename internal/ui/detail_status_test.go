package ui

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"bada/internal/storage"
)

// TestDetailShowsStatus verifies the task detail/notes view (opened with Enter)
// includes a Status row with the real status plus an overdue tag.
func TestDetailShowsStatus(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 90, 30
	id, _ := m.store.AddTask("Reply to advisor")
	m.store.UpdateTaskMetadata(id, "Thesis", "", "", "", "", 2,
		sql.NullTime{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		sql.NullTime{}, sql.NullTime{}, false)
	m.store.SetStatus(id, "IN-PROGRESS", false)
	m.reload()
	m.note = &noteState{target: noteTarget{kind: noteTask, taskID: id, title: "Reply to advisor"}}

	out := stripAnsiTest(strings.Join(m.noteMetaBlockLines(), "\n"))
	if !strings.Contains(out, "Status") {
		t.Fatalf("detail missing Status row:\n%s", out)
	}
	if !strings.Contains(out, "IN-PROGRESS") {
		t.Errorf("Status should show the real status IN-PROGRESS, got:\n%s", out)
	}
	if !strings.Contains(out, "overdue") {
		t.Errorf("overdue task should show an overdue tag, got:\n%s", out)
	}
}

// TestStatusCellShowsStage verifies a custom workflow stage is shown, not folded.
func TestStatusCellShowsStage(t *testing.T) {
	m := newTestModel(t)
	m.workflows = map[string][]storage.Stage{"Thesis": {
		{Name: "writing", Category: storage.StagePending},
		{Name: "review", Category: storage.StageActive},
	}}
	task := storage.Task{PrimaryTopic: "Thesis", Status: "review"}
	if got := stripAnsiTest(m.statusCell(task)); !strings.Contains(got, "review") {
		t.Errorf("statusCell = %q, want it to show the 'review' stage", got)
	}
}
