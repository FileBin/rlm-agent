// Package agent turns an LLM client into an orchestrator agent.
//
// An agent is one role of the project — "planner", "developer", "tester" —
// bound to the system prompt that describes it. Run puts that prompt in front
// of the task, asks the client for a completion and hands the answer to
// response.Parse, so the orchestrator only ever sees a validated Response.
//
// The struct deliberately does not import internal/orchestrator: it satisfies
// orchestrator.Agent structurally through the Run method, which keeps the
// dependency pointing one way (orchestrator -> agent) and lets the agent be
// tested without a state machine.
package agent

import (
	"context"
	"errors"
	"fmt"

	"rlm-agent/internal/llm"
	"rlm-agent/internal/response"
)

// ErrUnknownRole means Run was asked for a role that has no system prompt.
var ErrUnknownRole = errors.New("agent: unknown role")

// zeroTemp is the temperature the agent requests so retries replay
// deterministically. A pointer makes the zero value serialize explicitly
// (float64 with omitempty would drop it and the server default would apply).
var zeroTemp = 0.0

// Agent is one role of the orchestrator.
//
// client and model are fixed for the lifetime of the agent: a role is tied to
// one backend, and switching models mid-task would change the shape of the
// answers the orchestrator already planned around. prompts is read-only after
// New, so an Agent is safe for concurrent use.
type Agent struct {
	client     *llm.Client
	model      string
	prompts    map[string]string
	jsonSchema bool
}

// New returns an Agent that answers as the role named in Run.
//
// prompts maps a role name to its system prompt. jsonSchema selects the way
// structured output is requested: true asks for a real JSON schema via
// response_format {"type":"json_schema"}, which is what vLLM enforces with
// guided decoding; false asks for {"type":"json_object"}, the only mode
// DeepSeek offers, where the schema has to be restated in the prompt itself.
func New(client *llm.Client, model string, prompts map[string]string, jsonSchema bool) *Agent {
	return &Agent{
		client:     client,
		model:      model,
		prompts:    prompts,
		jsonSchema: jsonSchema,
	}
}

// Run performs one agent turn: it sends the role's system prompt plus prompt to
// the model and returns the parsed answer.
//
// A role without a prompt is a configuration error, not a model error: running
// it would ask the model to act without instructions, so it fails before any
// request is made. Transport failures and parse failures are both returned
// as-is (wrapped only where extra context helps), which lets the caller use
// errors.Is against response.ErrInvalidJSON and friends.
func (a *Agent) Run(ctx context.Context, role, prompt string) (*response.Response, error) {
	sys := a.prompts[role]
	if sys == "" {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}

	req := llm.ChatRequest{
		Model: a.model,
		Messages: []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: prompt},
		},
		// Deterministic output: the orchestrator replays prompts on retry and
		// must not get a different plan each time.
		Temperature:    &zeroTemp,
		ResponseFormat: a.responseFormat(role),
	}

	content, err := a.client.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return response.Parse(content)
}

// responseFormat builds the structured-output request for role. The schema name
// carries the role so a server log shows which agent asked for what.
func (a *Agent) responseFormat(role string) *llm.ResponseFormat {
	if a.jsonSchema {
		return &llm.ResponseFormat{
			Type:       "json_schema",
			JSONSchema: response.WrapSchema("agent_" + role),
		}
	}
	return &llm.ResponseFormat{Type: "json_object"}
}
