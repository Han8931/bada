package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"bada/internal/config"
)

// capture records what the fake provider received, so the tests can assert on
// the request bada actually puts on the wire.
type capture struct {
	path   string
	auth   string
	header http.Header
	body   map[string]any
}

// fakeProvider serves one canned reply and records the request.
func fakeProvider(t *testing.T, status int, reply string) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		got.header = r.Header.Clone()
		_ = json.Unmarshal(raw, &got.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func mustClient(t *testing.T, cfg config.LLM) *Client {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestChatOpenAI(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK, `{
		"model": "gpt-4o-mini-2024",
		"choices": [{"message": {"role": "assistant", "content": "  hello there\n"}}],
		"usage": {"prompt_tokens": 11, "completion_tokens": 3}
	}`)
	temp := 0.4
	client := mustClient(t, config.LLM{
		Provider:    "openai-compatible",
		BaseURL:     srv.URL + "/v1",
		Model:       "gpt-4o-mini",
		APIKey:      "sk-test",
		MaxTokens:   256,
		Temperature: &temp,
		Headers:     map[string]string{"X-Title": "bada"},
	})

	resp, err := client.Chat(context.Background(), []Message{System("be brief"), User("hi")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", got.path)
	}
	if got.auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got.auth)
	}
	if got.header.Get("X-Title") != "bada" {
		t.Errorf("configured header not sent: %v", got.header)
	}
	if got.body["model"] != "gpt-4o-mini" {
		t.Errorf("model = %v", got.body["model"])
	}
	if got.body["max_tokens"] != float64(256) {
		t.Errorf("max_tokens = %v", got.body["max_tokens"])
	}
	if got.body["temperature"] != 0.4 {
		t.Errorf("temperature = %v", got.body["temperature"])
	}
	msgs, _ := got.body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", got.body["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("first message = %v", first)
	}

	if resp.Text != "hello there" {
		t.Errorf("Text = %q, want the trimmed reply", resp.Text)
	}
	if resp.Model != "gpt-4o-mini-2024" {
		t.Errorf("Model = %q, want the model the server reported", resp.Model)
	}
	if resp.PromptTokens != 11 || resp.CompletionTokens != 3 {
		t.Errorf("token counts = %d/%d", resp.PromptTokens, resp.CompletionTokens)
	}
	if resp.Elapsed <= 0 {
		t.Error("Elapsed should be measured")
	}
}

// Unset options must be absent from the request, not sent as zeros: a
// temperature of 0 is a real setting, and some models reject one outright.
func TestChatOpenAIOmitsUnsetOptions(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK,
		`{"choices": [{"message": {"content": "ok"}}]}`)
	client := mustClient(t, config.LLM{
		Provider: "openai-compatible",
		BaseURL:  srv.URL + "/v1",
		Model:    "m",
	})
	if _, err := client.Chat(context.Background(), []Message{User("hi")}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := got.body["temperature"]; ok {
		t.Error("an unset temperature must be omitted from the request")
	}
	if _, ok := got.body["max_tokens"]; ok {
		t.Error("an unset max_tokens must be omitted from the request")
	}
	if got.auth != "" {
		t.Errorf("no key configured, but Authorization = %q", got.auth)
	}
	// With no model reported by the server, the configured one stands in.
	resp, _ := client.Chat(context.Background(), []Message{User("hi")})
	if resp.Model != "m" {
		t.Errorf("Model = %q, want the configured model as a fallback", resp.Model)
	}
}

func TestChatOllama(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK, `{
		"model": "llama3.2",
		"message": {"role": "assistant", "content": "hi"},
		"prompt_eval_count": 7,
		"eval_count": 2
	}`)
	temp := 0.1
	client := mustClient(t, config.LLM{
		Provider:    "ollama",
		BaseURL:     srv.URL,
		Model:       "llama3.2",
		MaxTokens:   64,
		Temperature: &temp,
	})

	resp, err := client.Chat(context.Background(), []Message{User("hi")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.path != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", got.path)
	}
	if got.body["stream"] != false {
		t.Errorf("stream = %v, want false", got.body["stream"])
	}
	opts, _ := got.body["options"].(map[string]any)
	if opts["temperature"] != 0.1 || opts["num_predict"] != float64(64) {
		t.Errorf("options = %v", opts)
	}
	if resp.Text != "hi" || resp.Model != "llama3.2" {
		t.Errorf("response = %+v", resp)
	}
	if resp.PromptTokens != 7 || resp.CompletionTokens != 2 {
		t.Errorf("token counts = %d/%d", resp.PromptTokens, resp.CompletionTokens)
	}
}

func TestChatOllamaOmitsOptionsWhenUnset(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK, `{"message": {"content": "hi"}}`)
	client := mustClient(t, config.LLM{Provider: "ollama", BaseURL: srv.URL, Model: "m"})
	if _, err := client.Chat(context.Background(), []Message{User("hi")}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := got.body["options"]; ok {
		t.Errorf("options should be omitted entirely when nothing is set: %v", got.body)
	}
}

func TestPingSendsAProbe(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK, `{"choices": [{"message": {"content": "ok"}}]}`)
	client := mustClient(t, config.LLM{
		Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m",
	})
	resp, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("Text = %q", resp.Text)
	}
	if msgs, _ := got.body["messages"].([]any); len(msgs) == 0 {
		t.Error("Ping should send at least one message")
	}
}

func TestHTTPErrorsAreExplained(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		wants  []string
	}{
		{
			"openai error shape",
			http.StatusUnauthorized,
			`{"error": {"message": "Incorrect API key provided"}}`,
			[]string{"401", "Incorrect API key provided", "api_key"},
		},
		{
			"ollama error shape",
			http.StatusNotFound,
			`{"error": "model 'nope' not found"}`,
			[]string{"404", "model 'nope' not found", "base_url"},
		},
		{
			"unparseable body still surfaces",
			http.StatusBadGateway,
			"<html>nginx</html>",
			[]string{"502", "nginx"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeProvider(t, tc.status, tc.body)
			client := mustClient(t, config.LLM{
				Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m",
			})
			_, err := client.Chat(context.Background(), []Message{User("hi")})
			if err == nil {
				t.Fatal("expected an error")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected an *APIError, got %T", err)
			}
			if apiErr.Status != tc.status {
				t.Errorf("Status = %d, want %d", apiErr.Status, tc.status)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should mention %q", err, want)
				}
			}
		})
	}
}

// A huge error page must not flood the status bar.
func TestErrorBodyIsTruncated(t *testing.T) {
	srv, _ := fakeProvider(t, http.StatusInternalServerError, strings.Repeat("x", 5000))
	client := mustClient(t, config.LLM{
		Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m",
	})
	_, err := client.Chat(context.Background(), []Message{User("hi")})
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > maxErrBody+200 {
		t.Errorf("error is %d bytes; it should be truncated", len(err.Error()))
	}
}

// If a gateway echoes the Authorization header into its error body, the key
// must not reach the user's screen.
func TestErrorsRedactTheAPIKey(t *testing.T) {
	srv, _ := fakeProvider(t, http.StatusBadRequest,
		`{"error": {"message": "bad header: Bearer sk-leaky-secret"}}`)
	client := mustClient(t, config.LLM{
		Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m",
		APIKey: "sk-leaky-secret",
	})
	_, err := client.Chat(context.Background(), []Message{User("hi")})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "sk-leaky-secret") {
		t.Fatalf("error leaked the API key: %v", err)
	}
}

// Configured headers are convenience, not a way to replace the credentials.
func TestConfiguredHeadersCannotOverrideAuth(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK, `{"choices": [{"message": {"content": "ok"}}]}`)
	client := mustClient(t, config.LLM{
		Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m",
		APIKey:  "sk-real",
		Headers: map[string]string{"Authorization": "Bearer sk-spoofed", "Content-Type": "text/plain"},
	})
	if _, err := client.Chat(context.Background(), []Message{User("hi")}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.auth != "Bearer sk-real" {
		t.Errorf("Authorization = %q, want the configured key to win", got.auth)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// Everything but Authorization and Content-Type is the user's to override —
// including User-Agent, e.g. to satisfy a gateway's UA allowlist.
func TestConfiguredHeadersCanOverrideUserAgent(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK, `{"choices": [{"message": {"content": "ok"}}]}`)
	client := mustClient(t, config.LLM{
		Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m",
		Headers: map[string]string{"User-Agent": "custom-agent/1.0"},
	})
	if _, err := client.Chat(context.Background(), []Message{User("hi")}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if ua := got.header.Get("User-Agent"); ua != "custom-agent/1.0" {
		t.Errorf("User-Agent = %q, want the configured header to win", ua)
	}
}

// The default User-Agent still applies when nothing overrides it.
func TestDefaultUserAgent(t *testing.T) {
	srv, got := fakeProvider(t, http.StatusOK, `{"choices": [{"message": {"content": "ok"}}]}`)
	client := mustClient(t, config.LLM{Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m"})
	if _, err := client.Chat(context.Background(), []Message{User("hi")}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if ua := got.header.Get("User-Agent"); ua != "bada" {
		t.Errorf("User-Agent = %q, want the default", ua)
	}
}

// Truncating an error body must not split a multi-byte rune, or the result
// is invalid UTF-8.
func TestErrorBodyTruncationIsRuneSafe(t *testing.T) {
	srv, _ := fakeProvider(t, http.StatusInternalServerError, strings.Repeat("가", 500))
	client := mustClient(t, config.LLM{
		Provider: "openai-compatible", BaseURL: srv.URL + "/v1", Model: "m",
	})
	_, err := client.Chat(context.Background(), []Message{User("hi")})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !utf8.ValidString(err.Error()) {
		t.Errorf("truncated error is not valid UTF-8: %q", err.Error())
	}
}

func TestNewRejectsAnUnusableConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := New(config.LLM{}); err == nil {
		t.Error("expected an unconfigured LLM to be rejected")
	}
	if _, err := New(config.LLM{Provider: "openai", Model: "gpt-4o-mini"}); err == nil {
		t.Error("expected a missing API key to be rejected")
	}
}

func TestChatRejectsAnEmptyConversation(t *testing.T) {
	client := mustClient(t, config.LLM{Provider: "ollama", BaseURL: "http://localhost:1", Model: "m"})
	if _, err := client.Chat(context.Background(), nil); err == nil {
		t.Error("expected an empty conversation to be rejected")
	}
}

func TestUnreachableEndpointIsReported(t *testing.T) {
	srv, _ := fakeProvider(t, http.StatusOK, `{}`)
	url := srv.URL
	srv.Close() // nothing is listening now
	client := mustClient(t, config.LLM{Provider: "ollama", BaseURL: url, Model: "m"})
	_, err := client.Chat(context.Background(), []Message{User("hi")})
	if err == nil {
		t.Fatal("expected an error from a closed endpoint")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should say the endpoint is unreachable, got %v", err)
	}
}
