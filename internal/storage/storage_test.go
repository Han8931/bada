package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"), filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustAdd(t *testing.T, s *Store, title string) int {
	t.Helper()
	id, err := s.AddTask(title)
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	return id
}

func fetchOne(t *testing.T, s *Store, id int) Task {
	t.Helper()
	tasks, err := s.FetchTasks()
	if err != nil {
		t.Fatalf("FetchTasks: %v", err)
	}
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %d not found", id)
	return Task{}
}

func TestMetadataRoundTripKeepsLocalWallTime(t *testing.T) {
	s := newTestStore(t)
	id := mustAdd(t, s, "write report")

	due := time.Date(2026, 7, 8, 15, 4, 0, 0, time.Local)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	err := s.UpdateTaskMetadata(id, "work, home", "urgent", "han", "", "UTC+09:00", 2,
		sql.NullTime{Time: due, Valid: true}, sql.NullTime{Time: start, Valid: true}, sql.NullTime{}, false)
	if err != nil {
		t.Fatalf("UpdateTaskMetadata: %v", err)
	}

	task := fetchOne(t, s, id)
	if !task.Due.Valid || !task.Due.Time.Equal(due) {
		t.Errorf("due = %v, want %v", task.Due.Time, due)
	}
	if got := task.Due.Time.Format("2006-01-02 15:04"); got != "2026-07-08 15:04" {
		t.Errorf("due wall time = %q, want 2026-07-08 15:04", got)
	}
	if len(task.Topics) != 2 {
		t.Errorf("topics = %v, want [home work]", task.Topics)
	}
	if task.Priority != 2 || task.Tags != "urgent" || task.Assignee != "han" {
		t.Errorf("fields not round-tripped: %+v", task)
	}
}

func TestLegacyUTCDueReadsAtFaceValue(t *testing.T) {
	s := newTestStore(t)
	id := mustAdd(t, s, "legacy row")
	// Rows written by old builds stored the typed wall time labeled as UTC.
	if _, err := s.db.Exec(`UPDATE tasks SET due = '2026-07-08T15:04:00Z' WHERE id = ?;`, id); err != nil {
		t.Fatalf("seed legacy due: %v", err)
	}
	task := fetchOne(t, s, id)
	if got := task.Due.Time.Format("2006-01-02 15:04"); got != "2026-07-08 15:04" {
		t.Errorf("legacy due wall time = %q, want 2026-07-08 15:04", got)
	}
	if task.Due.Time.Location() != time.Local {
		t.Errorf("legacy due location = %v, want Local", task.Due.Time.Location())
	}
}

func TestLegacySchemaMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Build a pre-migration database by hand: no status/priority/... columns
	// and a single inline topic column.
	raw, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		done INTEGER NOT NULL DEFAULT 0,
		tags TEXT DEFAULT '',
		due TEXT DEFAULT NULL,
		notes TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		topic TEXT DEFAULT ''
	);`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO tasks (title, done, created_at, topic) VALUES
		('finished thing', 1, '2026-01-01T09:00:00Z', 'thesis'),
		('open thing', 0, '2026-01-02T09:00:00Z', '');`); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	s, err := Open(dbPath, filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatalf("Open (migration): %v", err)
	}
	defer s.Close()

	tasks, err := s.FetchTasks()
	if err != nil {
		t.Fatalf("FetchTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
	byTitle := map[string]Task{}
	for _, task := range tasks {
		byTitle[task.Title] = task
	}
	done := byTitle["finished thing"]
	if done.Status != "DONE" || !done.Done {
		t.Errorf("done row: status=%q done=%v, want DONE/true", done.Status, done.Done)
	}
	if len(done.Topics) != 1 || done.Topics[0] != "thesis" || done.PrimaryTopic != "thesis" {
		t.Errorf("legacy topic not migrated: topics=%v primary=%q", done.Topics, done.PrimaryTopic)
	}
	open := byTitle["open thing"]
	if open.Status != "PENDING" || open.Done {
		t.Errorf("open row: status=%q done=%v, want PENDING/false", open.Status, open.Done)
	}

	// The legacy column must be gone.
	rows, err := s.db.Query(`PRAGMA table_info(tasks);`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == "topic" {
			t.Error("legacy topic column still present after migration")
		}
	}
}

func TestSetStatusCompletedAt(t *testing.T) {
	s := newTestStore(t)
	id := mustAdd(t, s, "task")

	if err := s.SetStatus(id, "DONE", true); err != nil {
		t.Fatalf("SetStatus done: %v", err)
	}
	first := fetchOne(t, s, id)
	if !first.CompletedAt.Valid {
		t.Fatal("completed_at not set on completion")
	}

	// Re-completing must preserve the original completion time.
	time.Sleep(1100 * time.Millisecond)
	if err := s.SetStatus(id, "DONE", true); err != nil {
		t.Fatalf("SetStatus done again: %v", err)
	}
	second := fetchOne(t, s, id)
	if !second.CompletedAt.Time.Equal(first.CompletedAt.Time) {
		t.Errorf("completed_at changed on re-complete: %v → %v", first.CompletedAt.Time, second.CompletedAt.Time)
	}

	// Leaving done clears it.
	if err := s.SetStatus(id, "PENDING", false); err != nil {
		t.Fatalf("SetStatus pending: %v", err)
	}
	reopened := fetchOne(t, s, id)
	if reopened.CompletedAt.Valid {
		t.Error("completed_at not cleared when leaving done")
	}
}

func TestRenameTopicCarriesWorkflowNotesAndPrimary(t *testing.T) {
	s := newTestStore(t)
	id := mustAdd(t, s, "chapter 1")
	if err := s.UpdateTaskMetadata(id, "old", "", "", "", "", 0, sql.NullTime{}, sql.NullTime{}, sql.NullTime{}, false); err != nil {
		t.Fatalf("UpdateTaskMetadata: %v", err)
	}
	if err := s.SetPrimaryTopic(id, "old"); err != nil {
		t.Fatalf("SetPrimaryTopic: %v", err)
	}
	if err := s.SetTopicWorkflow("old", []Stage{{Name: "writing", Category: StageActive}, {Name: "submitted", Category: StageDone}}); err != nil {
		t.Fatalf("SetTopicWorkflow: %v", err)
	}
	if err := s.UpdateTopicNote("old", "old note"); err != nil {
		t.Fatalf("UpdateTopicNote(old): %v", err)
	}
	if err := s.UpdateTopicNote("new", "existing note"); err != nil {
		t.Fatalf("UpdateTopicNote(new): %v", err)
	}

	if _, err := s.RenameTopic("old", "new"); err != nil {
		t.Fatalf("RenameTopic: %v", err)
	}

	task := fetchOne(t, s, id)
	if len(task.Topics) != 1 || task.Topics[0] != "new" || task.PrimaryTopic != "new" {
		t.Errorf("topics after rename = %v primary=%q, want [new]/new", task.Topics, task.PrimaryTopic)
	}
	stages, err := s.TopicWorkflow("new")
	if err != nil || len(stages) != 2 || stages[0].Name != "writing" {
		t.Errorf("workflow after rename = %v (err %v), want carried stages", stages, err)
	}
	if leftover, _ := s.TopicWorkflow("old"); len(leftover) != 0 {
		t.Errorf("old topic still has workflow: %v", leftover)
	}
	note, err := s.TopicNote("new")
	if err != nil {
		t.Fatalf("TopicNote: %v", err)
	}
	if note != "existing note\n\n---\n\nold note" {
		t.Errorf("merged note = %q", note)
	}
}

func TestTrashRoundTrip(t *testing.T) {
	s := newTestStore(t)
	id := mustAdd(t, s, "doomed")
	due := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	if err := s.UpdateTaskMetadata(id, "errands", "", "", "", "", 1, sql.NullTime{Time: due, Valid: true}, sql.NullTime{}, sql.NullTime{}, false); err != nil {
		t.Fatalf("UpdateTaskMetadata: %v", err)
	}

	if err := s.DeleteTask(id); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if tasks, _ := s.FetchTasks(); len(tasks) != 0 {
		t.Fatalf("tasks after delete = %d, want 0", len(tasks))
	}

	entries, err := s.ListTrash()
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("trash entries = %d, want 1", len(entries))
	}
	if err := s.RestoreTrash(entries); err != nil {
		t.Fatalf("RestoreTrash: %v", err)
	}

	tasks, err := s.FetchTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after restore = %d (err %v), want 1", len(tasks), err)
	}
	got := tasks[0]
	if got.Title != "doomed" || got.Priority != 1 {
		t.Errorf("restored task = %+v", got)
	}
	if len(got.Topics) != 1 || got.Topics[0] != "errands" {
		t.Errorf("restored topics = %v, want [errands]", got.Topics)
	}
	if !got.Due.Valid || got.Due.Time.Format("2006-01-02") != "2026-08-01" {
		t.Errorf("restored due = %v, want 2026-08-01", got.Due)
	}
	if entries, _ := s.ListTrash(); len(entries) != 0 {
		t.Error("trash file not removed after restore")
	}
}

func TestDeleteDoneTasksTrashesExactlyTheDoneOnes(t *testing.T) {
	s := newTestStore(t)
	doneA := mustAdd(t, s, "done a")
	doneB := mustAdd(t, s, "done b")
	keep := mustAdd(t, s, "keep me")
	for _, id := range []int{doneA, doneB} {
		if err := s.SetStatus(id, "DONE", true); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
	}

	n, err := s.DeleteDoneTasks()
	if err != nil {
		t.Fatalf("DeleteDoneTasks: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}
	tasks, _ := s.FetchTasks()
	if len(tasks) != 1 || tasks[0].ID != keep {
		t.Errorf("remaining tasks = %v, want only #%d", tasks, keep)
	}
	entries, _ := s.ListTrash()
	if len(entries) != 2 {
		t.Errorf("trash entries = %d, want 2", len(entries))
	}
}

func TestNormalizeTaskStatus(t *testing.T) {
	cases := []struct {
		status string
		done   bool
		want   string
	}{
		{"", false, "PENDING"},
		{"", true, "DONE"},
		{"pending", false, "PENDING"},
		{"in_progress", false, "IN-PROGRESS"},
		{"Done", false, "DONE"},
		{"rebuttal", true, "rebuttal"}, // custom stage names pass through
		{"  writing  ", false, "writing"},
	}
	for _, c := range cases {
		if got := normalizeTaskStatus(c.status, c.done); got != c.want {
			t.Errorf("normalizeTaskStatus(%q, %v) = %q, want %q", c.status, c.done, got, c.want)
		}
	}
}

func TestShiftDueReturnsNewTime(t *testing.T) {
	s := newTestStore(t)
	id := mustAdd(t, s, "task")
	due := time.Date(2026, 7, 8, 0, 0, 0, 0, time.Local)
	if err := s.UpdateTaskMetadata(id, "", "", "", "", "", 0, sql.NullTime{Time: due, Valid: true}, sql.NullTime{}, sql.NullTime{}, false); err != nil {
		t.Fatalf("UpdateTaskMetadata: %v", err)
	}
	got, err := s.ShiftDue(id, 3)
	if err != nil {
		t.Fatalf("ShiftDue: %v", err)
	}
	want := due.AddDate(0, 0, 3)
	if !got.Equal(want) {
		t.Errorf("ShiftDue returned %v, want %v", got, want)
	}
	task := fetchOne(t, s, id)
	if !task.Due.Time.Equal(want) {
		t.Errorf("stored due = %v, want %v", task.Due.Time, want)
	}
}
