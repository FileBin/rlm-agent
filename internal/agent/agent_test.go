package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"rlm-agent/internal/llm"
	"rlm-agent/internal/response"
)

// captured is the request the fake server last received. The handler runs on
// the server's goroutine, hence the mutex.
type captured struct {
	mu   sync.Mutex
	raw  []byte
	hits int
}

func (c *captured) request() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.raw
}

func (c *captured) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

// newServer starts a fake chat-completions backend that always answers with
// content as the assistant message and records the request body. The URL is
// the OpenAI-compatible base, so the client appends /chat/completions itself.
func newServer(t *testing.T, content string) (*httptest.Server, *captured) {
	t.Helper()

	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		cap.mu.Lock()
		cap.raw = body
		cap.hits++
		cap.mu.Unlock()

		// The answer is wrapped in the OpenAI envelope: the client reads
		// choices[0].message.content and parses that string itself.
		resp := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{"role": "assistant", "content": content},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

// decodeRequest decodes the captured body into the llm request shape.
func decodeRequest(t *testing.T, cap *captured) llm.ChatRequest {
	t.Helper()
	var req llm.ChatRequest
	if err := json.Unmarshal(cap.request(), &req); err != nil {
		t.Fatalf("decode captured request: %v (body=%s)", err, cap.request())
	}
	return req
}

// prompts is the role table shared by the tests.
func prompts() map[string]string {
	return map[string]string{
		"planner":   "You are the planner.",
		"developer": "You are the developer.",
		"tester":    "You are the tester.",
	}
}

// planningDone is the answer a planner is expected to give in the happy path.
const planningDone = `{"status":"planning_done","files":[{"path":"/workspace/plan/todo.md","content":"x"}]}`

// TestRunJSONSchema covers the vLLM path: the request must ask for a real JSON
// schema and the parsed answer must carry status and files.
func TestRunJSONSchema(t *testing.T) {
	srv, cap := newServer(t, planningDone)
	ag := New(llm.New(srv.URL+"/v1", ""), "test-model", prompts(), true)

	resp, err := ag.Run(context.Background(), "planner", "plan the task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Status != "planning_done" {
		t.Errorf("Status = %q, want planning_done", resp.Status)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(resp.Files))
	}
	if got, want := resp.Files[0].Path, "/workspace/plan/todo.md"; got != want {
		t.Errorf("Files[0].Path = %q, want %q", got, want)
	}

	req := decodeRequest(t, cap)
	if req.ResponseFormat == nil {
		t.Fatal("ResponseFormat is nil, want json_schema")
	}
	if got, want := req.ResponseFormat.Type, "json_schema"; got != want {
		t.Errorf("response_format.type = %q, want %q", got, want)
	}

	// The schema must be the vLLM envelope, named after the role.
	var env struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(req.ResponseFormat.JSONSchema, &env); err != nil {
		t.Fatalf("json_schema is not the {name,schema} envelope: %v (%s)", err, req.ResponseFormat.JSONSchema)
	}
	if got, want := env.Name, "agent_planner"; got != want {
		t.Errorf("json_schema.name = %q, want %q", got, want)
	}
	if len(env.Schema) == 0 || !json.Valid(env.Schema) {
		t.Errorf("json_schema.schema is missing or invalid: %s", env.Schema)
	}

	// Prompt assembly: system prompt of the role first, task second.
	if got, want := req.Model, "test-model"; got != want {
		t.Errorf("model = %q, want %q", got, want)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "You are the planner." {
		t.Errorf("messages[0] = %+v, want planner system prompt", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content != "plan the task" {
		t.Errorf("messages[1] = %+v, want user task prompt", req.Messages[1])
	}
}

// TestRunJSONObject covers the DeepSeek path: plain JSON mode, no schema.
func TestRunJSONObject(t *testing.T) {
	srv, cap := newServer(t, `{"status":"done"}`)
	ag := New(llm.New(srv.URL+"/v1", ""), "deepseek-chat", prompts(), false)

	resp, err := ag.Run(context.Background(), "developer", "write the code")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Status != "done" {
		t.Errorf("Status = %q, want done", resp.Status)
	}

	req := decodeRequest(t, cap)
	if req.ResponseFormat == nil {
		t.Fatal("ResponseFormat is nil, want json_object")
	}
	if got, want := req.ResponseFormat.Type, "json_object"; got != want {
		t.Errorf("response_format.type = %q, want %q", got, want)
	}
	if len(req.ResponseFormat.JSONSchema) != 0 {
		t.Errorf("json_schema = %s, want omitted in json_object mode", req.ResponseFormat.JSONSchema)
	}
}

// TestRunUnknownRole checks that a missing prompt fails before any request: the
// model must never be asked to act without instructions.
func TestRunUnknownRole(t *testing.T) {
	srv, cap := newServer(t, planningDone)
	ag := New(llm.New(srv.URL+"/v1", ""), "test-model", prompts(), true)

	tests := []struct {
		name string
		role string
	}{
		{name: "absent role", role: "reviewer"},
		{name: "empty role", role: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := ag.Run(context.Background(), tc.role, "do something")
			if err == nil {
				t.Fatalf("Run(%q) = %+v, want error", tc.role, resp)
			}
			if !errors.Is(err, ErrUnknownRole) {
				t.Errorf("err = %v, want ErrUnknownRole", err)
			}
			if resp != nil {
				t.Errorf("resp = %+v, want nil", resp)
			}
		})
	}
	if n := cap.count(); n != 0 {
		t.Errorf("server got %d requests, want 0", n)
	}
}

// TestRunInvalidJSON checks that a non-JSON answer surfaces as ErrInvalidJSON
// rather than as a half-filled Response.
func TestRunInvalidJSON(t *testing.T) {
	srv, _ := newServer(t, "sorry, I cannot do that")
	ag := New(llm.New(srv.URL+"/v1", ""), "test-model", prompts(), false)

	resp, err := ag.Run(context.Background(), "tester", "run the tests")
	if err == nil {
		t.Fatalf("Run = %+v, want error", resp)
	}
	if !errors.Is(err, response.ErrInvalidJSON) {
		t.Fatalf("err = %v, want response.ErrInvalidJSON", err)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil", resp)
	}
}

// TestRunEmptyStatus checks that valid JSON violating the contract is rejected:
// Parse, not the transport, decides what a usable answer is.
func TestRunEmptyStatus(t *testing.T) {
	srv, _ := newServer(t, `{"status":"","summary":"looks fine to me"}`)
	ag := New(llm.New(srv.URL+"/v1", ""), "test-model", prompts(), true)

	resp, err := ag.Run(context.Background(), "planner", "plan the task")
	if err == nil {
		t.Fatalf("Run = %+v, want error", resp)
	}
	if !errors.Is(err, response.ErrEmptyStatus) {
		t.Fatalf("err = %v, want response.ErrEmptyStatus", err)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil", resp)
	}
}

// TestRunTransportError checks the pass-through of a client failure: a non-200
// backend answer must not be mistaken for a parse error.
func TestRunTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	ag := New(llm.New(srv.URL+"/v1", ""), "test-model", prompts(), false)
	if _, err := ag.Run(context.Background(), "planner", "plan the task"); err == nil {
		t.Fatal("Run = nil error, want transport error")
	} else if strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("err = %v, want a transport error, not a parse error", err)
	}
}
