package config

import (
	"errors"
	"os"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	DefaultConfigFileName = "config.toml"
	DefaultDBName         = "todo.db"
	DefaultTrashDir       = "trash"
)

type Keymap struct {
	Quit          string `toml:"quit"`
	Add           string `toml:"add"`
	Up            string `toml:"up"`
	Down          string `toml:"down"`
	Toggle        string `toml:"toggle"`
	Delete        string `toml:"delete"`
	Detail        string `toml:"detail"`
	Confirm       string `toml:"confirm"`
	Cancel        string `toml:"cancel"`
	Edit          string `toml:"edit"`
	Trash         string `toml:"trash"`
	Rename        string `toml:"rename"`
	PriorityUp    string `toml:"priority_up"`
	PriorityDown  string `toml:"priority_down"`
	DueForward    string `toml:"due_forward"`
	DueBack       string `toml:"due_back"`
	SortDue       string `toml:"sort_due"`
	SortPriority  string `toml:"sort_priority"`
	SortCreated   string `toml:"sort_created"`
	DeleteAllDone string `toml:"delete_all_done"`
	Search        string `toml:"search"`
	NoteView      string `toml:"note_view"`
}

type Theme struct {
	Title       string `toml:"title"`
	Heading     string `toml:"heading"`
	Accent      string `toml:"accent"`
	Muted       string `toml:"muted"`
	Success     string `toml:"success"`
	Warning     string `toml:"warning"`
	Danger      string `toml:"danger"`
	Border      string `toml:"border"`
	SelectionBg string `toml:"selection_bg"`
	SelectionFg string `toml:"selection_fg"`
	StatusBg    string `toml:"status_bg"`
	StatusFg    string `toml:"status_fg"`
	StatusAltBg string `toml:"status_alt_bg"`
	StatusAltFg string `toml:"status_alt_fg"`
}

type Config struct {
	DBPath        string `toml:"db_path"`
	DefaultFilter string `toml:"default_filter"`
	TrashDir      string `toml:"trash_dir"`
	Keys          Keymap `toml:"keys"`
	Theme         Theme  `toml:"theme"`
}

func LoadOrCreate(path string) (Config, error) {
	cfg := defaultConfig()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := write(path, cfg); err != nil {
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
	applyKeyDefaults(&cfg)
	if cfg.DBPath == "" {
		cfg.DBPath = DefaultDBName
	}
	if cfg.TrashDir == "" {
		cfg.TrashDir = DefaultTrashDir
	}
	return cfg, nil
}

func applyKeyDefaults(cfg *Config) {
	def := defaultConfig().Keys
	if cfg.Keys.Quit == "" {
		cfg.Keys.Quit = def.Quit
	}
	if cfg.Keys.Add == "" {
		cfg.Keys.Add = def.Add
	}
	if cfg.Keys.Up == "" {
		cfg.Keys.Up = def.Up
	}
	if cfg.Keys.Down == "" {
		cfg.Keys.Down = def.Down
	}
	if cfg.Keys.Toggle == "" {
		cfg.Keys.Toggle = def.Toggle
	}
	if cfg.Keys.Delete == "" {
		cfg.Keys.Delete = def.Delete
	}
	if cfg.Keys.Detail == "" {
		cfg.Keys.Detail = def.Detail
	}
	if cfg.Keys.Confirm == "" {
		cfg.Keys.Confirm = def.Confirm
	}
	if cfg.Keys.Cancel == "" {
		cfg.Keys.Cancel = def.Cancel
	}
	if cfg.Keys.Edit == "" {
		cfg.Keys.Edit = def.Edit
	}
	if cfg.Keys.Trash == "" {
		cfg.Keys.Trash = def.Trash
	}
	if cfg.Keys.Rename == "" {
		cfg.Keys.Rename = def.Rename
	}
	if cfg.Keys.PriorityUp == "" {
		cfg.Keys.PriorityUp = def.PriorityUp
	}
	if cfg.Keys.PriorityDown == "" {
		cfg.Keys.PriorityDown = def.PriorityDown
	}
	if cfg.Keys.DueForward == "" {
		cfg.Keys.DueForward = def.DueForward
	}
	if cfg.Keys.DueBack == "" {
		cfg.Keys.DueBack = def.DueBack
	}
	if cfg.Keys.SortDue == "" {
		cfg.Keys.SortDue = def.SortDue
	}
	if cfg.Keys.SortPriority == "" {
		cfg.Keys.SortPriority = def.SortPriority
	}
	if cfg.Keys.SortCreated == "" {
		cfg.Keys.SortCreated = def.SortCreated
	}
	if cfg.Keys.DeleteAllDone == "" {
		cfg.Keys.DeleteAllDone = def.DeleteAllDone
	}
	if cfg.Keys.Search == "" {
		cfg.Keys.Search = def.Search
	}
	if cfg.Keys.NoteView == "" {
		cfg.Keys.NoteView = def.NoteView
	}
}

func write(path string, cfg Config) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func defaultConfig() Config {
	return Config{
		DBPath:        DefaultDBName,
		DefaultFilter: "all",
		TrashDir:      DefaultTrashDir,
		Keys: Keymap{
			Quit:          "q",
			Add:           "a",
			Up:            "k",
			Down:          "j",
			Toggle:        " ",
			Delete:        "d",
			Detail:        "v",
			Confirm:       "enter",
			Cancel:        "esc",
			Edit:          "e",
			Trash:         "T",
			Rename:        "r",
			PriorityUp:    "+",
			PriorityDown:  "-",
			DueForward:    "]",
			DueBack:       "[",
			SortDue:       "sd",
			SortPriority:  "sp",
			SortCreated:   "st",
			DeleteAllDone: "D",
			Search:        "/",
			NoteView:      "enter",
		},
		Theme: Theme{
			Title:       "#5B8DEF",
			Heading:     "#62B6CB",
			Accent:      "#F4A259",
			Muted:       "#6B7280",
			Success:     "#3CB371",
			Warning:     "#E9C46A",
			Danger:      "#E76F51",
			Border:      "#94A3B8",
			SelectionBg: "#FAD97A",
			StatusBg:    "#E5E7EB",
			StatusFg:    "#0B0F14",
			StatusAltBg: "#CFE8FF",
			StatusAltFg: "#0B0F14",
		},
	}
}
