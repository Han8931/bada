package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePeriodDays(t *testing.T) {
	for in, want := range map[string]int{
		"7d": 7, "1d": 1, "30d": 30, "90d": 90,
		"week": 7, "month": 30, "quarter": 90, "year": 365,
		"day": 1, "fortnight": 14,
		"2w": 14, "3m": 90, "1y": 365,
		"WEEK": 7, " 7d ": 7,
	} {
		got, err := ParsePeriodDays(in)
		if err != nil {
			t.Errorf("ParsePeriodDays(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParsePeriodDays(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParsePeriodDaysRejectsNonsense(t *testing.T) {
	for _, in := range []string{"", "soon", "d", "-3d", "0d", "7 days", "1000y", "3x", "7dd"} {
		if got, err := ParsePeriodDays(in); err == nil {
			t.Errorf("ParsePeriodDays(%q) = %d, want an error", in, got)
		}
	}
}

// A very long digit string must not overflow into a small or negative
// number that slips past the "too long" check.
func TestParsePeriodDaysRejectsOverflow(t *testing.T) {
	got, err := ParsePeriodDays("99999999999999999999y")
	if err == nil {
		t.Fatalf("ParsePeriodDays(huge) = %d, want an error", got)
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error = %q, want it to say the period is too long", err)
	}
}

// The error has to tell the user what is accepted — it lands in the status bar.
func TestParsePeriodDaysErrorNamesTheOptions(t *testing.T) {
	_, err := ParsePeriodDays("soon")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"7d", "week", "month"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should suggest %q", err, want)
		}
	}
}

func TestReportDefaults(t *testing.T) {
	var r Report
	if got := r.ResolvedPeriod(); got != DefaultReportPeriod {
		t.Errorf("ResolvedPeriod() = %q, want %q", got, DefaultReportPeriod)
	}
	if got := r.ResolvedDir(); !strings.HasSuffix(got, filepath.Join("bada", DefaultReportDir)) {
		t.Errorf("ResolvedDir() = %q, want it under the data dir", got)
	}
	commits, tasks := r.Limits()
	if commits <= 0 || tasks <= 0 {
		t.Errorf("Limits() = %d, %d; both need a positive default", commits, tasks)
	}

	custom := Report{Dir: "/tmp/reports", DefaultPeriod: "30d", MaxCommits: 10, MaxTasks: 20}
	if got := custom.ResolvedDir(); got != "/tmp/reports" {
		t.Errorf("ResolvedDir() = %q", got)
	}
	if got := custom.ResolvedPeriod(); got != "30d" {
		t.Errorf("ResolvedPeriod() = %q", got)
	}
	if c, ta := custom.Limits(); c != 10 || ta != 20 {
		t.Errorf("Limits() = %d, %d, want 10, 20", c, ta)
	}
}

// A configured prompt is often a long multi-line TOML string; it has to
// survive a load intact.
func TestLoadKeepsMultiLineSystemPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[report]\n" +
		"default_period = \"30d\"\n" +
		"extra_instructions = \"Write in Korean.\"\n" +
		"system_prompt = \"\"\"\n" +
		"Line one.\n" +
		"Line two.\n" +
		"\"\"\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if cfg.Report.DefaultPeriod != "30d" {
		t.Errorf("DefaultPeriod = %q", cfg.Report.DefaultPeriod)
	}
	if !strings.Contains(cfg.Report.SystemPrompt, "Line one.") || !strings.Contains(cfg.Report.SystemPrompt, "Line two.") {
		t.Errorf("SystemPrompt did not survive the load: %q", cfg.Report.SystemPrompt)
	}
	if cfg.Report.ExtraInstructions != "Write in Korean." {
		t.Errorf("ExtraInstructions = %q", cfg.Report.ExtraInstructions)
	}
}
