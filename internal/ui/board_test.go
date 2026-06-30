package ui

import (
	"strings"
	"testing"

	"bada/internal/storage"
)

func boardModel(t *testing.T) Model {
	m := newTestModel(t)
	stages := []storage.Stage{
		{Name: "writing", Category: storage.StagePending},
		{Name: "review", Category: storage.StageActive},
		{Name: "rebuttal", Category: storage.StageDone},
	}
	if err := m.store.SetTopicWorkflow("thesis", stages); err != nil {
		t.Fatal(err)
	}
	m.workflows = map[string][]storage.Stage{"thesis": stages}
	m.boardTopic = "thesis"
	m.tasks = []storage.Task{
		{ID: 1, Title: "intro", PrimaryTopic: "thesis", Status: "writing"},
		{ID: 2, Title: "method", PrimaryTopic: "thesis", Status: "review"},
		{ID: 3, Title: "results", PrimaryTopic: "thesis", Status: "review"},
		{ID: 4, Title: "other", PrimaryTopic: "side", Status: "writing"},    // excluded
		{ID: 5, Title: "legacy", PrimaryTopic: "thesis", Status: "PENDING"}, // unknown → initial
	}
	return m
}

func TestBoardColumnsBucketing(t *testing.T) {
	m := boardModel(t)
	stages, cols, ok := m.boardColumns()
	if !ok {
		t.Fatal("expected board columns")
	}
	if len(stages) != 3 || len(cols) != 3 {
		t.Fatalf("got %d stages / %d cols", len(stages), len(cols))
	}
	// writing: task 1 + legacy task 5 (unknown status maps to initial stage)
	if len(cols[0]) != 2 {
		t.Errorf("writing column = %d, want 2", len(cols[0]))
	}
	if len(cols[1]) != 2 { // review: tasks 2,3
		t.Errorf("review column = %d, want 2", len(cols[1]))
	}
	if len(cols[2]) != 0 {
		t.Errorf("rebuttal column = %d, want 0", len(cols[2]))
	}
}

func TestStageQuickFilter(t *testing.T) {
	m := boardModel(t)
	review := storage.Task{PrimaryTopic: "thesis", Status: "review"}
	writing := storage.Task{PrimaryTopic: "thesis", Status: "writing"}
	noWF := storage.Task{Status: "review"} // no governing workflow
	if !m.taskMatchesQuickFilter(review, "stage:review") {
		t.Error("review task should match stage:review")
	}
	if m.taskMatchesQuickFilter(writing, "stage:review") {
		t.Error("writing task should not match stage:review")
	}
	if m.taskMatchesQuickFilter(noWF, "stage:review") {
		t.Error("task without workflow should not match a stage filter")
	}
}

func TestStagePositionSort(t *testing.T) {
	m := boardModel(t)
	if pos, ok := m.stagePosition(storage.Task{PrimaryTopic: "thesis", Status: "rebuttal"}); !ok || pos != 2 {
		t.Errorf("rebuttal position = %d, ok=%v, want 2,true", pos, ok)
	}
	if _, ok := m.stagePosition(storage.Task{Status: "x"}); ok {
		t.Error("task without workflow should report no position")
	}
}

// TestUndoRevertsStatus verifies snapshot + applyUndo round-trips a real edit
// through the store.
func TestUndoRevertsStatus(t *testing.T) {
	m := newTestModel(t)
	id, err := m.store.AddTask("task")
	if err != nil {
		t.Fatal(err)
	}
	m.reload()
	before := findTask(m.tasks, id)

	// Make an edit and snapshot the prior state.
	m.snapshotUndo(before, "status change")
	if err := m.store.SetStatus(id, "DONE", true); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if !findTask(m.tasks, id).Done {
		t.Fatal("expected task done after edit")
	}

	// Undo.
	res, _ := m.applyUndo()
	m = res.(Model)
	got := findTask(m.tasks, id)
	if got.Done {
		t.Error("expected task not done after undo")
	}
	if m.undo != nil {
		t.Error("undo entry should be consumed")
	}
	// A second undo is a no-op.
	res, _ = m.applyUndo()
	m = res.(Model)
	if m.status != "Nothing to undo" {
		t.Errorf("second undo status = %q", m.status)
	}
}

func TestEnterBoardRequiresWorkflow(t *testing.T) {
	m := newTestModel(t)
	res, _ := m.enterBoardView("no-such-topic")
	m = res.(Model)
	if m.mode == modeBoard {
		t.Error("should not enter board for a topic without a workflow")
	}
}

// TestBoardRendersStageNames guards the regression where column headers showed
// the category word ("pending") instead of the stage name ("writing").
func TestBoardRendersStageNames(t *testing.T) {
	m := boardModel(t)
	res, _ := m.enterBoardView("thesis")
	m = res.(Model)
	if m.mode != modeBoard {
		t.Fatalf("expected modeBoard, got %d (%s)", m.mode, m.status)
	}
	out := m.View()
	for _, name := range []string{"writing", "review", "rebuttal"} {
		if !strings.Contains(out, name) {
			t.Errorf("board view missing stage name %q", name)
		}
	}
}

// TestBoardFallsBackToBusiestProject verifies a bare :board picks a workflow
// project even when nothing is scoped.
func TestBoardFallsBackToBusiestProject(t *testing.T) {
	m := boardModel(t) // has the "thesis" workflow + tasks, nothing scoped
	res, _ := m.enterBoardView("")
	m = res.(Model)
	if m.mode != modeBoard || m.boardTopic != "thesis" {
		t.Errorf("expected board for thesis, got mode=%d topic=%q (%s)", m.mode, m.boardTopic, m.status)
	}
}

// TestBoardEnterOpensDetail verifies Enter on a card opens its detail/notes view
// for the board-selected task and returns to the board on close.
func TestBoardEnterOpensDetail(t *testing.T) {
	m := boardModel(t)
	res, _ := m.enterBoardView("thesis")
	m = res.(Model)
	m.boardCol, m.boardRow = 1, 1 // second card in the review column
	want, ok := m.boardSelectedTask()
	if !ok {
		t.Fatal("no selected task")
	}
	res, _ = m.updateBoardMode("enter")
	m = res.(Model)
	if m.mode != modeNote {
		t.Fatalf("expected modeNote, got %d", m.mode)
	}
	if m.note == nil || m.note.target.taskID != want.ID {
		t.Fatalf("note target = %+v, want task #%d", m.note, want.ID)
	}
	res, _ = m.updateNoteMode("enter")
	if res.(Model).mode != modeBoard {
		t.Fatalf("expected return to modeBoard, got %d", res.(Model).mode)
	}
}
