package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	DefaultConfigFileName = "config.toml"
	DefaultConfigPathFile = "config.path"
	DefaultDBName         = "bada.db"
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
	ThemeToggle   string `toml:"theme_toggle"`
}

type Theme struct {
	// Preset names a built-in palette. When set, it provides the base colors; any
	// other field left non-empty overrides that base.
	Preset      string `toml:"preset"`
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
	HolidayBg   string `toml:"holiday_bg"`
	RowStripeBg string `toml:"row_stripe_bg"`
}

// Holiday is a public holiday shaded on the timeline. Date is either a full
// "YYYY-MM-DD" (a one-off, e.g. a moving holiday) or "MM-DD" (recurs yearly).
type Holiday struct {
	Date string `toml:"date"`
	Name string `toml:"name"`
}

type Agenda struct {
	UpcomingDays int `toml:"upcoming_days"`
}

type Config struct {
	DBPath        string    `toml:"db_path"`
	DefaultFilter string    `toml:"default_filter"`
	TrashDir      string    `toml:"trash_dir"`
	Keys          Keymap    `toml:"keys"`
	Theme         Theme     `toml:"theme"`
	Agenda        Agenda    `toml:"agenda"`
	Holidays      []Holiday `toml:"holidays"`
}

func LoadOrCreate(path string) (Config, error) {
	cfg := defaultConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, errors.New("config path is empty")
	}
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
	// Decode a second time into a zero-valued struct so we can tell which theme
	// fields the user actually set, then fold in the named preset (if any).
	var raw Config
	if err := toml.Unmarshal(data, &raw); err == nil {
		resolveThemePreset(&cfg, raw.Theme)
	}
	applyKeyDefaults(&cfg)
	if cfg.DBPath == "" {
		cfg.DBPath = DefaultDBPath()
	}
	if cfg.TrashDir == "" {
		cfg.TrashDir = DefaultTrashPath()
	}
	applyAgendaDefaults(&cfg)
	return cfg, nil
}

// ThemePresetNames lists the canonical built-in palette names, in display order.
// It is the single source of truth for the themes offered by the :theme command.
func ThemePresetNames() []string {
	return []string{"light", "dark", "purple", "ocean", "forest", "rose", "graphite"}
}

// PresetTheme returns a built-in palette by name ("light"/"default",
// "dark"/"slate", "purple"/"violet", and other named palettes). The second
// result is false for an unknown name.
func PresetTheme(name string) (Theme, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "light", "default", "":
		t := defaultConfig().Theme
		t.Preset = "light"
		return t, true
	case "dark", "slate", "dark-slate":
		return Theme{
			Preset:      "dark",
			Title:       "#8AB4F8",
			Heading:     "#7DD3FC",
			Accent:      "#F2CC60",
			Muted:       "#8B949E",
			Success:     "#3FB950",
			Warning:     "#D29922",
			Danger:      "#F85149",
			Border:      "#30363D",
			SelectionBg: "#264F78",
			SelectionFg: "#F0F6FC",
			StatusBg:    "#161B22",
			StatusFg:    "#C9D1D9",
			StatusAltBg: "#21262D",
			StatusAltFg: "#F0F6FC",
			HolidayBg:   "#3D1722",
			RowStripeBg: "#11161D",
		}, true
	case "purple", "violet", "amethyst":
		return Theme{
			Preset:      "purple",
			Title:       "#C4B5FD",
			Heading:     "#DDD6FE",
			Accent:      "#F0ABFC",
			Muted:       "#A1A1AA",
			Success:     "#34D399",
			Warning:     "#FBBF24",
			Danger:      "#FB7185",
			Border:      "#4C1D95",
			SelectionBg: "#6D28D9",
			SelectionFg: "#FAF5FF",
			StatusBg:    "#1E1B2E",
			StatusFg:    "#E9D5FF",
			StatusAltBg: "#2E1065",
			StatusAltFg: "#F5F3FF",
			HolidayBg:   "#4C1230",
			RowStripeBg: "#211A33",
		}, true
	case "ocean", "marine", "aqua":
		return Theme{
			Preset:      "ocean",
			Title:       "#67E8F9",
			Heading:     "#5EEAD4",
			Accent:      "#38BDF8",
			Muted:       "#94A3B8",
			Success:     "#2DD4BF",
			Warning:     "#FBBF24",
			Danger:      "#FB7185",
			Border:      "#155E75",
			SelectionBg: "#0E7490",
			SelectionFg: "#ECFEFF",
			StatusBg:    "#082F49",
			StatusFg:    "#BAE6FD",
			StatusAltBg: "#164E63",
			StatusAltFg: "#ECFEFF",
			HolidayBg:   "#4A1D2F",
			RowStripeBg: "#0B2538",
		}, true
	case "forest", "evergreen", "green":
		return Theme{
			Preset:      "forest",
			Title:       "#BBF7D0",
			Heading:     "#86EFAC",
			Accent:      "#FACC15",
			Muted:       "#9CA3AF",
			Success:     "#22C55E",
			Warning:     "#F59E0B",
			Danger:      "#F87171",
			Border:      "#365314",
			SelectionBg: "#166534",
			SelectionFg: "#F0FDF4",
			StatusBg:    "#172016",
			StatusFg:    "#DCFCE7",
			StatusAltBg: "#1F3A1D",
			StatusAltFg: "#F0FDF4",
			HolidayBg:   "#451A2B",
			RowStripeBg: "#141C13",
		}, true
	case "rose", "blush", "magenta":
		return Theme{
			Preset:      "rose",
			Title:       "#FDA4AF",
			Heading:     "#F9A8D4",
			Accent:      "#FBBF24",
			Muted:       "#A8A29E",
			Success:     "#4ADE80",
			Warning:     "#F59E0B",
			Danger:      "#F43F5E",
			Border:      "#881337",
			SelectionBg: "#BE185D",
			SelectionFg: "#FFF1F2",
			StatusBg:    "#2A171C",
			StatusFg:    "#FFE4E6",
			StatusAltBg: "#4C1026",
			StatusAltFg: "#FFF1F2",
			HolidayBg:   "#5F1234",
			RowStripeBg: "#25161B",
		}, true
	case "graphite", "mono", "neutral":
		return Theme{
			Preset:      "graphite",
			Title:       "#E5E7EB",
			Heading:     "#CBD5E1",
			Accent:      "#A3E635",
			Muted:       "#9CA3AF",
			Success:     "#84CC16",
			Warning:     "#EAB308",
			Danger:      "#EF4444",
			Border:      "#3F3F46",
			SelectionBg: "#52525B",
			SelectionFg: "#FAFAFA",
			StatusBg:    "#18181B",
			StatusFg:    "#D4D4D8",
			StatusAltBg: "#27272A",
			StatusAltFg: "#FAFAFA",
			HolidayBg:   "#3F1D2A",
			RowStripeBg: "#202024",
		}, true
	}
	return Theme{}, false
}

// resolveThemePreset applies a named preset as the base palette when the config
// file sets [theme].preset, keeping any explicitly-provided color as an override.
// raw holds only the fields the user actually wrote (zero elsewhere).
func resolveThemePreset(cfg *Config, raw Theme) {
	if strings.TrimSpace(raw.Preset) == "" {
		return
	}
	base, ok := PresetTheme(raw.Preset)
	if !ok {
		return
	}
	overlayTheme(&base, raw)
	cfg.Theme = base
}

// overlayTheme copies every non-empty color field from src onto dst, leaving
// dst's value in place where src is blank. Preset is not overlaid.
func overlayTheme(dst *Theme, src Theme) {
	set := func(d *string, s string) {
		if strings.TrimSpace(s) != "" {
			*d = s
		}
	}
	set(&dst.Title, src.Title)
	set(&dst.Heading, src.Heading)
	set(&dst.Accent, src.Accent)
	set(&dst.Muted, src.Muted)
	set(&dst.Success, src.Success)
	set(&dst.Warning, src.Warning)
	set(&dst.Danger, src.Danger)
	set(&dst.Border, src.Border)
	set(&dst.SelectionBg, src.SelectionBg)
	set(&dst.SelectionFg, src.SelectionFg)
	set(&dst.StatusBg, src.StatusBg)
	set(&dst.StatusFg, src.StatusFg)
	set(&dst.StatusAltBg, src.StatusAltBg)
	set(&dst.StatusAltFg, src.StatusAltFg)
	set(&dst.HolidayBg, src.HolidayBg)
	set(&dst.RowStripeBg, src.RowStripeBg)
}

func applyAgendaDefaults(cfg *Config) {
	if cfg.Agenda.UpcomingDays <= 0 {
		cfg.Agenda.UpcomingDays = defaultConfig().Agenda.UpcomingDays
	}
	if cfg.Agenda.UpcomingDays > 365 {
		cfg.Agenda.UpcomingDays = 365
	}
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
	if cfg.Keys.ThemeToggle == "" {
		cfg.Keys.ThemeToggle = def.ThemeToggle
	}
}

func write(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func defaultConfig() Config {
	return Config{
		DBPath:        DefaultDBPath(),
		DefaultFilter: "all",
		TrashDir:      DefaultTrashPath(),
		Keys: Keymap{
			Quit:          "q",
			Add:           "a",
			Up:            "k",
			Down:          "j",
			Toggle:        "r",
			Delete:        "D",
			Detail:        "v",
			Confirm:       "enter",
			Cancel:        "esc",
			Edit:          "e",
			Trash:         "T",
			Rename:        "", // folded into Edit ("e"); no dedicated key
			PriorityUp:    "+",
			PriorityDown:  "-",
			DueForward:    "]",
			DueBack:       "[",
			SortDue:       "sd",
			SortPriority:  "sp",
			SortCreated:   "st",
			DeleteAllDone: "X",
			Search:        "/",
			NoteView:      "enter",
			ThemeToggle:   "t",
		},
		Agenda: Agenda{
			UpcomingDays: 3,
		},
		Theme: Theme{
			Preset:      "light",
			Title:       "#2563EB",
			Heading:     "#0F766E",
			Accent:      "#D97706",
			Muted:       "#64748B",
			Success:     "#16A34A",
			Warning:     "#B45309",
			Danger:      "#DC2626",
			Border:      "#CBD5E1",
			SelectionBg: "#DBEAFE",
			SelectionFg: "#0F172A",
			StatusBg:    "#E2E8F0",
			StatusFg:    "#0F172A",
			StatusAltBg: "#0F766E",
			StatusAltFg: "#F8FAFC",
			HolidayBg:   "#FECACA",
			RowStripeBg: "#F1F5F9",
		},
		// Empty by default; users add their region's holidays, e.g.
		//   [[holidays]]
		//   date = "01-01"   # recurs yearly
		//   name = "New Year's Day"
		Holidays: nil,
	}
}

func Save(path string, cfg Config) error {
	return write(path, cfg)
}

func DefaultConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "bada")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "bada")
	}
	return "bada"
}

func DefaultCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); dir != "" {
		return filepath.Join(dir, "bada")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "bada")
	}
	return "bada-cache"
}

func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), DefaultConfigFileName)
}

func DefaultDBPath() string {
	return filepath.Join(DefaultCacheDir(), DefaultDBName)
}

func DefaultTrashPath() string {
	return filepath.Join(DefaultCacheDir(), DefaultTrashDir)
}

func ResolveConfigPath() string {
	if env := strings.TrimSpace(os.Getenv("BADA_CONFIG")); env != "" {
		return env
	}
	if data, err := os.ReadFile(ConfigPathFile()); err == nil {
		if path := strings.TrimSpace(string(data)); path != "" {
			return path
		}
	}
	return DefaultConfigPath()
}

func ConfigPathFile() string {
	return filepath.Join(DefaultConfigDir(), DefaultConfigPathFile)
}

func SetConfigPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("config path is empty")
	}
	if err := os.MkdirAll(DefaultConfigDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ConfigPathFile(), []byte(path+"\n"), 0o644)
}
