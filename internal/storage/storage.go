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
	Project            string
	Tags               string
	Due                sql.NullTime
	Start              sql.NullTime
	Priority           int
	Recurring          bool
	RecurrenceRule     string
	RecurrenceInterval int
	CreatedAt          time.Time
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
	project TEXT DEFAULT '',
	tags TEXT DEFAULT '',
	due TEXT DEFAULT NULL,
	start_at TEXT DEFAULT NULL,
	priority INTEGER NOT NULL DEFAULT 0,
	recurring INTEGER NOT NULL DEFAULT 0,
	recurrence_rule TEXT DEFAULT '',
	recurrence_interval INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(ddl); err != nil {
		return err
	}
	return s.ensureTaskColumns()
}

func (s *Store) ensureTaskColumns() error {
	required := map[string]string{
		"start_at":            "ALTER TABLE tasks ADD COLUMN start_at TEXT DEFAULT NULL;",
		"priority":            "ALTER TABLE tasks ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;",
		"recurring":           "ALTER TABLE tasks ADD COLUMN recurring INTEGER NOT NULL DEFAULT 0;",
		"recurrence_rule":     "ALTER TABLE tasks ADD COLUMN recurrence_rule TEXT DEFAULT '';",
		"recurrence_interval": "ALTER TABLE tasks ADD COLUMN recurrence_interval INTEGER NOT NULL DEFAULT 0;",
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

func (s *Store) FetchTasks() ([]Task, error) {
	rows, err := s.db.Query(`SELECT id, title, done, project, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, created_at FROM tasks ORDER BY id;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
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
	if done {
		val = 1
	}
	_, err := s.db.Exec(`UPDATE tasks SET done = ? WHERE id = ?;`, val, id)
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
	}
	res, err := s.db.Exec(`DELETE FROM tasks WHERE done = 1;`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) RenameTopic(oldName, newName string) (int64, error) {
	res, err := s.db.Exec(`UPDATE tasks SET project = ? WHERE project = ?;`, newName, oldName)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) DeleteTopic(topic string) (int64, error) {
	tasks, err := s.fetchTasksByTopic(topic)
	if err != nil {
		return 0, err
	}
	if len(tasks) > 0 {
		if err := s.moveToTrash(tasks); err != nil {
			return 0, err
		}
	}
	res, err := s.db.Exec(`DELETE FROM tasks WHERE project = ?;`, topic)
	if err != nil {
		return 0, err
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

func (s *Store) UpdateTaskMetadata(id int, project, tags string, priority int, due, start sql.NullTime, recurring bool) error {
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
	_, err := s.db.Exec(`UPDATE tasks SET project = ?, tags = ?, priority = ?, due = ?, start_at = ?, recurring = ? WHERE id = ?;`,
		project, tags, priority, dueStr, startStr, rec, id)
	return err
}

func (s *Store) UpdateRecurrence(id int, rule string, interval int) error {
	_, err := s.db.Exec(`UPDATE tasks SET recurrence_rule = ?, recurrence_interval = ? WHERE id = ?;`, rule, interval, id)
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
		_, err := tx.Exec(`INSERT INTO tasks (title, done, project, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			task.Title, boolToInt(task.Done), task.Project, task.Tags, nullTimeToString(task.Due), nullTimeToString(task.Start), task.Priority, boolToInt(task.Recurring), task.RecurrenceRule, task.RecurrenceInterval, task.CreatedAt.Format(time.RFC3339))
		if err != nil {
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
	row := s.db.QueryRow(`SELECT id, title, done, project, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, created_at FROM tasks WHERE id = ?;`, id)
	return scanTask(row)
}

func (s *Store) fetchDoneTasks() ([]Task, error) {
	rows, err := s.db.Query(`SELECT id, title, done, project, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, created_at FROM tasks WHERE done = 1 ORDER BY id;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Store) fetchTasksByTopic(topic string) ([]Task, error) {
	rows, err := s.db.Query(`SELECT id, title, done, project, tags, due, start_at, priority, recurring, recurrence_rule, recurrence_interval, created_at FROM tasks WHERE project = ? ORDER BY id;`, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
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
	var dueStr, startStr sql.NullString
	var createdStr string

	if err := scanner.Scan(&t.ID, &t.Title, &doneInt, &t.Project, &t.Tags, &dueStr, &startStr, &priority, &recurring, &rule, &interval, &createdStr); err != nil {
		return Task{}, err
	}
	t.Done = doneInt == 1
	t.Priority = priority
	t.Recurring = recurring == 1
	if rule.Valid {
		t.RecurrenceRule = rule.String
	}
	t.RecurrenceInterval = interval
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
	return t, nil
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
