package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Provider names accepted by [llm].provider. ProviderCompatible covers any
// gateway that speaks OpenAI's /chat/completions wire format (LM Studio,
// vLLM, OpenRouter, Groq, llama.cpp's server, …) and differs from
// ProviderOpenAI only in that it has no default base URL.
const (
	ProviderOpenAI     = "openai"
	ProviderCompatible = "openai-compatible"
	ProviderOllama     = "ollama"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	defaultOllamaBaseURL = "http://localhost:11434"
	defaultLLMTimeout    = 60
	maxLLMTimeout        = 600
)

// LLM configures the optional connection to a chat-completion API. bada speaks
// two wire formats: OpenAI's POST {base_url}/chat/completions (providers
// "openai" and "openai-compatible") and Ollama's native POST
// {base_url}/api/chat. Nothing is contacted until a command asks for it.
type LLM struct {
	// Provider selects the wire format and the default endpoint:
	// "openai", "openai-compatible", or "ollama". Empty disables the feature.
	Provider string `toml:"provider"`
	// BaseURL overrides the provider default. Like the OpenAI SDKs, it must
	// include the version prefix (".../v1") for the OpenAI wire format.
	BaseURL string `toml:"base_url"`
	// Model is the model id sent with every request, e.g. "gpt-4o-mini" or
	// "llama3.2". Required.
	Model string `toml:"model"`
	// APIKey is either the key itself or an indirection — "env:OPENAI_API_KEY"
	// or "${OPENAI_API_KEY}" — resolved at request time so the secret is never
	// written back when bada rewrites this file.
	APIKey string `toml:"api_key"`
	// APIKeyEnv names an environment variable to read the key from. It is the
	// recommended way to keep the key out of the config file entirely.
	APIKeyEnv string `toml:"api_key_env"`
	// TimeoutSeconds bounds a single request. 0 means the built-in default.
	TimeoutSeconds int `toml:"timeout_seconds"`
	// MaxTokens caps the reply length. 0 leaves it to the provider.
	MaxTokens int `toml:"max_tokens"`
	// Temperature is omitted from the request when unset, which matters for
	// reasoning models that reject any value but their own.
	Temperature *float64 `toml:"temperature"`
	// Headers are extra HTTP headers, for gateways that require them (e.g.
	// OpenRouter's HTTP-Referer). They cannot override Authorization.
	Headers map[string]string `toml:"headers"`
}

// NormalizeProvider maps the accepted spellings onto a canonical provider name.
// The second result is false for an unknown name.
func NormalizeProvider(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ProviderOpenAI:
		return ProviderOpenAI, true
	case ProviderCompatible, "compatible", "openai_compatible", "openai-compat":
		return ProviderCompatible, true
	case ProviderOllama:
		return ProviderOllama, true
	}
	return "", false
}

// Configured reports whether the user has asked for an LLM connection at all.
// It says nothing about whether that configuration is usable — see Validate.
func (l LLM) Configured() bool {
	return strings.TrimSpace(l.Provider) != ""
}

// ResolvedBaseURL is the endpoint root for the configured provider, with any
// trailing slash removed. It is empty when neither the config nor the provider
// supplies one.
func (l LLM) ResolvedBaseURL() string {
	if base := strings.TrimSpace(l.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	provider, _ := NormalizeProvider(l.Provider)
	switch provider {
	case ProviderOpenAI:
		return defaultOpenAIBaseURL
	case ProviderOllama:
		return defaultOllamaBaseURL
	}
	return ""
}

// RequestTimeout bounds a single request, clamped to something sane.
func (l LLM) RequestTimeout() time.Duration {
	secs := l.TimeoutSeconds
	if secs <= 0 {
		secs = defaultLLMTimeout
	}
	if secs > maxLLMTimeout {
		secs = maxLLMTimeout
	}
	return time.Duration(secs) * time.Second
}

// ResolveAPIKey returns the key to authenticate with, reading it from the
// environment when the config points there rather than holding it inline.
// A missing key is not an error here; only providers that need one complain.
func (l LLM) ResolveAPIKey() (string, error) {
	if raw := strings.TrimSpace(l.APIKey); raw != "" {
		name, indirect := envVarRef(raw)
		if !indirect {
			return raw, nil
		}
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("[llm].api_key points at $%s, which is unset or empty", name)
	}
	if name := strings.TrimSpace(l.APIKeyEnv); name != "" {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("[llm].api_key_env names $%s, which is unset or empty", name)
	}
	// Fall back to the conventional variable, so a plain `provider = "openai"`
	// works for anyone who already exports it.
	if provider, _ := NormalizeProvider(l.Provider); provider == ProviderOpenAI {
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")), nil
	}
	return "", nil
}

// envVarRef recognizes the "env:NAME" and "${NAME}" indirections and returns
// the variable name. The second result is false for a literal value.
func envVarRef(raw string) (string, bool) {
	if name, ok := strings.CutPrefix(raw, "env:"); ok {
		return strings.TrimSpace(name), true
	}
	if name, ok := strings.CutPrefix(raw, "${"); ok {
		if name, closed := strings.CutSuffix(name, "}"); closed {
			return strings.TrimSpace(name), true
		}
	}
	return "", false
}

// Validate reports what, if anything, keeps the current settings from being
// usable. The message is shown to the user, so it names the field to fix.
func (l LLM) Validate() error {
	if !l.Configured() {
		return errors.New("no LLM configured; set [llm].provider in the config file")
	}
	provider, ok := NormalizeProvider(l.Provider)
	if !ok {
		return fmt.Errorf("unknown [llm].provider %q; use %q, %q, or %q",
			l.Provider, ProviderOpenAI, ProviderCompatible, ProviderOllama)
	}
	if strings.TrimSpace(l.Model) == "" {
		return errors.New("no [llm].model set")
	}
	base := l.ResolvedBaseURL()
	if base == "" {
		return fmt.Errorf("[llm].base_url is required for provider %q", provider)
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("[llm].base_url must start with http:// or https://, got %q", base)
	}
	key, err := l.ResolveAPIKey()
	if err != nil {
		return err
	}
	if key == "" && provider == ProviderOpenAI {
		return errors.New("no API key; set [llm].api_key_env or export OPENAI_API_KEY")
	}
	return nil
}

// Summary is a one-line, secret-free description of the connection, for the
// status bar: `openai · gpt-4o-mini · https://api.openai.com/v1 · key: set`.
func (l LLM) Summary() string {
	if !l.Configured() {
		return "not configured"
	}
	provider, ok := NormalizeProvider(l.Provider)
	if !ok {
		provider = l.Provider + " (unknown)"
	}
	model := strings.TrimSpace(l.Model)
	if model == "" {
		model = "no model"
	}
	base := l.ResolvedBaseURL()
	if base == "" {
		base = "no base_url"
	}
	parts := []string{provider, model, base}
	if provider != ProviderOllama {
		key, err := l.ResolveAPIKey()
		switch {
		case err != nil:
			parts = append(parts, "key: unresolved")
		case key == "":
			parts = append(parts, "key: missing")
		default:
			parts = append(parts, "key: set")
		}
	}
	return strings.Join(parts, " · ")
}
