package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// DefaultReportDir is where :report writes its Markdown files, under the
	// data directory (never the cache directory — these are user documents).
	DefaultReportDir = "reports"
	// DefaultReportPeriod is the window a bare :report covers.
	DefaultReportPeriod = "7d"
	maxReportPeriodDays = 3650
)

// Report configures the LLM-written project report produced by :report.
type Report struct {
	// Dir is where saved reports are written. Empty means the default,
	// $XDG_DATA_HOME/bada/reports.
	Dir string `toml:"dir"`
	// DefaultPeriod is the window a bare ":report" covers, e.g. "7d", "week",
	// "month", or "90d".
	DefaultPeriod string `toml:"default_period"`
	// SystemPrompt replaces the built-in instructions entirely. Run
	// ":report prompt" to see the effective prompt, as a starting point.
	SystemPrompt string `toml:"system_prompt"`
	// SystemPromptFile is the same override read from a file, which is easier
	// to iterate on than a long TOML string. It wins over SystemPrompt.
	SystemPromptFile string `toml:"system_prompt_file"`
	// ExtraInstructions is appended to whichever system prompt is in force —
	// the way to nudge tone, language, or focus without restating the whole
	// thing ("write in Korean", "lead with risks").
	ExtraInstructions string `toml:"extra_instructions"`
	// MaxCommits and MaxTasks bound how much history is put in the prompt, so
	// a busy month cannot blow past the model's context window.
	MaxCommits int `toml:"max_commits"`
	MaxTasks   int `toml:"max_tasks"`
}

// ResolvedDir is the directory saved reports are written to.
func (r Report) ResolvedDir() string {
	if dir := strings.TrimSpace(r.Dir); dir != "" {
		return dir
	}
	return filepath.Join(DefaultDataDir(), DefaultReportDir)
}

// ResolvedPeriod is the configured default window, falling back to the
// built-in one when unset.
func (r Report) ResolvedPeriod() string {
	if p := strings.TrimSpace(r.DefaultPeriod); p != "" {
		return p
	}
	return DefaultReportPeriod
}

// Limits are the prompt-size caps, with defaults applied.
func (r Report) Limits() (maxCommits, maxTasks int) {
	maxCommits, maxTasks = r.MaxCommits, r.MaxTasks
	if maxCommits <= 0 {
		maxCommits = 200
	}
	if maxTasks <= 0 {
		maxTasks = 200
	}
	return maxCommits, maxTasks
}

// ParsePeriodDays turns a window like "7d", "week", "month", or "quarter" into
// a day count. The error is shown to the user, so it lists what is accepted.
func ParsePeriodDays(period string) (int, error) {
	p := strings.ToLower(strings.TrimSpace(period))
	switch p {
	case "":
		return 0, fmt.Errorf("empty period")
	case "day", "today", "24h":
		return 1, nil
	case "week":
		return 7, nil
	case "fortnight":
		return 14, nil
	case "month":
		return 30, nil
	case "quarter":
		return 90, nil
	case "year":
		return 365, nil
	}
	// "30d" / "12w" / "3m" — a count plus a unit.
	unit, mult := "", 0
	for _, u := range []struct {
		suffix string
		days   int
	}{{"d", 1}, {"w", 7}, {"m", 30}, {"y", 365}} {
		if strings.HasSuffix(p, u.suffix) {
			unit, mult = u.suffix, u.days
			break
		}
	}
	if mult == 0 {
		return 0, fmt.Errorf("unknown period %q; try 7d, 30d, week, month, or quarter", period)
	}
	n := 0
	digits := strings.TrimSuffix(p, unit)
	if digits == "" {
		return 0, fmt.Errorf("unknown period %q; try 7d, 30d, week, month, or quarter", period)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("unknown period %q; try 7d, 30d, week, month, or quarter", period)
		}
		// A long digit string would otherwise overflow n (and n*mult below)
		// and wrap around, letting a bogus value slip past the range check.
		if n > maxReportPeriodDays {
			return 0, fmt.Errorf("period %q is too long; the maximum is 10 years", period)
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("period must cover at least one day, got %q", period)
	}
	if days := n * mult; days <= maxReportPeriodDays {
		return days, nil
	}
	return 0, fmt.Errorf("period %q is too long; the maximum is 10 years", period)
}
