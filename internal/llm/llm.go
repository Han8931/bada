// Package llm talks to a chat-completion API. It speaks two wire formats —
// OpenAI's /chat/completions (which every "OpenAI-compatible" gateway also
// implements) and Ollama's native /api/chat — behind one Client, so callers
// need not care which provider the user configured.
//
// Nothing here starts a request on its own: the UI builds a Client per command
// and throws it away, so a config reload always takes effect immediately.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"bada/internal/config"
)

// Roles accepted by both wire formats.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn of a conversation.
type Message struct {
	Role    string
	Content string
}

// System and User build the two message kinds callers actually construct.
func System(content string) Message { return Message{Role: RoleSystem, Content: content} }
func User(content string) Message   { return Message{Role: RoleUser, Content: content} }

// Response is a completed reply, normalized across providers. Token counts are
// zero when the provider does not report them.
type Response struct {
	Text             string
	Model            string
	PromptTokens     int
	CompletionTokens int
	Elapsed          time.Duration
}

// Client is a configured connection to one provider. Construct it with New.
type Client struct {
	provider string
	baseURL  string
	model    string
	apiKey   string
	headers  map[string]string
	maxToks  int
	temp     *float64
	http     *http.Client
}

// New validates the config and returns a client for it. The error is written
// for the user: it names the setting to fix.
func New(cfg config.LLM) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	provider, _ := config.NormalizeProvider(cfg.Provider)
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return nil, err
	}
	return &Client{
		provider: provider,
		baseURL:  cfg.ResolvedBaseURL(),
		model:    strings.TrimSpace(cfg.Model),
		apiKey:   key,
		headers:  cfg.Headers,
		maxToks:  cfg.MaxTokens,
		temp:     cfg.Temperature,
		http:     &http.Client{Timeout: cfg.RequestTimeout()},
	}, nil
}

// Provider and Model describe the client without exposing its credentials.
func (c *Client) Provider() string { return c.provider }
func (c *Client) Model() string    { return c.model }

// Chat sends the conversation and returns the assistant's reply. The context
// bounds the call in addition to the configured timeout.
func (c *Client) Chat(ctx context.Context, msgs []Message) (Response, error) {
	if len(msgs) == 0 {
		return Response{}, fmt.Errorf("llm: no messages to send")
	}
	start := time.Now()
	var (
		resp Response
		err  error
	)
	if c.provider == config.ProviderOllama {
		resp, err = c.chatOllama(ctx, msgs)
	} else {
		resp, err = c.chatOpenAI(ctx, msgs)
	}
	if err != nil {
		return Response{}, err
	}
	resp.Elapsed = time.Since(start)
	if resp.Model == "" {
		resp.Model = c.model
	}
	return resp, nil
}

// Ping is the cheapest round-trip that proves the whole path works: the base
// URL resolves, the credentials are accepted, and the model exists.
func (c *Client) Ping(ctx context.Context) (Response, error) {
	return c.Chat(ctx, []Message{
		System("You are a connection test. Reply with the single word: ok."),
		User("ping"),
	})
}

// --- OpenAI wire format ------------------------------------------------------

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// toOpenAIMessages converts the provider-agnostic conversation into the wire
// shape both OpenAI-compatible and Ollama chat endpoints take.
func toOpenAIMessages(msgs []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, openAIMessage{Role: m.Role, Content: m.Content})
	}
	return out
}

type openAIResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *Client) chatOpenAI(ctx context.Context, msgs []Message) (Response, error) {
	body := openAIRequest{
		Model:       c.model,
		Messages:    toOpenAIMessages(msgs),
		MaxTokens:   c.maxToks,
		Temperature: c.temp,
	}
	raw, err := c.post(ctx, c.baseURL+"/chat/completions", body)
	if err != nil {
		return Response{}, err
	}
	var parsed openAIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("llm: unreadable response from %s: %w", c.baseURL, err)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("llm: %s returned no choices", c.baseURL)
	}
	return Response{
		Text:             strings.TrimSpace(parsed.Choices[0].Message.Content),
		Model:            parsed.Model,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
	}, nil
}

// --- Ollama wire format ------------------------------------------------------

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

type ollamaOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
}

type ollamaResponse struct {
	Model   string        `json:"model"`
	Message openAIMessage `json:"message"`
	// Ollama names its token counts differently from OpenAI.
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

func (c *Client) chatOllama(ctx context.Context, msgs []Message) (Response, error) {
	body := ollamaRequest{
		Model:    c.model,
		Messages: toOpenAIMessages(msgs),
		// bada renders a reply only once it is complete, so streaming would buy
		// nothing but a multi-object body to reassemble.
		Stream: false,
	}
	if c.temp != nil || c.maxToks > 0 {
		body.Options = &ollamaOptions{Temperature: c.temp, NumPredict: c.maxToks}
	}
	raw, err := c.post(ctx, c.baseURL+"/api/chat", body)
	if err != nil {
		return Response{}, err
	}
	var parsed ollamaResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("llm: unreadable response from %s: %w", c.baseURL, err)
	}
	return Response{
		Text:             strings.TrimSpace(parsed.Message.Content),
		Model:            parsed.Model,
		PromptTokens:     parsed.PromptEvalCount,
		CompletionTokens: parsed.EvalCount,
	}, nil
}

// --- transport ---------------------------------------------------------------

// maxErrBody caps how much of a failed response is quoted back to the user; a
// misrouted base URL can otherwise return an entire HTML page.
const maxErrBody = 400

func (c *Client) post(ctx context.Context, url string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("llm: cannot encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("llm: bad request URL %q: %w", url, err)
	}
	// User headers go on first so they cannot displace Content-Type or
	// Authorization below; everything else, including User-Agent, is theirs
	// to override.
	req.Header.Set("User-Agent", "bada")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Keep the cause wrapped so callers can test for a timeout, unless
		// redaction had something to remove — then the sanitized text wins.
		if safe := c.redact(err.Error()); safe != err.Error() {
			return nil, fmt.Errorf("llm: %s unreachable: %s", c.baseURL, safe)
		}
		return nil, fmt.Errorf("llm: %s unreachable: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("llm: cannot read response from %s: %w", c.baseURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{
			Status:   resp.StatusCode,
			Endpoint: url,
			Message:  c.redact(describeFailure(resp.StatusCode, raw)),
		}
	}
	return raw, nil
}

// redact strips the API key from anything about to be shown to the user, in
// case a gateway echoes the Authorization header back in an error.
func (c *Client) redact(s string) string {
	if c.apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.apiKey, "«api key»")
}

// APIError is a non-2xx reply. It keeps the status so callers can distinguish
// "your key is wrong" from "the server is down".
type APIError struct {
	Status   int
	Endpoint string
	Message  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("llm: %s returned %d: %s", e.Endpoint, e.Status, e.Message)
}

// describeFailure turns an error body into one readable line, preferring the
// provider's own message and adding a hint for the statuses users hit most.
func describeFailure(status int, raw []byte) string {
	msg := providerErrorMessage(raw)
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	msg = collapse(msg)
	if r := []rune(msg); len(r) > maxErrBody {
		msg = string(r[:maxErrBody]) + "…"
	}
	if hint := statusHint(status); hint != "" {
		if msg == "" {
			return hint
		}
		return msg + " (" + hint + ")"
	}
	if msg == "" {
		return http.StatusText(status)
	}
	return msg
}

// providerErrorMessage digs the human-readable text out of the two error
// shapes in the wild: OpenAI's {"error":{"message":…}} and Ollama's
// {"error":"…"}.
func providerErrorMessage(raw []byte) string {
	var openAIShape struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &openAIShape); err == nil && openAIShape.Error.Message != "" {
		return openAIShape.Error.Message
	}
	var ollamaShape struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &ollamaShape); err == nil && ollamaShape.Error != "" {
		return ollamaShape.Error
	}
	return ""
}

func statusHint(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "check [llm].api_key / api_key_env"
	case http.StatusNotFound:
		return "check [llm].base_url and model"
	case http.StatusTooManyRequests:
		return "rate limited; retry shortly"
	}
	return ""
}

// collapse folds whitespace so a multi-line body fits the one-line status bar.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
