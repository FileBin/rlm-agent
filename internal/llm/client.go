// Package llm is a minimal client for OpenAI-compatible chat completions
// endpoints.
//
// It implements the small subset of the protocol shared by vLLM and the
// DeepSeek API, so a local model and a hosted one are reached through the same
// code path: only the base URL and the API key differ.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Base URLs of the supported backends. Both expose an OpenAI-compatible
// /chat/completions endpoint.
const (
	BaseURLVLLM     = "http://nn.home/v1"
	BaseURLDeepSeek = "https://api.deepseek.com/v1"
)

// requestTimeout bounds a single completion call. Generations on a loaded
// local GPU can take minutes, so the limit is generous.
const requestTimeout = 300 * time.Second

// Message is one entry of the chat history.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResponseFormat asks the backend for structured output. JSONSchema is passed
// through verbatim: vLLM expects the full {"name":...,"schema":...} object,
// DeepSeek only the {"type":"json_object"} form.
type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

// ChatRequest is the body of a POST /chat/completions request.
type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// Client talks to one backend. It is safe for concurrent use: the embedded
// http.Client is, and the remaining fields are never mutated after New.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New returns a Client for an OpenAI-compatible baseURL such as BaseURLVLLM.
// A trailing slash is stripped so joining the path never doubles it. An empty
// apiKey means no Authorization header is sent, which is how a local vLLM
// server without a key is used.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// Chat sends req to baseURL+"/chat/completions" and returns the content of the
// first choice.
//
// A non-200 response, an empty choice list and an empty content are all errors:
// callers treat the returned string as the model's answer and must never
// receive "" as if it were one.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (string, error) {
	if c == nil {
		return "", errors.New("llm: nil client")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm: post %s: %w", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read response from %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: %s returned HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("llm: decode response from %s: %w", url, err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: %s returned no choices: %s", url, snippet(raw))
	}

	content := out.Choices[0].Message.Content
	if content == "" {
		return "", fmt.Errorf("llm: %s returned empty content: %s", url, snippet(raw))
	}
	return content, nil
}

// snippet shortens a response body so an error message stays readable in logs.
func snippet(b []byte) string {
	const max = 512
	s := strings.TrimSpace(string(b))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
