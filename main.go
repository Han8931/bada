package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	toml "github.com/pelletier/go-toml/v2"
	_ "modernc.org/sqlite"
)

const (
	configFileName = "config.toml"
	defaultDBName  = "todo.db"
)

type mode int

const (
	modeList mode = iota
	modeAdd
	modeMetadata
)

type task struct {
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

type Keymap struct {
	Quit    string `toml:"quit"`
	Add     string `toml:"add"`
	Up      string `toml:"up"`
	Down    string `toml:"down"`
	Toggle  string `toml:"toggle"`
	Delete  string `toml:"delete"`
	Detail  string `toml:"detail"`
	Confirm string `toml:"confirm"`
	Cancel  string `toml:"cancel"`
	Edit    string `toml:"edit"`
}

type Config struct {
	DBPath        string `toml:"db_path"`
	DefaultFilter string `toml:"default_filter"`
	Keys          Keymap `toml:"keys"`
}

type model struct {
	db         *sql.DB
	cfg        Config
	tasks      []task
	cursor     int
	mode       mode
	input      textinput.Model
	status     string
	filterDone string
	confirmDel bool
	pendingDel *task
	meta       *metaState
}

type metaState struct {
	taskID    int
	project   string
	tags      string
	priority  string
	due       string
	start     string
	recurring string
	index     int
}

func main() {
	cfg, err := loadOrCreateConfig(configFileName)
	if err != nil {
		fmt.Printf("failed to load config: %v\n", err)
		os.Exit(1)
	}

	db, err := openDB(cfg.DBPath)
	if err != nil {
		fmt.Printf("failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := ensureSchema(db); err != nil {
		fmt.Printf("failed to migrate database: %v\n", err)
		os.Exit(1)
	}

	tasks, err := fetchTasks(db)
	if err != nil {
		fmt.Printf("failed to load tasks: %v\n", err)
		os.Exit(1)
	}

	ti := textinput.New()
	ti.Placeholder = "Task title"
	ti.CharLimit = 256
	ti.Width = 40

	m := model{
		db:         db,
		cfg:        cfg,
		tasks:      tasks,
		cursor:     clampCursor(0, len(tasks)),
		status:     "Press 'a' to add, space to toggle, 'd' to delete.",
		input:      ti,
		mode:       modeList,
		filterDone: strings.ToLower(cfg.DefaultFilter),
	}

	program := tea.NewProgram(m)
	if _, err := program.Run(); err != nil {
		fmt.Printf("error running program: %v\n", err)
		os.Exit(1)
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.meta != nil {
			return m.updateMetadataMode(msg.String(), msg)
		}
		if m.confirmDel {
			return m.updateDeleteConfirm(msg.String())
		}
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.input.Width = msg.Width - 10
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.mode == modeAdd {
		return m.updateAddMode(key, msg)
	}
	return m.updateListMode(key)
}

func (m model) updateAddMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel:
		m.mode = modeList
		m.input.SetValue("")
		m.status = "Cancelled"
		return m, nil
	case m.cfg.Keys.Confirm:
		title := strings.TrimSpace(m.input.Value())
		if title == "" {
			m.status = "Title cannot be empty"
			return m, nil
		}
		if err := addTask(m.db, title); err != nil {
			m.status = fmt.Sprintf("save failed: %v", err)
			return m, nil
		}
		var err error
		m.tasks, err = fetchTasks(m.db)
		if err != nil {
			m.status = fmt.Sprintf("reload failed: %v", err)
		} else {
			m.status = "Added task"
			m.cursor = clampCursor(len(m.tasks)-1, len(m.tasks))
		}
		m.input.SetValue("")
		m.input.Blur()
		m.mode = modeList
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m model) updateListMode(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c", m.cfg.Keys.Quit:
		return m, tea.Quit
	case m.cfg.Keys.Down:
		if len(m.tasks) == 0 {
			return m, nil
		}
		m.cursor = clampCursor(m.cursor+1, len(m.tasks))
	case m.cfg.Keys.Up:
		if m.cursor > 0 {
			m.cursor = clampCursor(m.cursor-1, len(m.tasks))
		}
	case m.cfg.Keys.Add:
		m.mode = modeAdd
		m.input.Focus()
		m.status = "Add mode: type a title and press Enter"
	case m.cfg.Keys.Toggle:
		if len(m.tasks) == 0 {
			return m, nil
		}
		task := m.tasks[m.cursor]
		err := setDone(m.db, task.ID, !task.Done)
		if err != nil {
			m.status = fmt.Sprintf("toggle failed: %v", err)
			return m, nil
		}
		m.tasks, err = fetchTasks(m.db)
		if err == nil {
			m.cursor = clampCursor(m.cursor+1, len(m.tasks))
			m.status = "Toggled task"
		} else {
			m.status = fmt.Sprintf("reload failed: %v", err)
		}
	case m.cfg.Keys.Delete:
		if len(m.tasks) == 0 {
			return m, nil
		}
		t := m.tasks[m.cursor]
		m.confirmDel = true
		m.pendingDel = &t
		m.status = fmt.Sprintf("Delete \"%s\"? y/n", t.Title)
	case m.cfg.Keys.Detail:
		if len(m.tasks) == 0 {
			m.status = "No tasks"
			return m, nil
		}
		task := m.tasks[m.cursor]
		info := fmt.Sprintf("Task #%d • %s • %s", task.ID, task.Title, humanDone(task.Done))
		if task.Project != "" {
			info += " • project:" + task.Project
		}
		if strings.TrimSpace(task.Tags) != "" {
			info += " • tags:" + task.Tags
		}
		if task.Priority != 0 {
			info += fmt.Sprintf(" • priority:%d", task.Priority)
		}
		if task.Due.Valid {
			info += " • due:" + task.Due.Time.Format("2006-01-02")
		}
		if task.Start.Valid {
			info += " • start:" + task.Start.Time.Format("2006-01-02")
		}
		if task.Recurring {
			info += " • recurring"
		}
		m.status = info
	case m.cfg.Keys.Edit:
		if len(m.tasks) == 0 {
			m.status = "No tasks to edit"
			return m, nil
		}
		return m.startMetadataEdit(m.tasks[m.cursor])
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	b.WriteString("Todo (Bubble Tea + SQLite)")
	b.WriteString("\n\n")

	if m.meta != nil {
		b.WriteString(fmt.Sprintf("Editing metadata: %s (field %d/6)\n\n", m.currentMetaLabel(), m.meta.index+1))
		b.WriteString(m.metaPrompt())
		b.WriteString("\n")
		b.WriteString(m.input.View())
		b.WriteString("\n\n")
		b.WriteString(m.status)
		return b.String()
	}

	if len(m.tasks) == 0 {
		b.WriteString("No tasks yet. Press 'a' to add one.")
	} else {
		for i, t := range m.tasks {
			cursor := " "
			if m.cursor == i && m.mode == modeList {
				cursor = ">"
			}

			checkbox := "[ ]"
			if t.Done {
				checkbox = "[x]"
			}

			extras := make([]string, 0, 3)
			if t.Project != "" {
				extras = append(extras, "P:"+t.Project)
			}
			if strings.TrimSpace(t.Tags) != "" {
				extras = append(extras, "T:"+t.Tags)
			}
			if t.Due.Valid {
				extras = append(extras, "D:"+t.Due.Time.Format("2006-01-02"))
			}
			if t.Start.Valid {
				extras = append(extras, "S:"+t.Start.Time.Format("2006-01-02"))
			}
			if t.Priority != 0 {
				extras = append(extras, fmt.Sprintf("Prio:%d", t.Priority))
			}
			if t.Recurring {
				extras = append(extras, "R")
			}

			body := fmt.Sprintf("%s %s %s", cursor, checkbox, t.Title)
			if len(extras) > 0 {
				body += " [" + strings.Join(extras, " | ") + "]"
			}

			b.WriteString(body)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if m.mode == modeAdd {
		b.WriteString("Add Task: ")
		b.WriteString(m.input.View())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.status)
	b.WriteString("\n")
	b.WriteString(renderHelp(m.cfg.Keys))

	return b.String()
}

func (m model) updateDeleteConfirm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "n", "N":
		m.status = "Delete cancelled"
		m.confirmDel = false
		m.pendingDel = nil
		return m, nil
	case "y", "Y":
		if m.pendingDel == nil {
			m.status = "Nothing to delete"
			m.confirmDel = false
			return m, nil
		}
		if err := deleteTask(m.db, m.pendingDel.ID); err != nil {
			m.status = fmt.Sprintf("delete failed: %v", err)
			m.confirmDel = false
			m.pendingDel = nil
			return m, nil
		}
		var err error
		m.tasks, err = fetchTasks(m.db)
		if err == nil {
			m.cursor = clampCursor(m.cursor, len(m.tasks))
			m.status = "Deleted task"
		} else {
			m.status = fmt.Sprintf("reload failed: %v", err)
		}
		m.confirmDel = false
		m.pendingDel = nil
		return m, nil
	default:
		return m, nil
	}
}

func renderHelp(k Keymap) string {
	return fmt.Sprintf("%s/%s move • %s add • %s/%s detail • %s toggle • %s delete • %s edit • %s quit",
		k.Up, k.Down, k.Add, k.Detail, k.Confirm, k.Toggle, k.Delete, k.Edit, k.Quit)
}

func (m model) startMetadataEdit(t task) (tea.Model, tea.Cmd) {
	m.meta = &metaState{
		taskID:    t.ID,
		project:   t.Project,
		tags:      t.Tags,
		priority:  fmt.Sprintf("%d", t.Priority),
		due:       formatDate(t.Due),
		start:     formatDate(t.Start),
		recurring: boolToYN(t.Recurring),
		index:     0,
	}
	m.input.SetValue(m.meta.currentValue())
	m.input.Placeholder = m.meta.currentLabel()
	m.input.Focus()
	m.mode = modeMetadata
	m.status = "Edit metadata: Enter to save field, Esc to cancel"
	return m, nil
}

func (m model) updateMetadataMode(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case m.cfg.Keys.Cancel, "esc":
		m.meta = nil
		m.mode = modeList
		m.input.Blur()
		m.status = "Edit cancelled"
		return m, nil
	case m.cfg.Keys.Confirm, "enter":
		if m.meta == nil {
			return m, nil
		}
		m.meta.setCurrentValue(m.input.Value())
		if m.meta.index >= len(metaFields())-1 {
			return m.saveMetadata()
		}
		m.meta.index++
		m.input.SetValue(m.meta.currentValue())
		m.input.Placeholder = m.meta.currentLabel()
		m.status = m.metaPrompt()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m model) saveMetadata() (tea.Model, tea.Cmd) {
	if m.meta == nil {
		return m, nil
	}
	taskID := m.meta.taskID
	priority, err := parsePriority(m.meta.priority)
	if err != nil {
		m.status = fmt.Sprintf("priority invalid: %v", err)
		return m, nil
	}
	due, err := parseDate(m.meta.due)
	if err != nil {
		m.status = fmt.Sprintf("due date invalid: %v", err)
		return m, nil
	}
	start, err := parseDate(m.meta.start)
	if err != nil {
		m.status = fmt.Sprintf("start date invalid: %v", err)
		return m, nil
	}
	recurring := parseYN(m.meta.recurring)

	err = updateTaskMetadata(m.db, m.meta.taskID, m.meta.project, m.meta.tags, priority, due, start, recurring)
	if err != nil {
		m.status = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.meta = nil
	m.mode = modeList
	m.input.Blur()

	m.tasks, err = fetchTasks(m.db)
	if err == nil {
		for i, t := range m.tasks {
			if t.ID == taskID {
				m.cursor = clampCursor(i, len(m.tasks))
				break
			}
		}
		m.status = "Metadata saved"
	} else {
		m.status = fmt.Sprintf("reload failed: %v", err)
	}
	return m, nil
}

func (m model) pendingID() int {
	if m.meta != nil {
		return m.meta.taskID
	}
	if m.pendingDel != nil {
		return m.pendingDel.ID
	}
	return 0
}

func metaFields() []string {
	return []string{"project", "tags", "priority", "due date (YYYY-MM-DD)", "start date (YYYY-MM-DD)", "recurring (y/n)"}
}

func (ms metaState) currentLabel() string {
	return metaFields()[ms.index]
}

func (ms metaState) currentValue() string {
	switch ms.index {
	case 0:
		return ms.project
	case 1:
		return ms.tags
	case 2:
		return ms.priority
	case 3:
		return ms.due
	case 4:
		return ms.start
	case 5:
		return ms.recurring
	default:
		return ""
	}
}

func (ms *metaState) setCurrentValue(v string) {
	switch ms.index {
	case 0:
		ms.project = v
	case 1:
		ms.tags = v
	case 2:
		ms.priority = v
	case 3:
		ms.due = v
	case 4:
		ms.start = v
	case 5:
		ms.recurring = v
	}
}

func (m model) metaPrompt() string {
	if m.meta == nil {
		return ""
	}
	return fmt.Sprintf("Editing %s (field %d of %d). Enter to advance, Esc to cancel.",
		m.meta.currentLabel(), m.meta.index+1, len(metaFields()))
}

func parsePriority(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	return strconv.Atoi(v)
}

func parseDate(v string) (sql.NullTime, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

func formatDate(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}

func parseYN(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "y" || v == "yes" || v == "true" || v == "1"
}

func boolToYN(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

func (m model) currentMetaLabel() string {
	if m.meta == nil {
		return ""
	}
	return m.meta.currentLabel()
}
func clampCursor(cur, n int) int {
	if n <= 0 {
		return 0
	}
	if cur < 0 {
		return 0
	}
	if cur >= n {
		return n - 1
	}
	return cur
}

func loadOrCreateConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeConfig(path, cfg); err != nil {
			return cfg, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.DBPath == "" {
		cfg.DBPath = defaultDBName
	}
	return cfg, nil
}

func writeConfig(path string, cfg Config) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func defaultConfig() Config {
	return Config{
		DBPath:        defaultDBName,
		DefaultFilter: "all",
		Keys: Keymap{
			Quit:    "q",
			Add:     "a",
			Up:      "k",
			Down:    "j",
			Toggle:  " ",
			Delete:  "d",
			Detail:  "enter",
			Confirm: "enter",
			Cancel:  "esc",
			Edit:    "e",
		},
	}
}

func openDB(dbPath string) (*sql.DB, error) {
	if dbPath == "" {
		dbPath = defaultDBName
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	// modernc.org/sqlite uses driver name "sqlite" and prefers a file: DSN.
	// mode=rwc creates the database file if it doesn't exist.
	dsn := sqliteDSN(dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
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

func ensureSchema(db *sql.DB) error {
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
	_, err := db.Exec(ddl)
	if err != nil {
		return err
	}
	return ensureTaskColumns(db)
}

func ensureTaskColumns(db *sql.DB) error {
	required := map[string]string{
		"start_at":  "ALTER TABLE tasks ADD COLUMN start_at TEXT DEFAULT NULL;",
		"priority":  "ALTER TABLE tasks ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;",
		"recurring": "ALTER TABLE tasks ADD COLUMN recurring INTEGER NOT NULL DEFAULT 0;",
	}
	existing := map[string]struct{}{}
	rows, err := db.Query(`PRAGMA table_info(tasks);`)
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
		if _, err := db.Exec(alter); err != nil {
			return err
		}
	}
	return rows.Err()
}

func fetchTasks(db *sql.DB) ([]task, error) {
	rows, err := db.Query(`SELECT id, title, done, project, tags, due, start_at, priority, recurring, created_at FROM tasks ORDER BY id;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []task
	for rows.Next() {
		var t task
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

func addTask(db *sql.DB, title string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO tasks (title, done, created_at) VALUES (?, 0, ?);`, title, now)
	return err
}

func setDone(db *sql.DB, id int, done bool) error {
	val := 0
	if done {
		val = 1
	}
	_, err := db.Exec(`UPDATE tasks SET done = ? WHERE id = ?;`, val, id)
	return err
}

func updateTaskMetadata(db *sql.DB, id int, project, tags string, priority int, due, start sql.NullTime, recurring bool) error {
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
	_, err := db.Exec(`UPDATE tasks SET project = ?, tags = ?, priority = ?, due = ?, start_at = ?, recurring = ? WHERE id = ?;`,
		project, tags, priority, dueStr, startStr, rec, id)
	return err
}

func deleteTask(db *sql.DB, id int) error {
	_, err := db.Exec(`DELETE FROM tasks WHERE id = ?;`, id)
	return err
}

func humanDone(done bool) string {
	if done {
		return "done"
	}
	return "pending"
}
