package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvedBaseURLDefaultsPerProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		llm      LLM
		expected string
	}{
		{"openai default", LLM{Provider: "openai"}, defaultOpenAIBaseURL},
		{"ollama default", LLM{Provider: "ollama"}, defaultOllamaBaseURL},
		{"compatible has none", LLM{Provider: "openai-compatible"}, ""},
		{"explicit wins", LLM{Provider: "openai", BaseURL: "http://box:8000/v1"}, "http://box:8000/v1"},
		{"trailing slash trimmed", LLM{Provider: "ollama", BaseURL: "http://box:1234/"}, "http://box:1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.llm.ResolvedBaseURL(); got != tc.expected {
				t.Errorf("ResolvedBaseURL() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNormalizeProviderAcceptsAliases(t *testing.T) {
	for _, in := range []string{"OpenAI", " openai "} {
		if got, ok := NormalizeProvider(in); !ok || got != ProviderOpenAI {
			t.Errorf("NormalizeProvider(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"compatible", "openai_compatible", "openai-compat"} {
		if got, ok := NormalizeProvider(in); !ok || got != ProviderCompatible {
			t.Errorf("NormalizeProvider(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := NormalizeProvider("bedrock"); ok {
		t.Error("expected an unknown provider to be rejected")
	}
}

func TestResolveAPIKeyIndirections(t *testing.T) {
	t.Setenv("BADA_TEST_KEY", "sk-from-env")
	t.Setenv("OPENAI_API_KEY", "sk-conventional")

	for _, tc := range []struct {
		name     string
		llm      LLM
		expected string
	}{
		{"literal", LLM{Provider: "openai", APIKey: "sk-literal"}, "sk-literal"},
		{"env: prefix", LLM{Provider: "openai", APIKey: "env:BADA_TEST_KEY"}, "sk-from-env"},
		{"${} form", LLM{Provider: "openai", APIKey: "${BADA_TEST_KEY}"}, "sk-from-env"},
		{"api_key_env", LLM{Provider: "openai", APIKeyEnv: "BADA_TEST_KEY"}, "sk-from-env"},
		{"openai falls back to OPENAI_API_KEY", LLM{Provider: "openai"}, "sk-conventional"},
		{"ollama needs no key", LLM{Provider: "ollama"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.llm.ResolveAPIKey()
			if err != nil {
				t.Fatalf("ResolveAPIKey: %v", err)
			}
			if got != tc.expected {
				t.Errorf("ResolveAPIKey() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestResolveAPIKeyReportsMissingEnvVar(t *testing.T) {
	t.Setenv("BADA_TEST_ABSENT", "")
	for _, llm := range []LLM{
		{Provider: "openai", APIKey: "env:BADA_TEST_ABSENT"},
		{Provider: "openai", APIKeyEnv: "BADA_TEST_ABSENT"},
	} {
		if _, err := llm.ResolveAPIKey(); err == nil {
			t.Errorf("expected an error for an unset variable, config %+v", llm)
		} else if !strings.Contains(err.Error(), "BADA_TEST_ABSENT") {
			t.Errorf("error should name the variable, got %v", err)
		}
	}
}

func TestValidate(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	for _, tc := range []struct {
		name    string
		llm     LLM
		wantErr string // substring; empty means the config must validate
	}{
		{"unconfigured", LLM{}, "provider"},
		{"unknown provider", LLM{Provider: "bedrock", Model: "m"}, "unknown"},
		{"no model", LLM{Provider: "openai", APIKey: "k"}, "model"},
		{"compatible needs base_url", LLM{Provider: "openai-compatible", Model: "m", APIKey: "k"}, "base_url"},
		{"base_url needs a scheme", LLM{Provider: "ollama", Model: "m", BaseURL: "box:11434"}, "http://"},
		{"openai needs a key", LLM{Provider: "openai", Model: "gpt-4o-mini"}, "API key"},
		{"ollama without a key is fine", LLM{Provider: "ollama", Model: "llama3.2"}, ""},
		{"openai with a key is fine", LLM{Provider: "openai", Model: "gpt-4o-mini", APIKey: "sk-x"}, ""},
		{"compatible with a base_url is fine", LLM{Provider: "compatible", Model: "m", BaseURL: "http://localhost:1234/v1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.llm.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestSummaryNeverLeaksTheKey(t *testing.T) {
	llm := LLM{Provider: "openai", Model: "gpt-4o-mini", APIKey: "sk-super-secret"}
	summary := llm.Summary()
	if strings.Contains(summary, "sk-super-secret") {
		t.Fatalf("Summary() leaked the key: %q", summary)
	}
	for _, want := range []string{"openai", "gpt-4o-mini", defaultOpenAIBaseURL, "key: set"} {
		if !strings.Contains(summary, want) {
			t.Errorf("Summary() = %q, want it to mention %q", summary, want)
		}
	}
	if got := (LLM{}).Summary(); got != "not configured" {
		t.Errorf("empty Summary() = %q", got)
	}
}

func TestRequestTimeoutClamps(t *testing.T) {
	if got := (LLM{}).RequestTimeout().Seconds(); got != defaultLLMTimeout {
		t.Errorf("default timeout = %v", got)
	}
	if got := (LLM{TimeoutSeconds: 99999}).RequestTimeout().Seconds(); got != maxLLMTimeout {
		t.Errorf("clamped timeout = %v", got)
	}
	if got := (LLM{TimeoutSeconds: 5}).RequestTimeout().Seconds(); got != 5 {
		t.Errorf("explicit timeout = %v", got)
	}
}

// A config file's [llm] section must survive a load, and saving it back must
// not expand an env-var indirection into the file — that would write the
// user's API key to disk behind their back.
func TestLoadAndSaveKeepsKeyIndirection(t *testing.T) {
	t.Setenv("BADA_TEST_KEY", "sk-from-env")
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
db_path = "/tmp/bada.db"

[llm]
provider = "ollama"
model = "llama3.2"
api_key = "env:BADA_TEST_KEY"
timeout_seconds = 12
max_tokens = 256
temperature = 0.4

[llm.headers]
X-Title = "bada"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if cfg.LLM.Provider != "ollama" || cfg.LLM.Model != "llama3.2" {
		t.Fatalf("provider/model not loaded: %+v", cfg.LLM)
	}
	if cfg.LLM.TimeoutSeconds != 12 || cfg.LLM.MaxTokens != 256 {
		t.Errorf("numeric settings not loaded: %+v", cfg.LLM)
	}
	if cfg.LLM.Temperature == nil || *cfg.LLM.Temperature != 0.4 {
		t.Errorf("temperature not loaded: %+v", cfg.LLM.Temperature)
	}
	if cfg.LLM.Headers["X-Title"] != "bada" {
		t.Errorf("headers not loaded: %+v", cfg.LLM.Headers)
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(saved), "sk-from-env") {
		t.Fatalf("Save wrote the resolved key to disk:\n%s", saved)
	}
	if !strings.Contains(string(saved), "env:BADA_TEST_KEY") {
		t.Errorf("Save dropped the key indirection:\n%s", saved)
	}
}

// An unset temperature must stay out of the written config, so the request can
// omit it too.
func TestUnsetTemperatureIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := defaultConfig()
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(saved), "temperature") {
		t.Errorf("a nil temperature should not be written:\n%s", saved)
	}
	if !strings.Contains(string(saved), "[llm]") {
		t.Errorf("the [llm] section should be written for discoverability:\n%s", saved)
	}
}
