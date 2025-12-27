package config

import (
	"errors"
	"os"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	DefaultConfigFileName = "config.toml"
	DefaultDBName         = "todo.db"
)

type Keymap struct {
	Quit         string `toml:"quit"`
	Add          string `toml:"add"`
	Up           string `toml:"up"`
	Down         string `toml:"down"`
	Toggle       string `toml:"toggle"`
	Delete       string `toml:"delete"`
	Detail       string `toml:"detail"`
	Confirm      string `toml:"confirm"`
	Cancel       string `toml:"cancel"`
	Edit         string `toml:"edit"`
	Rename       string `toml:"rename"`
	PriorityUp   string `toml:"priority_up"`
	PriorityDown string `toml:"priority_down"`
	DueForward   string `toml:"due_forward"`
	DueBack      string `toml:"due_back"`
	SortDue      string `toml:"sort_due"`
	SortPriority string `toml:"sort_priority"`
	SortCreated  string `toml:"sort_created"`
}

type Config struct {
	DBPath        string `toml:"db_path"`
	DefaultFilter string `toml:"default_filter"`
	Keys          Keymap `toml:"keys"`
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
	if cfg.DBPath == "" {
		cfg.DBPath = DefaultDBName
	}
	return cfg, nil
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
		Keys: Keymap{
			Quit:         "q",
			Add:          "a",
			Up:           "k",
			Down:         "j",
			Toggle:       " ",
			Delete:       "d",
			Detail:       "enter",
			Confirm:      "enter",
			Cancel:       "esc",
			Edit:         "e",
			Rename:       "r",
			PriorityUp:   "+",
			PriorityDown: "-",
			DueForward:   "]",
			DueBack:      "[",
			SortDue:      "sd",
			SortPriority: "sp",
			SortCreated:  "st",
		},
	}
}
