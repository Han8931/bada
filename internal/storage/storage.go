package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Task struct {
	ID                 int
	Title              string
	Status             string
	Done               bool
	Topics             []string
	PrimaryTopic       string
	Timezone           string
	Tags               string
	Assignee           string
	Reporter           string
	Due                sql.NullTime
	Start              sql.NullTime
	End                sql.NullTime
	Priority           int
	Recurring          bool
	RecurrenceRule     string
	RecurrenceInterval int
	Notes              string
	CreatedAt          time.Time
	CompletedAt        sql.NullTime
}

type Store struct {
	db       *sql.DB
	trashDir string
}

// Stage is one step in a topic's custom status workflow. Category is one of
// "pending", "active", or "done" and drives both display color and whether a
// task entering the stage is considered complete.
type Stage struct {
	Name     string
	Category string
}

// TopicMeta holds project-level metadata for a topic (the per-topic notes plus
// description, an optional target date, and an archived flag).
type TopicMeta struct {
	Topic       string
	Description string
	TargetDate  sql.NullTime
	Archived    bool
	Notes       string
}

// Stage category constants.
const (
	StagePending = "pending"
	StageActive  = "active"
	StageDone    = "done"
)

// normalizeStageCategory clamps an arbitrary string to a known stage category,
// defaulting to "active".
func normalizeStageCategory(cat string) string {
	switch strings.ToLower(strings.TrimSpace(cat)) {
	case StagePending:
		return StagePending
	case StageDone:
		return StageDone
	default:
		return StageActive
	}
}

type TrashEntry struct {
	Path      string
	DeletedAt time.Time
	Task      Task
}

type rowScanner interface {
	Scan(dest ...any) error
}

func taskSelectSQL(suffix string) string {
	return `SELECT id, title, done, status, tags, assignee, reporter, due, start_at, end_at, timezone, priority, recurring, recurrence_rule, recurrence_interval, notes, primary_topic, created_at, completed_at FROM tasks ` + suffix
}

func Open(dbPath, trashDir string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("db path is empty")
	}
	if strings.TrimSpace(trashDir) == "" {
		trashDir = "trash"
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	dsn := sqliteDSN(dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	absTrash := trashDir
	if !filepath.IsAbs(absTrash) {
		if abs, err := filepath.Abs(trashDir); err == nil {
			absTrash = abs
		}
	}

	s := &Store{db: db, trashDir: absTrash}
	if err := s.ensureSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) ensureSchema() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS tasks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title TEXT NOT NULL,
	done INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'PENDING',
	tags TEXT DEFAULT '',
	assignee TEXT DEFAULT '',
	reporter TEXT DEFAULT '',
	due TEXT DEFAULT NULL,
	start_at TEXT DEFAULT NULL,
	end_at TEXT DEFAULT NULL,
	timezone TEXT DEFAULT '',
	priority INTEGER NOT NULL DEFAULT 0,
	recurring INTEGER NOT NULL DEFAULT 0,
	recurrence_rule TEXT DEFAULT '',
	recurrence_interval INTEGER NOT NULL DEFAULT 0,
	notes TEXT DEFAULT '',
	created_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(ddl); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS topic_notes (
	topic TEXT PRIMARY KEY,
	notes TEXT NOT NULL DEFAULT ''
);`); err != nil {
		return err
	}
	if err := s.ensureTaskColumns(); err != nil {
		return err
	}
	if err := s.ensureTaskTopics(); err != nil {
		return err
	}
	if err := s.ensureTopicStages(); err != nil {
		return err
	}
	if err := s.dropLegacyTopicColumn(); err != nil {
		return err
	}
	return s.ensureTopicNoteColumns()
}

func (s *Store) ensureTopicStages() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS topic_stages (
	topic TEXT NOT NULL,
	position INTEGER NOT NULL,
	name TEXT NOT NULL,
	category TEXT NOT NULL DEFAULT 'active',
	PRIMARY KEY (topic, position)
);`); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topic_stages_topic ON topic_stages(topic);`)
	return err
}

func (s *Store) ensureTaskColumns() error {
	required := map[string]string{
		"timezone":            "ALTER TABLE tasks ADD COLUMN timezone TEXT DEFAULT '';",
		"status":              "ALTER TABLE tasks ADD COLUMN status TEXT NOT NULL DEFAULT 'PENDING';",
		"assignee":            "ALTER TABLE tasks ADD COLUMN assignee TEXT DEFAULT '';",
		"reporter":            "ALTER TABLE tasks ADD COLUMN reporter TEXT DEFAULT '';",
		"start_at":            "ALTER TABLE tasks ADD COLUMN start_at TEXT DEFAULT NULL;",
		"end_at":              "ALTER TABLE tasks ADD COLUMN end_at TEXT DEFAULT NULL;",
		"priority":            "ALTER TABLE tasks ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;",
		"recurring":           "ALTER TABLE tasks ADD COLUMN recurring INTEGER NOT NULL DEFAULT 0;",
		"recurrence_rule":     "ALTER TABLE tasks ADD COLUMN recurrence_rule TEXT DEFAULT '';",
		"recurrence_interval": "ALTER TABLE tasks ADD COLUMN recurrence_interval INTEGER NOT NULL DEFAULT 0;",
		"completed_at":        "ALTER TABLE tasks ADD COLUMN completed_at TEXT DEFAULT NULL;",
		"notes":               "ALTER TABLE tasks ADD COLUMN notes TEXT DEFAULT '';",
		"primary_topic":       "ALTER TABLE tasks ADD COLUMN primary_topic TEXT DEFAULT '';",
	}
	existing := map[string]struct{}{}
	rows, err := s.db.Query(`PRAGMA table_info(tasks);`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	for col, alter := range required {
		if _, ok := existing[col]; ok {
			continue
		}
		if _, err := s.db.Exec(alter); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE tasks SET status = 'DONE' WHERE done = 1 AND (status = '' OR status = 'PENDING');`); err != nil {
		return err
	}
	// Priority is now a 3-level scale; clamp any legacy 4/5 values down to High.
	if _, err := s.db.Exec(`UPDATE tasks SET priority = ? WHERE priority > ?;`, maxPriority, maxPriority); err != nil {
		return err
	}
	return rows.Err()
}

func (s *Store) ensureTaskTopics() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS task_topics (
	task_id INTEGER NOT NULL,
	topic TEXT NOT NULL,
	PRIMARY KEY (task_id, topic)
);`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_task_topics_topic ON task_topics(topic);`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_task_topics_task_id ON task_topics(task_id);`); err != nil {
		return err
	}
	return nil
}

func (s *Store) dropLegacyTopicColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(tasks);`)
	if err != nil {
		return err
	}
	hasTopic := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "topic" {
			hasTopic = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	// Close before writing: the pool holds a single connection, so a write
	// issued while these rows are open would wait on it forever.
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasTopic {
		return nil
	}
	// Carry the single-topic values into task_topics (and primary_topic) before
	// dropping the column, so an old DB keeps its topic assignments.
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO task_topics (task_id, topic)
SELECT id, TRIM(topic) FROM tasks WHERE TRIM(COALESCE(topic, '')) != '';`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE tasks SET primary_topic = TRIM(topic)
WHERE TRIM(COALESCE(topic, '')) != '' AND TRIM(COALESCE(primary_topic, '')) = '';`); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE tasks DROP COLUMN topic;`)
	return err
}

func (s *Store) ensureTopicNoteColumns() error {
	required := map[string]string{
		"notes":       "ALTER TABLE topic_notes ADD COLUMN notes TEXT NOT NULL DEFAULT '';",
		"description": "ALTER TABLE topic_notes ADD COLUMN description TEXT DEFAULT '';",
		"target_date": "ALTER TABLE topic_notes ADD COLUMN target_date TEXT DEFAULT NULL;",
		"archived":    "ALTER TABLE topic_notes ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;",
	}
	existing := map[string]struct{}{}
	rows, err := s.db.Query(`PRAGMA table_info(topic_notes);`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	for col, alter := range required {
		if _, ok := existing[col]; ok {
			continue
		}
		if _, err := s.db.Exec(alter); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) FetchTasks() ([]Task, error) {
	rows, err := s.db.Query(taskSelectSQL(`ORDER BY id;`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	var ids []int
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
		ids = append(ids, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachTopics(tasks, ids); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Store) AddTask(title string) (int, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO tasks (title, done, status, created_at) VALUES (?, 0, 'PENDING', ?);`, title, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return int(id), nil
}

// SetStatus updates a task's status string and its done flag. The caller decides
// done-ness because, with custom per-topic workflows, whether a status means
// "complete" depends on the governing topic's stage category — knowledge that
// lives in the UI layer, not storage. completed_at is stamped only on the
// transition into done and cleared when leaving done.
func (s *Store) SetStatus(id int, status string, done bool) error {
	status = normalizeTaskStatus(status, done)
	var prevDone int
	var prevCompleted sql.NullString
	if err := s.db.QueryRow(`SELECT done, completed_at FROM tasks WHERE id = ?;`, id).Scan(&prevDone, &prevCompleted); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	completed := sql.NullString{}
	if done {
		if prevDone == 1 && prevCompleted.Valid {
			completed = prevCompleted // already done; preserve original completion time
		} else {
			completed = sql.NullString{String: time.Now().Format(time.RFC3339), Valid: true}
		}
	}
	_, err := s.db.Exec(`UPDATE tasks SET status = ?, done = ?, completed_at = ? WHERE id = ?;`, status, boolToInt(done), completed, id)
	return err
}

func (s *Store) DeleteTask(id int) error {
	task, err := s.fetchTaskByID(id)
	if err != nil {
		return err
	}
	if err := s.moveToTrash([]Task{task}); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM task_topics WHERE task_id = ?;`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ?;`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteDoneTasks() (int64, error) {
	doneTasks, err := s.fetchDoneTasks()
	if err != nil {
		return 0, err
	}
	if len(doneTasks) > 0 {
		if err := s.moveToTrash(doneTasks); err != nil {
			return 0, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	// Delete exactly the rows that were snapshotted to trash, never a broader
	// `WHERE done = 1`, so no task can be dropped without a trash copy.
	var rows int64
	for _, task := range doneTasks {
		if _, err := tx.Exec(`DELETE FROM task_topics WHERE task_id = ?;`, task.ID); err != nil {
			tx.Rollback()
			return 0, err
		}
		res, err := tx.Exec(`DELETE FROM tasks WHERE id = ?;`, task.ID)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		if n, err := res.RowsAffected(); err == nil {
			rows += n
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rows, nil
}

func (s *Store) RenameTopic(oldName, newName string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO task_topics (task_id, topic)
SELECT task_id, ? FROM task_topics WHERE topic = ?;`, newName, oldName)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM task_topics WHERE topic = ?;`, oldName)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	// Carry the custom workflow to the new name (keep any stages the new topic
	// already had), then drop the old rows.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO topic_stages (topic, position, name, category)
SELECT ?, position, name, category FROM topic_stages WHERE topic = ?;`, newName, oldName); err != nil {
		tx.Rollback()
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM topic_stages WHERE topic = ?;`, oldName); err != nil {
		tx.Rollback()
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE tasks SET primary_topic = ? WHERE primary_topic = ?;`, newName, oldName); err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if err := s.renameTopicMeta(oldName, newName); err != nil {
		rows, _ := res.RowsAffected()
		return rows, err
	}
	if err := s.renameTopicNote(oldName, newName); err != nil {
		rows, _ := res.RowsAffected()
		return rows, err
	}
	return res.RowsAffected()
}

// renameTopicMeta carries description/target/archived to the new topic name when
// the new name has no metadata of its own yet. Notes are handled separately by
// renameTopicNote (which merges them).
func (s *Store) renameTopicMeta(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || oldName == newName {
		return nil
	}
	oldMeta, err := s.TopicMeta(oldName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(oldMeta.Description) == "" && !oldMeta.TargetDate.Valid && !oldMeta.Archived {
		return nil
	}
	newMeta, err := s.TopicMeta(newName)
	if err != nil {
		return err
	}
	// Don't clobber metadata the destination topic already has.
	if strings.TrimSpace(newMeta.Description) == "" && !newMeta.TargetDate.Valid && !newMeta.Archived {
		return s.UpdateTopicMeta(newName, oldMeta.Description, oldMeta.TargetDate, oldMeta.Archived)
	}
	return nil
}

func (s *Store) DeleteTopic(topic string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM task_topics WHERE topic = ?;`, topic)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`UPDATE tasks SET primary_topic = '' WHERE primary_topic = ?;`, topic); err != nil {
		rows, _ := res.RowsAffected()
		return rows, err
	}
	if err := s.DeleteTopicWorkflow(topic); err != nil {
		rows, _ := res.RowsAffected()
		return rows, err
	}
	if err := s.DeleteTopicNote(topic); err != nil {
		rows, _ := res.RowsAffected()
		return rows, err
	}
	return res.RowsAffected()
}

func (s *Store) UpdateTitle(id int, title string) error {
	_, err := s.db.Exec(`UPDATE tasks SET title = ? WHERE id = ?;`, title, id)
	return err
}

// maxPriority is the highest priority level (3-level scale: 0 none, 1 Low,
// 2 Med, 3 High). Kept in sync with the UI's maxPriority.
const maxPriority = 3

func (s *Store) UpdatePriority(id int, priority int) error {
	if priority < 0 {
		priority = 0
	}
	if priority > maxPriority {
		priority = maxPriority
	}
	_, err := s.db.Exec(`UPDATE tasks SET priority = ? WHERE id = ?;`, priority, id)
	return err
}

// ShiftDue moves a task's due date by the given number of days (seeding from
// now when it has none) and returns the resulting due time, so callers don't
// re-derive it.
func (s *Store) ShiftDue(id int, days int) (time.Time, error) {
	var current sql.NullString
	err := s.db.QueryRow(`SELECT due FROM tasks WHERE id = ?;`, id).Scan(&current)
	if err != nil {
		return time.Time{}, err
	}
	var base time.Time
	if current.Valid {
		base = parseWallTime(current.String)
	}
	if base.IsZero() {
		base = time.Now()
	}
	newTime := base.AddDate(0, 0, days)
	newStr := sql.NullString{String: newTime.Format(time.RFC3339), Valid: true}
	_, err = s.db.Exec(`UPDATE tasks SET due = ? WHERE id = ?;`, newStr, id)
	if err != nil {
		return time.Time{}, err
	}
	return newTime, nil
}

func (s *Store) UpdateTaskMetadata(id int, topic, tags, assignee, reporter, timezone string, priority int, due, start, end sql.NullTime, recurring bool) error {
	dueStr := nullTimeToString(due)
	startStr := nullTimeToString(start)
	endStr := nullTimeToString(end)
	rec := 0
	if recurring {
		rec = 1
	}
	topics := splitTopics(topic)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE tasks SET tags = ?, assignee = ?, reporter = ?, timezone = ?, priority = ?, due = ?, start_at = ?, end_at = ?, recurring = ? WHERE id = ?;`,
		tags, assignee, reporter, timezone, priority, dueStr, startStr, endStr, rec, id)
	if err != nil {
		tx.Rollback()
		return err
	}
	if err := s.setTaskTopicsTx(tx, id, topics); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateRecurrence(id int, rule string, interval int) error {
	_, err := s.db.Exec(`UPDATE tasks SET recurrence_rule = ?, recurrence_interval = ? WHERE id = ?;`, rule, interval, id)
	return err
}

func (s *Store) UpdateTaskNotes(id int, notes string) error {
	_, err := s.db.Exec(`UPDATE tasks SET notes = ? WHERE id = ?;`, notes, id)
	return err
}

func (s *Store) TopicNote(topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", nil
	}
	var notes sql.NullString
	err := s.db.QueryRow(`SELECT notes FROM topic_notes WHERE topic = ?;`, topic).Scan(&notes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if notes.Valid {
		return notes.String, nil
	}
	return "", nil
}

func (s *Store) UpdateTopicNote(topic, notes string) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errors.New("topic is empty")
	}
	_, err := s.db.Exec(`INSERT INTO topic_notes (topic, notes) VALUES (?, ?) ON CONFLICT(topic) DO UPDATE SET notes = excluded.notes;`, topic, notes)
	return err
}

func (s *Store) DeleteTopicNote(topic string) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM topic_notes WHERE topic = ?;`, topic)
	return err
}

// TopicWorkflow returns the ordered stages defined for a topic, or an empty
// slice when the topic has no custom workflow.
func (s *Store) TopicWorkflow(topic string) ([]Stage, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT name, category FROM topic_stages WHERE topic = ? ORDER BY position;`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stages []Stage
	for rows.Next() {
		var st Stage
		if err := rows.Scan(&st.Name, &st.Category); err != nil {
			return nil, err
		}
		st.Category = normalizeStageCategory(st.Category)
		stages = append(stages, st)
	}
	return stages, rows.Err()
}

// AllTopicWorkflows loads every topic's workflow in a single query.
func (s *Store) AllTopicWorkflows() (map[string][]Stage, error) {
	rows, err := s.db.Query(`SELECT topic, name, category FROM topic_stages ORDER BY topic, position;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Stage{}
	for rows.Next() {
		var topic string
		var st Stage
		if err := rows.Scan(&topic, &st.Name, &st.Category); err != nil {
			return nil, err
		}
		st.Category = normalizeStageCategory(st.Category)
		out[topic] = append(out[topic], st)
	}
	return out, rows.Err()
}

// SetTopicWorkflow replaces a topic's workflow with the given ordered stages.
// Passing no stages clears the workflow.
func (s *Store) SetTopicWorkflow(topic string, stages []Stage) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errors.New("topic is empty")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := s.setTopicWorkflowTx(tx, topic, stages); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) setTopicWorkflowTx(tx *sql.Tx, topic string, stages []Stage) error {
	if _, err := tx.Exec(`DELETE FROM topic_stages WHERE topic = ?;`, topic); err != nil {
		return err
	}
	pos := 0
	for _, st := range stages {
		name := strings.TrimSpace(st.Name)
		if name == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO topic_stages (topic, position, name, category) VALUES (?, ?, ?, ?);`,
			topic, pos, name, normalizeStageCategory(st.Category)); err != nil {
			return err
		}
		pos++
	}
	return nil
}

// DeleteTopicWorkflow removes a topic's custom workflow.
func (s *Store) DeleteTopicWorkflow(topic string) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM topic_stages WHERE topic = ?;`, topic)
	return err
}

// TopicMeta returns the project-level metadata for a topic, with notes folded in.
func (s *Store) TopicMeta(topic string) (TopicMeta, error) {
	topic = strings.TrimSpace(topic)
	meta := TopicMeta{Topic: topic}
	if topic == "" {
		return meta, nil
	}
	var notes, desc, target sql.NullString
	var archived sql.NullInt64
	err := s.db.QueryRow(`SELECT notes, description, target_date, archived FROM topic_notes WHERE topic = ?;`, topic).
		Scan(&notes, &desc, &target, &archived)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return meta, nil
		}
		return meta, err
	}
	meta.Notes = notes.String
	meta.Description = desc.String
	meta.Archived = archived.Int64 == 1
	if target.Valid {
		if parsed := parseWallTime(target.String); !parsed.IsZero() {
			meta.TargetDate = sql.NullTime{Time: parsed, Valid: true}
		}
	}
	return meta, nil
}

// AllTopicMeta loads metadata for every topic that has a topic_notes row.
func (s *Store) AllTopicMeta() (map[string]TopicMeta, error) {
	rows, err := s.db.Query(`SELECT topic, notes, description, target_date, archived FROM topic_notes;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TopicMeta{}
	for rows.Next() {
		var topic string
		var notes, desc, target sql.NullString
		var archived sql.NullInt64
		if err := rows.Scan(&topic, &notes, &desc, &target, &archived); err != nil {
			return nil, err
		}
		meta := TopicMeta{Topic: topic, Notes: notes.String, Description: desc.String, Archived: archived.Int64 == 1}
		if target.Valid {
			if parsed := parseWallTime(target.String); !parsed.IsZero() {
				meta.TargetDate = sql.NullTime{Time: parsed, Valid: true}
			}
		}
		out[topic] = meta
	}
	return out, rows.Err()
}

// UpdateTopicMeta upserts the description, target date, and archived flag for a
// topic without disturbing its notes.
func (s *Store) UpdateTopicMeta(topic, description string, targetDate sql.NullTime, archived bool) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errors.New("topic is empty")
	}
	target := nullTimeToString(targetDate)
	_, err := s.db.Exec(`INSERT INTO topic_notes (topic, notes, description, target_date, archived) VALUES (?, '', ?, ?, ?)
ON CONFLICT(topic) DO UPDATE SET description = excluded.description, target_date = excluded.target_date, archived = excluded.archived;`,
		topic, description, target, boolToInt(archived))
	return err
}

// SetPrimaryTopic assigns the topic whose workflow governs a task's status.
func (s *Store) SetPrimaryTopic(taskID int, topic string) error {
	_, err := s.db.Exec(`UPDATE tasks SET primary_topic = ? WHERE id = ?;`, strings.TrimSpace(topic), taskID)
	return err
}

// OverwriteTask replaces every mutable field of an existing task row (including
// its topics) with the given snapshot. It's the basis for single-level undo:
// capture a Task before an edit, then OverwriteTask to revert. The row must
// still exist (deletes are reverted via the trash, not this method).
func (s *Store) OverwriteTask(t Task) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE tasks SET title = ?, done = ?, status = ?, tags = ?, assignee = ?, reporter = ?,
due = ?, start_at = ?, end_at = ?, timezone = ?, priority = ?, recurring = ?, recurrence_rule = ?,
recurrence_interval = ?, notes = ?, primary_topic = ?, completed_at = ? WHERE id = ?;`,
		t.Title, boolToInt(t.Done), normalizeTaskStatus(t.Status, t.Done), t.Tags, t.Assignee, t.Reporter,
		nullTimeToString(t.Due), nullTimeToString(t.Start), nullTimeToString(t.End), t.Timezone, t.Priority,
		boolToInt(t.Recurring), t.RecurrenceRule, t.RecurrenceInterval, t.Notes, t.PrimaryTopic,
		nullTimeToString(t.CompletedAt), t.ID)
	if err != nil {
		tx.Rollback()
		return err
	}
	if err := s.setTaskTopicsTx(tx, t.ID, t.Topics); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) ListTrash() ([]TrashEntry, error) {
	entries := []TrashEntry{}
	dirEntries, err := os.ReadDir(s.trashDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return entries, nil
		}
		return nil, err
	}
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		path := filepath.Join(s.trashDir, de.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var payload struct {
			DeletedAt time.Time `json:"deleted_at"`
			Task      Task      `json:"task"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		entries = append(entries, TrashEntry{
			Path:      path,
			DeletedAt: payload.DeletedAt,
			Task:      payload.Task,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].DeletedAt.After(entries[j].DeletedAt)
	})
	return entries, nil
}

func (s *Store) RestoreTrash(entries []TrashEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, e := range entries {
		task := e.Task
		// Trust the trashed task's own done flag: a custom done-stage status
		// (e.g. "rebuttal") is not literally "DONE" yet still completed.
		status := normalizeTaskStatus(task.Status, task.Done)
		res, err := tx.Exec(`INSERT INTO tasks (title, done, status, tags, assignee, reporter, due, start_at, end_at, timezone, priority, recurring, recurrence_rule, recurrence_interval, notes, primary_topic, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			task.Title, boolToInt(task.Done), status, task.Tags, task.Assignee, task.Reporter, nullTimeToString(task.Due), nullTimeToString(task.Start), nullTimeToString(task.End), task.Timezone, task.Priority, boolToInt(task.Recurring), task.RecurrenceRule, task.RecurrenceInterval, task.Notes, task.PrimaryTopic, task.CreatedAt.Format(time.RFC3339))
		if err != nil {
			tx.Rollback()
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := s.setTaskTopicsTx(tx, int(id), task.Topics); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.Remove(e.Path)
	}
	return nil
}

func (s *Store) TrashDir() string {
	return s.trashDir
}

func (s *Store) PurgeTrash(entries []TrashEntry) error {
	for _, e := range entries {
		if err := os.Remove(e.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) fetchTaskByID(id int) (Task, error) {
	row := s.db.QueryRow(taskSelectSQL(`WHERE id = ?;`), id)
	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	topics, err := s.fetchTopicsForTask(id)
	if err != nil {
		return Task{}, err
	}
	task.Topics = topics
	return task, nil
}

func (s *Store) fetchDoneTasks() ([]Task, error) {
	rows, err := s.db.Query(taskSelectSQL(`WHERE done = 1 ORDER BY id;`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	var ids []int
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
		ids = append(ids, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachTopics(tasks, ids); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Store) attachTopics(tasks []Task, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	topicMap, err := s.fetchTopicsForTasks(ids)
	if err != nil {
		return err
	}
	for i := range tasks {
		tasks[i].Topics = topicMap[tasks[i].ID]
	}
	return nil
}

func (s *Store) fetchTopicsForTask(id int) ([]string, error) {
	rows, err := s.db.Query(`SELECT topic FROM task_topics WHERE task_id = ? ORDER BY topic;`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var topics []string
	for rows.Next() {
		var topic string
		if err := rows.Scan(&topic); err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return topics, nil
}

func (s *Store) fetchTopicsForTasks(ids []int) (map[int][]string, error) {
	if len(ids) == 0 {
		return map[int][]string{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`SELECT task_id, topic FROM task_topics WHERE task_id IN (%s) ORDER BY topic;`, strings.Join(placeholders, ","))
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int][]string{}
	for rows.Next() {
		var taskID int
		var topic string
		if err := rows.Scan(&taskID, &topic); err != nil {
			return nil, err
		}
		m[taskID] = append(m[taskID], topic)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

func splitTopics(raw string) []string {
	parts := strings.Split(raw, ",")
	return normalizeTopics(parts)
}

func normalizeTopics(topics []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		if _, ok := seen[topic]; ok {
			continue
		}
		seen[topic] = struct{}{}
		out = append(out, topic)
	}
	return out
}

func (s *Store) setTaskTopicsTx(tx *sql.Tx, id int, topics []string) error {
	topics = normalizeTopics(topics)
	if _, err := tx.Exec(`DELETE FROM task_topics WHERE task_id = ?;`, id); err != nil {
		return err
	}
	for _, topic := range topics {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO task_topics (task_id, topic) VALUES (?, ?);`, id, topic); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) moveToTrash(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.trashDir, 0o755); err != nil {
		return err
	}
	now := time.Now().UTC()
	for i, t := range tasks {
		payload := struct {
			DeletedAt time.Time `json:"deleted_at"`
			Task      Task      `json:"task"`
		}{
			DeletedAt: now,
			Task:      t,
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		name := fmt.Sprintf("%s-%d-%d-%s.json", now.Format("20060102T150405Z"), t.ID, i, sanitizeFilename(t.Title))
		path := filepath.Join(s.trashDir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func scanTask(scanner rowScanner) (Task, error) {
	var t Task
	var doneInt, priority, recurring int
	var rule sql.NullString
	var interval int
	var status, notes, assignee, reporter, primaryTopic sql.NullString
	var dueStr, startStr, endStr, completedStr sql.NullString
	var createdStr string

	if err := scanner.Scan(&t.ID, &t.Title, &doneInt, &status, &t.Tags, &assignee, &reporter, &dueStr, &startStr, &endStr, &t.Timezone, &priority, &recurring, &rule, &interval, &notes, &primaryTopic, &createdStr, &completedStr); err != nil {
		return Task{}, err
	}
	if primaryTopic.Valid {
		t.PrimaryTopic = strings.TrimSpace(primaryTopic.String)
	}
	t.Done = doneInt == 1
	t.Status = normalizeTaskStatus(status.String, t.Done)
	t.Priority = priority
	t.Recurring = recurring == 1
	if rule.Valid {
		t.RecurrenceRule = rule.String
	}
	t.RecurrenceInterval = interval
	if notes.Valid {
		t.Notes = notes.String
	}
	if assignee.Valid {
		t.Assignee = assignee.String
	}
	if reporter.Valid {
		t.Reporter = reporter.String
	}
	if dueStr.Valid {
		parsed := parseWallTime(dueStr.String)
		if !parsed.IsZero() {
			t.Due = sql.NullTime{Time: parsed, Valid: true}
		}
	}
	if startStr.Valid {
		parsed := parseWallTime(startStr.String)
		if !parsed.IsZero() {
			t.Start = sql.NullTime{Time: parsed, Valid: true}
		}
	}
	if endStr.Valid {
		parsed := parseWallTime(endStr.String)
		if !parsed.IsZero() {
			t.End = sql.NullTime{Time: parsed, Valid: true}
		}
	}
	if created, err := time.Parse(time.RFC3339, createdStr); err == nil {
		t.CreatedAt = created
	}
	if completedStr.Valid {
		parsed := parseTimeWithFallback(completedStr.String)
		if !parsed.IsZero() {
			t.CompletedAt = sql.NullTime{Time: parsed, Valid: true}
		}
	}
	return t, nil
}

func normalizeTaskStatus(status string, done bool) string {
	trimmed := strings.TrimSpace(status)
	upper := strings.ReplaceAll(strings.ToUpper(trimmed), "_", "-")
	switch upper {
	case "DONE":
		return "DONE"
	case "IN-PROGRESS":
		return "IN-PROGRESS"
	case "PENDING":
		return "PENDING"
	case "":
		if done {
			return "DONE"
		}
		return "PENDING"
	default:
		// Preserve custom workflow stage names verbatim rather than collapsing
		// them to PENDING; they round-trip through scan and rotation.
		return trimmed
	}
}

func (s *Store) renameTopicNote(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || oldName == newName {
		return nil
	}
	oldNote, err := s.TopicNote(oldName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(oldNote) == "" {
		return s.DeleteTopicNote(oldName)
	}
	newNote, err := s.TopicNote(newName)
	if err != nil {
		return err
	}
	merged := mergeNotes(newNote, oldNote)
	if err := s.UpdateTopicNote(newName, merged); err != nil {
		return err
	}
	return s.DeleteTopicNote(oldName)
}

func mergeNotes(primary, extra string) string {
	if strings.TrimSpace(primary) == "" {
		return extra
	}
	if strings.TrimSpace(extra) == "" {
		return primary
	}
	return primary + "\n\n---\n\n" + extra
}

func sanitizeFilename(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	title = strings.ReplaceAll(title, " ", "-")

	var b strings.Builder
	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	res := b.String()
	if res == "" {
		res = "task"
	}
	if len(res) > 48 {
		res = res[:48]
	}
	return res
}

// nullTimeToString serializes a time as RFC3339 in its own location, so local
// wall-clock dates keep their face value and true instants keep their offset.
func nullTimeToString(t sql.NullTime) sql.NullString {
	if !t.Valid {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Time.Format(time.RFC3339), Valid: true}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// parseTimeWithFallback parses an RFC3339 (or bare-date) string as a true
// instant, preserving whatever offset it was stored with. Use it for event
// timestamps (created_at, completed_at).
func parseTimeWithFallback(val string) time.Time {
	if val == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", val); err == nil {
		return t
	}
	return time.Time{}
}

// parseWallTime parses a stored date at face value: whatever wall-clock time
// the string shows is rebuilt in the local timezone. Use it for user-facing
// dates (due, start, end, target) so rows written under another offset —
// including legacy rows stored as UTC — keep the date the user typed.
func parseWallTime(val string) time.Time {
	t := parseTimeWithFallback(val)
	if t.IsZero() {
		return t
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
}

func sqliteDSN(path string) string {
	if strings.HasPrefix(path, "file:") {
		return path
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	u := url.URL{
		Scheme: "file",
		Path:   path,
	}
	q := u.Query()
	q.Set("mode", "rwc")
	q.Set("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return u.String()
}
