package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// okResponse is a minimal successful OpenAI-shaped reply.
const okResponse = `{"choices":[{"message":{"role":"assistant","content":"hello from the model"}}]}`

// recorder captures the last request the test server saw. The mutex guards
// access because the handler runs on the server's own goroutine.
type recorder struct {
	mu     sync.Mutex
	method string
	path   string
	header http.Header
	body   []byte
}

func (r *recorder) capture(req *http.Request) error {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.method = req.Method
	r.path = req.URL.Path
	r.header = req.Header.Clone()
	r.body = body
	return nil
}

func (r *recorder) snapshot() (method, path string, header http.Header, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.method, r.path, r.header.Clone(), append([]byte(nil), r.body...)
}

// newServer starts a test server that records the request and replies with the
// given status and body. It performs no external I/O.
func newServer(t *testing.T, status int, reply string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := rec.capture(r); err != nil {
			http.Error(w, "cannot read request body", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestChatSendsRequestFields(t *testing.T) {
	srv, rec := newServer(t, http.StatusOK, okResponse)
	c := New(srv.URL+"/v1", "")

	temp := 0.2
	_, err := c.Chat(context.Background(), ChatRequest{
		Model: "qwen3-32b",
		Messages: []Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "say hi"},
		},
		Temperature: &temp,
		MaxTokens:   128,
		ResponseFormat: &ResponseFormat{
			Type:       "json_schema",
			JSONSchema: json.RawMessage(`{"name":"answer","schema":{"type":"object"}}`),
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v, want nil", err)
	}

	method, _, _, raw := rec.snapshot()
	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}

	var got struct {
		Model       string          `json:"model"`
		Messages    []Message       `json:"messages"`
		Temperature float64         `json:"temperature"`
		MaxTokens   int             `json:"max_tokens"`
		Format      *ResponseFormat `json:"response_format"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("request body is not valid JSON: %v\nbody: %s", err, raw)
	}

	if got.Model != "qwen3-32b" {
		t.Errorf("model = %q, want %q", got.Model, "qwen3-32b")
	}
	if len(got.Messages) != 2 {
		t.Fatalf("got %d messages, want 2: %s", len(got.Messages), raw)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be terse" {
		t.Errorf("messages[0] = %+v, want the system prompt", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "say hi" {
		t.Errorf("messages[1] = %+v, want the user prompt", got.Messages[1])
	}
	if got.Temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2", got.Temperature)
	}
	if got.MaxTokens != 128 {
		t.Errorf("max_tokens = %d, want 128", got.MaxTokens)
	}
	if got.Format == nil {
		t.Fatalf("response_format missing from %s", raw)
	}
	if got.Format.Type != "json_schema" {
		t.Errorf("response_format.type = %q, want %q", got.Format.Type, "json_schema")
	}
	if string(got.Format.JSONSchema) != `{"name":"answer","schema":{"type":"object"}}` {
		t.Errorf("response_format.json_schema = %s, want it passed through verbatim", got.Format.JSONSchema)
	}
}

func TestChatOmitsOptionalFields(t *testing.T) {
	srv, rec := newServer(t, http.StatusOK, okResponse)
	c := New(srv.URL, "")

	if _, err := c.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "ping"}},
	}); err != nil {
		t.Fatalf("Chat() error = %v, want nil", err)
	}

	_, _, _, raw := rec.snapshot()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	for _, key := range []string{"temperature", "max_tokens", "response_format"} {
		if _, ok := fields[key]; ok {
			t.Errorf("zero-valued %q was sent: %s", key, raw)
		}
	}
}

func TestChatReturnsContent(t *testing.T) {
	srv, _ := newServer(t, http.StatusOK, okResponse)
	c := New(srv.URL, "")

	got, err := c.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "say hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v, want nil", err)
	}
	if want := "hello from the model"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestChatHTTPErrorIncludesBody(t *testing.T) {
	const errBody = `{"error":{"message":"model overloaded","type":"server_error"}}`
	srv, _ := newServer(t, http.StatusInternalServerError, errBody)
	c := New(srv.URL, "secret")

	got, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen3-32b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("Chat() = %q, nil; want an error on HTTP 500", got)
	}
	if got != "" {
		t.Errorf("content = %q, want empty on error", got)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want it to mention the status code", err)
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Errorf("error = %q, want it to include the response body", err)
	}
}

func TestChatEmptyChoices(t *testing.T) {
	srv, _ := newServer(t, http.StatusOK, `{"choices":[]}`)
	c := New(srv.URL, "")

	if got, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen3-32b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}); err == nil {
		t.Fatalf("Chat() = %q, nil; want an error for an empty choices list", got)
	}
}

func TestChatEmptyContent(t *testing.T) {
	srv, _ := newServer(t, http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	c := New(srv.URL, "")

	if got, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen3-32b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}); err == nil {
		t.Fatalf("Chat() = %q, nil; want an error for empty content", got)
	}
}

func TestAuthorizationHeader(t *testing.T) {
	tests := []struct {
		name      string
		apiKey    string
		wantValue string
		wantSent  bool
	}{
		{name: "with key", apiKey: "sk-test-123", wantValue: "Bearer sk-test-123", wantSent: true},
		{name: "without key", apiKey: "", wantSent: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := newServer(t, http.StatusOK, okResponse)
			c := New(srv.URL, tc.apiKey)

			if _, err := c.Chat(context.Background(), ChatRequest{
				Model:    "deepseek-chat",
				Messages: []Message{{Role: "user", Content: "hi"}},
			}); err != nil {
				t.Fatalf("Chat() error = %v, want nil", err)
			}

			_, _, header, _ := rec.snapshot()
			values, sent := header["Authorization"]
			if sent != tc.wantSent {
				t.Fatalf("Authorization sent = %v, want %v", sent, tc.wantSent)
			}
			if tc.wantSent {
				if len(values) != 1 || values[0] != tc.wantValue {
					t.Errorf("Authorization = %v, want [%q]", values, tc.wantValue)
				}
			}
			if ct := header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

func TestChatPathJoinsBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL func(*httptest.Server) string
	}{
		{name: "versioned base", baseURL: func(s *httptest.Server) string { return s.URL + "/v1" }},
		{name: "trailing slash trimmed", baseURL: func(s *httptest.Server) string { return s.URL + "/v1/" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := newServer(t, http.StatusOK, okResponse)
			c := New(tc.baseURL(srv), "")

			if _, err := c.Chat(context.Background(), ChatRequest{
				Model:    "deepseek-chat",
				Messages: []Message{{Role: "user", Content: "hi"}},
			}); err != nil {
				t.Fatalf("Chat() error = %v, want nil", err)
			}

			_, path, _, _ := rec.snapshot()
			if want := "/v1/chat/completions"; path != want {
				t.Errorf("path = %q, want %q", path, want)
			}
		})
	}
}

func TestChatRespectsContextCancellation(t *testing.T) {
	srv, _ := newServer(t, http.StatusOK, okResponse)
	c := New(srv.URL, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Chat(ctx, ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}); err == nil {
		t.Fatal("Chat() error = nil, want a cancellation error")
	}
}
