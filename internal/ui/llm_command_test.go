package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/config"
	"bada/internal/llm"
)

// runLLM drives the ":llm …" command and returns the resulting model.
func runLLM(t *testing.T, cfg config.LLM, arg string) (Model, bool) {
	t.Helper()
	m := Model{cfg: config.Config{LLM: cfg}}
	res, cmd := m.runLLMCommand(arg)
	next, ok := res.(Model)
	if !ok {
		t.Fatalf("runLLMCommand(%q) did not return a Model", arg)
	}
	return next, cmd != nil
}

func TestLLMCommandReportsSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	m, dispatched := runLLM(t, config.LLM{}, "")
	if dispatched {
		t.Error(":llm should not contact anything")
	}
	if !strings.Contains(m.status, "not configured") {
		t.Errorf("status = %q, want it to say the LLM is not configured", m.status)
	}

	m, _ = runLLM(t, config.LLM{Provider: "ollama", Model: "llama3.2"}, "")
	for _, want := range []string{"ollama", "llama3.2", "localhost:11434"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status = %q, want it to mention %q", m.status, want)
		}
	}
}

// A config file that predates [llm] entirely has nowhere for :config to add
// a provider, so the unconfigured message must point at :llm init instead.
func TestLLMCommandPointsAtInitForAnOlderConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("db_path = \"x\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	m := Model{configPath: path}
	res, _ := m.runLLMCommand("")
	m = res.(Model)
	if !strings.Contains(m.status, ":llm init") {
		t.Errorf("status = %q, want it to point at :llm init", m.status)
	}

	// A config that already has [llm] (just left empty) keeps pointing at
	// :config, since :llm init would refuse — the section is already there.
	if err := os.WriteFile(path, []byte("[llm]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	res, _ = m.runLLMCommand("")
	m = res.(Model)
	if strings.Contains(m.status, ":llm init") {
		t.Errorf("status = %q, should not suggest :llm init once the section exists", m.status)
	}
}

// A configured-but-broken setup should explain itself rather than report a
// healthy connection.
func TestLLMCommandExplainsAnIncompleteConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	m, _ := runLLM(t, config.LLM{Provider: "openai"}, "")
	if !strings.Contains(m.status, "model") {
		t.Errorf("status = %q, want it to name the missing setting", m.status)
	}
}

// ":llm test" must refuse to dispatch a request it knows will fail, and say why.
func TestLLMTestValidatesBeforeDispatching(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	m, dispatched := runLLM(t, config.LLM{Provider: "openai", Model: "gpt-4o-mini"}, "test")
	if dispatched {
		t.Error("a config with no API key should not reach the network")
	}
	if !strings.Contains(m.status, "API key") {
		t.Errorf("status = %q, want it to name the missing key", m.status)
	}

	m, dispatched = runLLM(t, config.LLM{Provider: "ollama", Model: "llama3.2"}, "test")
	if !dispatched {
		t.Error("a valid config should dispatch the connection test")
	}
	if !strings.Contains(m.status, "contacting") {
		t.Errorf("status = %q, want it to report that a request is in flight", m.status)
	}
}

func TestLLMCommandRejectsAnUnknownArgument(t *testing.T) {
	m, dispatched := runLLM(t, config.LLM{Provider: "ollama", Model: "m"}, "explode")
	if dispatched {
		t.Error("an unknown argument should not contact anything")
	}
	if !strings.Contains(m.status, "usage:") {
		t.Errorf("status = %q, want usage help", m.status)
	}
}

func TestHandleLLMPingFormatsTheResult(t *testing.T) {
	m := Model{}
	res, _ := m.handleLLMPing(llmPingMsg{resp: llm.Response{
		Text: "ok", Model: "llama3.2", Elapsed: 1200 * time.Millisecond,
	}})
	next := res.(Model)
	for _, want := range []string{"LLM ok", "llama3.2", "1.2s", `"ok"`} {
		if !strings.Contains(next.status, want) {
			t.Errorf("status = %q, want it to mention %q", next.status, want)
		}
	}
}

func TestHandleLLMPingReportsFailureOnOneLine(t *testing.T) {
	m := Model{}
	res, _ := m.handleLLMPing(llmPingMsg{err: &llm.APIError{
		Status: 401, Endpoint: "https://api.openai.com/v1/chat/completions",
		Message: "Incorrect API key\nprovided",
	}})
	next := res.(Model)
	if strings.Contains(next.status, "\n") {
		t.Errorf("status must stay on one line, got %q", next.status)
	}
	if !strings.Contains(next.status, "failed") || !strings.Contains(next.status, "401") {
		t.Errorf("status = %q, want the failure and its status code", next.status)
	}
}

// A long or multi-line reply must not break the single-line status bar.
func TestHandleLLMPingTruncatesALongReply(t *testing.T) {
	m := Model{}
	res, _ := m.handleLLMPing(llmPingMsg{resp: llm.Response{
		Text: strings.Repeat("very long ", 50), Model: "m",
	}})
	next := res.(Model)
	if len([]rune(next.status)) > llmReplyPreview+80 {
		t.Errorf("status is %d runes; the reply should be truncated: %q", len([]rune(next.status)), next.status)
	}
}

// ":llm" has to be reachable from the command line's completion list.
func TestLLMCommandIsCompletable(t *testing.T) {
	if got := completeCommand(":ll"); got != ":llm" {
		t.Errorf("completeCommand(\":ll\") = %q, want \":llm\"", got)
	}
}

// The command line has to route ":llm …" to the handler, and leave the prompt
// afterwards like every other command.
func TestLLMCommandDispatch(t *testing.T) {
	m := newTestModel(t)
	m.cfg.LLM = config.LLM{Provider: "ollama", Model: "llama3.2"}
	m.mode = modeCommand
	m.input.SetValue(":llm")

	res, cmd := m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if cmd != nil {
		t.Error(":llm alone should not contact anything")
	}
	if m.mode != modeList {
		t.Errorf("mode = %v, want the command prompt to close", m.mode)
	}
	if !strings.Contains(m.status, "ollama") {
		t.Errorf("status = %q, want the LLM summary", m.status)
	}

	m.mode = modeCommand
	m.input.SetValue(":llm test")
	res, cmd = m.updateCommandMode("enter", tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if cmd == nil {
		t.Error(":llm test should dispatch a request")
	}
	if m.mode != modeList {
		t.Errorf("mode = %v, want the command prompt to close", m.mode)
	}
}

// The help screen has to mention the command, or nobody will find it.
func TestHelpMentionsLLM(t *testing.T) {
	if !strings.Contains(newTestModel(t).helpContent(), ":llm") {
		t.Error("help content should document :llm")
	}
}

// An older config file has no [llm] section, and installing a new bada never
// rewrites an existing config — so ":llm init" has to add the settings without
// disturbing what is already there.
func TestLLMInitAppendsSectionsToAnOlderConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "db_path = \"/tmp/bada.db\"\n\n[keys]\n# my own comment\nquit = \"Q\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	m := newTestModel(t)
	m.configPath = path

	res, _ := m.initLLMConfig()
	m = res.(Model)

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.HasPrefix(string(saved), original) {
		t.Errorf("the existing config must be preserved verbatim:\n%s", saved)
	}
	if !strings.Contains(string(saved), "# my own comment") {
		t.Error("appending must not strip the user's comments")
	}
	for _, want := range []string{"[llm]", "[report]", "provider = \"\""} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("config is missing %q after init:\n%s", want, saved)
		}
	}
	if !strings.Contains(m.status, path) {
		t.Errorf("status = %q, want the path that was updated", m.status)
	}
	// The appended settings must load, and be live in the model straight away.
	if _, err := config.LoadOrCreate(path); err != nil {
		t.Fatalf("the appended config does not parse: %v", err)
	}
	if m.cfg.Report.ResolvedPeriod() != "7d" {
		t.Errorf("config not reloaded into the model: %+v", m.cfg.Report)
	}
	// A config file can hold an API key; init must not widen its permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want the original 0600", info.Mode().Perm())
	}
}

func TestLLMInitIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[llm]\nprovider = \"ollama\"\nmodel = \"m\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	m := newTestModel(t)
	m.configPath = path

	res, _ := m.initLLMConfig()
	m = res.(Model)
	if !strings.Contains(m.status, "already has") {
		t.Errorf("status = %q, want it to report the section exists", m.status)
	}
	saved, _ := os.ReadFile(path)
	if strings.Count(string(saved), "[llm]") != 1 {
		t.Errorf("init must not add a second [llm] section:\n%s", saved)
	}
}

// A config that already has [report] but not [llm] must not end up with two
// [report] tables — TOML rejects duplicates.
func TestLLMInitSkipsAnExistingReportSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[report]\ndefault_period = \"30d\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	m := newTestModel(t)
	m.configPath = path

	res, _ := m.initLLMConfig()
	m = res.(Model)

	saved, _ := os.ReadFile(path)
	if strings.Count(string(saved), "[report]") != 1 {
		t.Errorf("init added a duplicate [report] section:\n%s", saved)
	}
	if !strings.Contains(string(saved), "[llm]") {
		t.Errorf("init should still add [llm]:\n%s", saved)
	}
	cfg, err := config.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("the appended config does not parse: %v", err)
	}
	if cfg.Report.DefaultPeriod != "30d" {
		t.Errorf("init clobbered the existing [report] settings: %+v", cfg.Report)
	}
	if !strings.Contains(m.status, "Added") {
		t.Errorf("status = %q, want a confirmation", m.status)
	}
}

// The ":llm init" template is a third place these settings are written, next to
// config.example.toml and the marshaled defaults. If it falls behind, an
// upgrading user gets fewer fields than a fresh install — which is the problem
// init exists to solve.
func TestLLMInitTemplateListsEverySetting(t *testing.T) {
	for _, group := range []struct {
		section string
		value   any
	}{
		{"llm", config.LLM{}},
		{"report", config.Report{}},
	} {
		if !strings.Contains(llmConfigTemplate, "["+group.section+"]") {
			t.Errorf("the init template has no [%s] section", group.section)
			continue
		}
		typ := reflect.TypeOf(group.value)
		for i := 0; i < typ.NumField(); i++ {
			tag := typ.Field(i).Tag.Get("toml")
			field, _, _ := strings.Cut(tag, ",")
			if field == "" || field == "-" {
				continue
			}
			// Live or commented, in key form or as its own table header.
			pattern := regexp.MustCompile(`(?m)^\s*#?\s*(` +
				regexp.QuoteMeta(field) + `\s*=|\[` +
				regexp.QuoteMeta(group.section+"."+field) + `\])`)
			if !pattern.MatchString(llmConfigTemplate) {
				t.Errorf("the init template never mentions [%s].%s", group.section, field)
			}
		}
	}
}

// Whatever init appends has to be valid TOML that leaves the feature off.
func TestLLMInitTemplateIsInert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(llmConfigTemplate), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("the init template is not valid TOML: %v", err)
	}
	if cfg.LLM.Configured() {
		t.Errorf("init must not switch the LLM on, got provider %q", cfg.LLM.Provider)
	}
}
