package config

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// tomlFieldNames lists the toml key of every field on a struct type.
func tomlFieldNames(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("toml")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}

func readExampleConfig(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("../../config.example.toml")
	if err != nil {
		t.Fatalf("read config.example.toml: %v", err)
	}
	return string(body)
}

// install.sh seeds a new install by copying config.example.toml, while first
// launch writes the marshaled defaults. The two must offer the same settings,
// or which fields a user can see depends on how they installed — the exact
// drift that hid [llm] from an upgrade.
func TestExampleConfigDocumentsEverySetting(t *testing.T) {
	body := readExampleConfig(t)
	for _, group := range []struct {
		section string
		fields  []string
	}{
		{"llm", tomlFieldNames(t, LLM{})},
		{"report", tomlFieldNames(t, Report{})},
	} {
		if !strings.Contains(body, "\n["+group.section+"]") {
			t.Errorf("config.example.toml has no [%s] section", group.section)
			continue
		}
		for _, field := range group.fields {
			// The key may be live or commented, but it has to be visible. A
			// map-valued field is written as its own table header instead
			// ("[llm.headers]"), so accept that spelling too.
			pattern := regexp.MustCompile(`(?m)^\s*#?\s*(` +
				regexp.QuoteMeta(field) + `\s*=|\[` +
				regexp.QuoteMeta(group.section+"."+field) + `\])`)
			if !pattern.MatchString(body) {
				t.Errorf("config.example.toml never mentions [%s].%s — a new install would not see it",
					group.section, field)
			}
		}
	}
}

// A fresh config must parse, and must leave the LLM switched off: copying the
// example should never start contacting a provider on its own.
func TestExampleConfigIsInertAndValid(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	if err := os.WriteFile(path, []byte(readExampleConfig(t)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("the example config does not load: %v", err)
	}
	if cfg.LLM.Configured() {
		t.Errorf("a fresh config must leave the LLM off, got provider %q", cfg.LLM.Provider)
	}
	if cfg.Report.ResolvedPeriod() == "" {
		t.Error("the example should ship a usable default report period")
	}
}

// The defaults written on first launch must carry the same sections, so a user
// who never ran install.sh sees them too.
func TestWrittenDefaultsIncludeLLMSections(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	if _, err := LoadOrCreate(path); err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, want := range []string{"[llm]", "[report]", "provider = ''", "timeout_seconds = 60"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("a newly written config is missing %q:\n%s", want, body)
		}
	}
}
