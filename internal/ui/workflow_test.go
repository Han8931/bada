package ui

import (
	"path/filepath"
	"testing"

	"bada/internal/storage"
)

func newWorkflowStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "bada.db"), filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestWorkflowRoundTrip verifies stage order and category survive a save/load.
func TestWorkflowRoundTrip(t *testing.T) {
	store := newWorkflowStore(t)
	stages := []storage.Stage{
		{Name: "writing", Category: storage.StagePending},
		{Name: "review", Category: storage.StageActive},
		{Name: "submission", Category: storage.StageActive},
		{Name: "rebuttal", Category: storage.StageDone},
	}
	if err := store.SetTopicWorkflow("thesis", stages); err != nil {
		t.Fatal(err)
	}
	got, err := store.TopicWorkflow("thesis")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(stages) {
		t.Fatalf("got %d stages, want %d", len(got), len(stages))
	}
	for i := range stages {
		if got[i] != stages[i] {
			t.Errorf("stage %d = %+v, want %+v", i, got[i], stages[i])
		}
	}
}

// TestSetStatusDoneFlag verifies that the explicit done flag drives the done
// column and completed_at, independent of the literal status string.
func TestSetStatusDoneFlag(t *testing.T) {
	store := newWorkflowStore(t)
	id, err := store.AddTask("write chapter")
	if err != nil {
		t.Fatal(err)
	}
	// Custom done-stage status, marked done.
	if err := store.SetStatus(id, "rebuttal", true); err != nil {
		t.Fatal(err)
	}
	tasks, _ := store.FetchTasks()
	got := findTask(tasks, id)
	if got.Status != "rebuttal" {
		t.Errorf("status = %q, want rebuttal", got.Status)
	}
	if !got.Done {
		t.Error("expected Done=true")
	}
	if !got.CompletedAt.Valid {
		t.Error("expected completed_at set")
	}
	// Rotate back to a non-done custom stage; done + completed_at clear.
	if err := store.SetStatus(id, "writing", false); err != nil {
		t.Fatal(err)
	}
	tasks, _ = store.FetchTasks()
	got = findTask(tasks, id)
	if got.Done {
		t.Error("expected Done=false after leaving done stage")
	}
	if got.CompletedAt.Valid {
		t.Error("expected completed_at cleared")
	}
}

// TestNormalizeStatusPreservesCustom checks legacy values still normalize while
// custom stage names round-trip unchanged.
func TestNormalizeStatusPreservesCustom(t *testing.T) {
	store := newWorkflowStore(t)
	id, _ := store.AddTask("t")
	cases := []struct{ in, want string }{
		{"done", "DONE"},
		{"in_progress", "IN-PROGRESS"},
		{"pending", "PENDING"},
		{"writing", "writing"},
		{"Needs Review", "Needs Review"},
	}
	for _, c := range cases {
		if err := store.SetStatus(id, c.in, false); err != nil {
			t.Fatal(err)
		}
		tasks, _ := store.FetchTasks()
		if got := findTask(tasks, id).Status; got != c.want {
			t.Errorf("SetStatus(%q) stored %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRenameTopicCascades verifies a topic rename carries its workflow and
// re-points primary_topic.
func TestRenameTopicCascades(t *testing.T) {
	store := newWorkflowStore(t)
	store.SetTopicWorkflow("old", []storage.Stage{{Name: "a", Category: storage.StageActive}})
	id, _ := store.AddTask("t")
	store.SetPrimaryTopic(id, "old")

	if _, err := store.RenameTopic("old", "new"); err != nil {
		t.Fatal(err)
	}
	wf, _ := store.TopicWorkflow("new")
	if len(wf) != 1 || wf[0].Name != "a" {
		t.Errorf("workflow not carried to new topic: %+v", wf)
	}
	if old, _ := store.TopicWorkflow("old"); len(old) != 0 {
		t.Errorf("old workflow not cleared: %+v", old)
	}
	tasks, _ := store.FetchTasks()
	if pt := findTask(tasks, id).PrimaryTopic; pt != "new" {
		t.Errorf("primary_topic = %q, want new", pt)
	}
}

// TestDeleteTopicCascades verifies delete clears workflow and primary_topic.
func TestDeleteTopicCascades(t *testing.T) {
	store := newWorkflowStore(t)
	store.SetTopicWorkflow("proj", []storage.Stage{{Name: "a", Category: storage.StageActive}})
	id, _ := store.AddTask("t")
	store.SetPrimaryTopic(id, "proj")

	if _, err := store.DeleteTopic("proj"); err != nil {
		t.Fatal(err)
	}
	if wf, _ := store.TopicWorkflow("proj"); len(wf) != 0 {
		t.Errorf("workflow not deleted: %+v", wf)
	}
	tasks, _ := store.FetchTasks()
	if pt := findTask(tasks, id).PrimaryTopic; pt != "" {
		t.Errorf("primary_topic = %q, want empty", pt)
	}
}

// TestTopicMetaRoundTrip checks description/target/archived persist.
func TestTopicMetaRoundTrip(t *testing.T) {
	store := newWorkflowStore(t)
	target, _ := parseDate("2026-09-01")
	if err := store.UpdateTopicMeta("proj", "my project", target, true); err != nil {
		t.Fatal(err)
	}
	meta, err := store.TopicMeta("proj")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "my project" {
		t.Errorf("description = %q", meta.Description)
	}
	if !meta.Archived {
		t.Error("expected archived")
	}
	if !meta.TargetDate.Valid || meta.TargetDate.Time.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("target = %+v", meta.TargetDate)
	}
}

// --- governing workflow / rotation (UI layer) ---

func wfModel(t *testing.T) Model {
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
	return m
}

func TestGoverningWorkflowByPrimaryTopic(t *testing.T) {
	m := wfModel(t)
	governed := storage.Task{PrimaryTopic: "thesis", Topics: []string{"thesis"}, Status: "writing"}
	if _, ok := m.governingWorkflow(governed); !ok {
		t.Error("expected governing workflow for primary topic")
	}
	// Topic present but not primary → no workflow governs.
	label := storage.Task{PrimaryTopic: "", Topics: []string{"thesis"}, Status: "PENDING"}
	if _, ok := m.governingWorkflow(label); ok {
		t.Error("did not expect workflow when topic is not primary")
	}
}

func TestNextTaskStatusCycles(t *testing.T) {
	m := wfModel(t)
	steps := []struct{ from, want string }{
		{"writing", "review"},
		{"review", "rebuttal"},
		{"rebuttal", "writing"}, // wraps
		{"unknown-legacy", "writing"},
	}
	for _, s := range steps {
		task := storage.Task{PrimaryTopic: "thesis", Status: s.from}
		if got := m.nextTaskStatus(task); got != s.want {
			t.Errorf("next(%q) = %q, want %q", s.from, got, s.want)
		}
	}
	// No workflow → legacy cycle.
	legacy := storage.Task{Status: "PENDING"}
	if got := m.nextTaskStatus(legacy); got != "IN-PROGRESS" {
		t.Errorf("legacy next = %q, want IN-PROGRESS", got)
	}
}

func TestStatusMeansDone(t *testing.T) {
	m := wfModel(t)
	done := storage.Task{PrimaryTopic: "thesis", Status: "writing"}
	if !m.statusMeansDone(done, "rebuttal") {
		t.Error("rebuttal is the done stage")
	}
	if m.statusMeansDone(done, "review") {
		t.Error("review is not a done stage")
	}
	// Legacy.
	if !m.statusMeansDone(storage.Task{}, "DONE") {
		t.Error("legacy DONE means done")
	}
}

func TestTaskLabelUnknownMapsToInitial(t *testing.T) {
	m := wfModel(t)
	task := storage.Task{PrimaryTopic: "thesis", Status: "PENDING"} // not a stage name
	if got := m.taskStatusLabel(task); got != "writing" {
		t.Errorf("unknown status label = %q, want initial stage writing", got)
	}
}

func TestTopicStageStats(t *testing.T) {
	m := wfModel(t)
	// Seed tasks with this primary topic at various stages.
	m.tasks = []storage.Task{
		{ID: 1, PrimaryTopic: "thesis", Status: "writing"},
		{ID: 2, PrimaryTopic: "thesis", Status: "writing"},
		{ID: 3, PrimaryTopic: "thesis", Status: "rebuttal"},
		{ID: 4, PrimaryTopic: "other", Status: "writing"}, // different primary, excluded
	}
	counts := m.topicStageStats("thesis")
	if len(counts) != 3 {
		t.Fatalf("got %d stage buckets, want 3", len(counts))
	}
	if counts[0].Stage.Name != "writing" || counts[0].Count != 2 {
		t.Errorf("writing bucket = %+v, want count 2", counts[0])
	}
	if counts[2].Stage.Name != "rebuttal" || counts[2].Count != 1 {
		t.Errorf("rebuttal bucket = %+v, want count 1", counts[2])
	}
}

func findTask(tasks []storage.Task, id int) storage.Task {
	for _, t := range tasks {
		if t.ID == id {
			return t
		}
	}
	return storage.Task{}
}

func TestPriorityThreeLevels(t *testing.T) {
	if priorityLabel(0) != "" || priorityLabel(1) != "Low" || priorityLabel(2) != "Med" || priorityLabel(3) != "High" {
		t.Errorf("labels: %q %q %q %q", priorityLabel(0), priorityLabel(1), priorityLabel(2), priorityLabel(3))
	}
	// Over-cap clamps to High.
	if got, _ := parsePriority("9"); got != maxPriority {
		t.Errorf("parsePriority(9) = %d, want %d", got, maxPriority)
	}
}

func TestUpdatePriorityClampsToThree(t *testing.T) {
	store := newWorkflowStore(t)
	id, _ := store.AddTask("t")
	if err := store.UpdatePriority(id, 5); err != nil {
		t.Fatal(err)
	}
	tasks, _ := store.FetchTasks()
	if p := findTask(tasks, id).Priority; p != 3 {
		t.Errorf("priority clamped to %d, want 3", p)
	}
}
