package storage

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Task struct {
	ID        int
	Title     string
	Done      bool
	Project   string
	Tags      string
	Due       sql.NullTime
	Start     sql.NullTime
	Priority  int
	Recurring bool
	CreatedAt time.Time
}

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("db path is empty")
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

	s := &Store{db: db}
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
	created_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(ddl); err != nil {
		return err
	}
	return s.ensureTaskColumns()
}

func (s *Store) ensureTaskColumns() error {
	required := map[string]string{
		"start_at":  "ALTER TABLE tasks ADD COLUMN start_at TEXT DEFAULT NULL;",
		"priority":  "ALTER TABLE tasks ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;",
		"recurring": "ALTER TABLE tasks ADD COLUMN recurring INTEGER NOT NULL DEFAULT 0;",
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
	rows, err := s.db.Query(`SELECT id, title, done, project, tags, due, start_at, priority, recurring, created_at FROM tasks ORDER BY id;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var doneInt, priority, recurring int
		var dueStr, startStr sql.NullString
		var createdStr string

		if err := rows.Scan(&t.ID, &t.Title, &doneInt, &t.Project, &t.Tags, &dueStr, &startStr, &priority, &recurring, &createdStr); err != nil {
			return nil, err
		}
		t.Done = doneInt == 1
		t.Priority = priority
		t.Recurring = recurring == 1
		if dueStr.Valid {
			if parsed, err := time.Parse(time.RFC3339, dueStr.String); err == nil {
				t.Due = sql.NullTime{Time: parsed, Valid: true}
			}
		}
		if startStr.Valid {
			if parsed, err := time.Parse(time.RFC3339, startStr.String); err == nil {
				t.Start = sql.NullTime{Time: parsed, Valid: true}
			}
		}
		if created, err := time.Parse(time.RFC3339, createdStr); err == nil {
			t.CreatedAt = created
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
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?;`, id)
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
