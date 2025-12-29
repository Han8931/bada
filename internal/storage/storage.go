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
	Done               bool
	Topics             []string
	Tags               string
	Due                sql.NullTime
	Start              sql.NullTime
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

type TrashEntry struct {
	Path      string
	DeletedAt time.Time
	Task      Task
}

type rowScanner interface {
	Scan(dest ...any) error
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
	tags TEXT DEFAULT '',
	due TEXT DEFAULT NULL,
	start_at TEXT DEFAULT NULL,
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
	if err := s.dropLegacyTopicColumn(); err != nil {
		return err
	}
	return s.ensureTopicNoteColumns()
}

func (s *Store) ensureTaskColumns() error {
	required := map[string]string{
		"start_at":            "ALTER TABLE tasks ADD COLUMN start_at TEXT DEFAULT NULL;",
		"priority":            "ALTER TABLE tasks ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;",
		"recurring":           "ALTER TABLE tasks ADD COLUMN recurring INTEGER NOT NULL DEFAULT 0;",
		"recurrence_rule":     "ALTER TABLE tasks ADD COLUMN recurrence_rule TEXT DEFAULT '';",
		"recurrence_interval": "ALTER TABLE tasks ADD COLUMN recurrence_interval INTEGER NOT NULL DEFAULT 0;",
		"completed_at":        "ALTER TABLE tasks ADD COLUMN completed_at TEXT DEFAULT NULL;",
		"notes":               "ALTER TABLE tasks ADD COLUMN notes TEXT DEFAULT '';",
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
	defer rows.Close()
	hasTopic := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "topic" {
			hasTopic = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasTopic {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE tasks DROP COLUMN topic;`)
	return err
}

func (s *Store) ensureTopicNoteColumns() error {
	required := map[string]string{
		"notes": "ALTER TABLE topic_notes ADD COLUMN notes TEXT NOT NULL DEFAULT '';",
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
	rows, err := s.db.Query(`SELECT id, title, done, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, notes, created_at, completed_at FROM tasks ORDER BY id;`)
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

func (s *Store) AddTask(title string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT INTO tasks (title, done, created_at) VALUES (?, 0, ?);`, title, now)
	return err
}

func (s *Store) SetDone(id int, done bool) error {
	val := 0
	completed := sql.NullString{}
	if done {
		val = 1
		completed = sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true}
	}
	_, err := s.db.Exec(`UPDATE tasks SET done = ?, completed_at = ? WHERE id = ?;`, val, completed, id)
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
	if _, err := s.db.Exec(`DELETE FROM task_topics WHERE task_id = ?;`, id); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM tasks WHERE id = ?;`, id)
	return err
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
		for _, task := range doneTasks {
			if _, err := s.db.Exec(`DELETE FROM task_topics WHERE task_id = ?;`, task.ID); err != nil {
				return 0, err
			}
		}
	}
	res, err := s.db.Exec(`DELETE FROM tasks WHERE done = 1;`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if err := s.renameTopicNote(oldName, newName); err != nil {
		rows, _ := res.RowsAffected()
		return rows, err
	}
	return res.RowsAffected()
}

func (s *Store) DeleteTopic(topic string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM task_topics WHERE topic = ?;`, topic)
	if err != nil {
		return 0, err
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

func (s *Store) UpdatePriority(id int, priority int) error {
	if priority < 0 {
		priority = 0
	}
	if priority > 5 {
		priority = 5
	}
	_, err := s.db.Exec(`UPDATE tasks SET priority = ? WHERE id = ?;`, priority, id)
	return err
}

func (s *Store) ShiftDue(id int, days int) error {
	var current sql.NullString
	err := s.db.QueryRow(`SELECT due FROM tasks WHERE id = ?;`, id).Scan(&current)
	if err != nil {
		return err
	}
	var base time.Time
	if current.Valid {
		base = parseTimeWithFallback(current.String)
	} else {
		base = time.Now().UTC()
	}
	newTime := base.AddDate(0, 0, days)
	newStr := sql.NullString{String: newTime.UTC().Format(time.RFC3339), Valid: true}
	_, err = s.db.Exec(`UPDATE tasks SET due = ? WHERE id = ?;`, newStr, id)
	return err
}

func (s *Store) UpdateTaskMetadata(id int, topic, tags string, priority int, due, start sql.NullTime, recurring bool) error {
	dueStr := sql.NullString{}
	if due.Valid {
		dueStr = sql.NullString{String: due.Time.UTC().Format(time.RFC3339), Valid: true}
	}
	startStr := sql.NullString{}
	if start.Valid {
		startStr = sql.NullString{String: start.Time.UTC().Format(time.RFC3339), Valid: true}
	}
	rec := 0
	if recurring {
		rec = 1
	}
	topics := splitTopics(topic)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE tasks SET tags = ?, priority = ?, due = ?, start_at = ?, recurring = ? WHERE id = ?;`,
		tags, priority, dueStr, startStr, rec, id)
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
		res, err := tx.Exec(`INSERT INTO tasks (title, done, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, notes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			task.Title, boolToInt(task.Done), task.Tags, nullTimeToString(task.Due), nullTimeToString(task.Start), task.Priority, boolToInt(task.Recurring), task.RecurrenceRule, task.RecurrenceInterval, task.Notes, task.CreatedAt.Format(time.RFC3339))
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
	row := s.db.QueryRow(`SELECT id, title, done, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, notes, created_at, completed_at FROM tasks WHERE id = ?;`, id)
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
	rows, err := s.db.Query(`SELECT id, title, done, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, notes, created_at, completed_at FROM tasks WHERE done = 1 ORDER BY id;`)
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

func (s *Store) fetchTasksByTopic(topic string) ([]Task, error) {
	rows, err := s.db.Query(`SELECT DISTINCT tasks.id, tasks.title, tasks.done, tasks.tags, tasks.due, tasks.start_at, tasks.priority,
tasks.recurring, tasks.recurrence_rule, tasks.recurrence_interval, tasks.notes, tasks.created_at, tasks.completed_at
FROM tasks
INNER JOIN task_topics ON tasks.id = task_topics.task_id
WHERE task_topics.topic = ?
ORDER BY tasks.id;`, topic)
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

func (s *Store) setTaskTopics(id int, topics []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := s.setTaskTopicsTx(tx, id, topics); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
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
	var notes sql.NullString
	var dueStr, startStr, completedStr sql.NullString
	var createdStr string

	if err := scanner.Scan(&t.ID, &t.Title, &doneInt, &t.Tags, &dueStr, &startStr, &priority, &recurring, &rule, &interval, &notes, &createdStr, &completedStr); err != nil {
		return Task{}, err
	}
	t.Done = doneInt == 1
	t.Priority = priority
	t.Recurring = recurring == 1
	if rule.Valid {
		t.RecurrenceRule = rule.String
	}
	t.RecurrenceInterval = interval
	if notes.Valid {
		t.Notes = notes.String
	}
	if dueStr.Valid {
		parsed := parseTimeWithFallback(dueStr.String)
		if !parsed.IsZero() {
			t.Due = sql.NullTime{Time: parsed, Valid: true}
		}
	}
	if startStr.Valid {
		parsed := parseTimeWithFallback(startStr.String)
		if !parsed.IsZero() {
			t.Start = sql.NullTime{Time: parsed, Valid: true}
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

func nullTimeToString(t sql.NullTime) sql.NullString {
	if !t.Valid {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Time.UTC().Format(time.RFC3339), Valid: true}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

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
