package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bada/internal/config"
	"bada/internal/llm"
)

// llmPingMsg carries a finished connection test back to the event loop.
type llmPingMsg struct {
	resp llm.Response
	err  error
}

// llmReplyPreview is how many columns of a test reply the status bar shows.
const llmReplyPreview = 60

// llmPingCmd runs the round-trip off the event loop, so a slow or unreachable
// endpoint leaves the UI responsive instead of freezing it for the timeout.
func llmPingCmd(cfg config.LLM) tea.Cmd {
	return func() tea.Msg {
		client, err := llm.New(cfg)
		if err != nil {
			return llmPingMsg{err: err}
		}
		// The client already carries the configured timeout; this context is
		// the backstop for a connection that hangs before that applies.
		ctx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout()+5*time.Second)
		defer cancel()
		resp, err := client.Ping(ctx)
		return llmPingMsg{resp: resp, err: err}
	}
}

// runLLMCommand backs ":llm" (report the configured connection) and
// ":llm test" (actually contact it).
func (m Model) runLLMCommand(arg string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		m.status = "LLM: " + m.cfg.LLM.Summary()
		if m.cfg.LLM.Configured() {
			if err := m.cfg.LLM.Validate(); err != nil {
				m.status += " — " + err.Error()
			} else {
				m.status += " — :llm test to check it"
			}
		} else if m.configPredatesLLMSection() {
			// A config written before [llm] existed has nowhere for
			// :config to put a provider — point at the command that adds
			// the section, not just the editor.
			m.status += " — this config predates it; run :llm init to add the settings"
		} else {
			m.status += " — set [llm].provider and [llm].model in :config"
		}
		return m, nil
	case "init":
		return m.initLLMConfig()
	case "test", "ping":
		if err := m.cfg.LLM.Validate(); err != nil {
			m.status = "LLM: " + err.Error()
			return m, nil
		}
		m.status = fmt.Sprintf("LLM: contacting %s…", m.cfg.LLM.ResolvedBaseURL())
		return m, llmPingCmd(m.cfg.LLM)
	default:
		m.status = "usage: :llm · :llm test (check the connection) · :llm init (add the settings to an older config)"
		return m, nil
	}
}

// llmConfigTemplate is what ":llm init" appends. It is commented out apart
// from the section headers, so adding it changes no behavior until the user
// fills in a provider — and it is plain text, appended rather than re-encoded,
// so the rest of the file keeps its own comments and formatting.
const llmConfigTemplate = `
[llm]
# Chat-completion API used by :report. Contacted only when a command asks.
# provider: "openai", "openai-compatible" (any OpenAI-format gateway; needs
# base_url), or "ollama" (local, no key). Run :llm test once it is filled in.
# Every field is listed blank so the settings are visible; an empty provider
# leaves the feature off. base_url defaults per provider
# (https://api.openai.com/v1 · http://localhost:11434). api_key accepts
# "env:NAME" so the secret need not live in this file.
provider = ""
base_url = ""
model = ""
api_key = ""
api_key_env = ""
timeout_seconds = 60
max_tokens = 0
# temperature is omitted from the request unless set — not the same as 0.
# temperature = 0.2
# Extra HTTP headers, for gateways that require them:
# [llm.headers]
# HTTP-Referer = "https://github.com/"

[report]
# :report writes a status report from a project's git log and tasks.
# An empty system_prompt means the built-in one; 0 means the built-in cap.
dir = ""
default_period = "7d"
system_prompt = ""
system_prompt_file = ""
extra_instructions = ""
max_commits = 0
max_tasks = 0
`

// initLLMConfig appends the [llm] and [report] sections to a config file that
// predates them. Installing a new bada never rewrites an existing config, so
// without this the settings are invisible to anyone upgrading.
func (m Model) initLLMConfig() (tea.Model, tea.Cmd) {
	path := strings.TrimSpace(m.configPath)
	if path == "" {
		m.status = "No config file path is known"
		return m, nil
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		m.status = "Could not read " + path + ": " + collapseWhitespace(err.Error())
		return m, nil
	}
	if hasTOMLSection(string(existing), "llm") {
		m.status = path + " already has an [llm] section — edit it with :config"
		return m, nil
	}
	addition := llmConfigTemplate
	if hasTOMLSection(string(existing), "report") {
		// Keep only the [llm] half rather than adding a second [report] table,
		// which TOML rejects as a duplicate.
		if llmOnly, _, found := strings.Cut(addition, "\n[report]"); found {
			addition = llmOnly + "\n"
		}
	}
	body := string(existing)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	// A config can hold an API key, so keep whatever mode the user has on it
	// rather than widening it to the default.
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(body+addition), mode); err != nil {
		m.status = "Could not write " + path + ": " + collapseWhitespace(err.Error())
		return m, nil
	}
	cfg, err := config.LoadOrCreate(path)
	if err != nil {
		m.status = "Wrote the settings, but reloading failed: " + collapseWhitespace(err.Error())
		return m, nil
	}
	m.cfg = cfg
	m.status = "Added [llm] settings to " + path + " — open :config to fill in a provider"
	return m, nil
}

// configPredatesLLMSection reports whether the config file on disk has no
// [llm] section at all — as opposed to one that is present but empty — so an
// unconfigured LLM can point at ":llm init" only when that is actually the
// fix.
func (m Model) configPredatesLLMSection() bool {
	path := strings.TrimSpace(m.configPath)
	if path == "" {
		return false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return !hasTOMLSection(string(body), "llm")
}

// hasTOMLSection reports whether an uncommented [name] table header is present.
func hasTOMLSection(body, name string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "["+name+"]") {
			return true
		}
	}
	return false
}

func (m Model) handleLLMPing(msg llmPingMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "LLM test failed — " + collapseWhitespace(msg.err.Error())
		return m, nil
	}
	reply := collapseWhitespace(msg.resp.Text)
	if reply == "" {
		reply = "(empty reply)"
	}
	reply = truncateTextWidth(reply, llmReplyPreview)
	m.status = fmt.Sprintf("LLM ok · %s · %s · %q",
		msg.resp.Model, msg.resp.Elapsed.Round(time.Millisecond), reply)
	return m, nil
}

// collapseWhitespace folds a multi-line message into the single line the
// status bar can show.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
