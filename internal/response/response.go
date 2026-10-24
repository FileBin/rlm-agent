// Package response parses the structured answer returned by an rlm-agent LLM.
//
// It replaces the GBNF grammar that constrained generation on older backends:
// vLLM is asked for JSON with guided decoding against JSONSchema, and DeepSeek
// with plain JSON mode plus the schema restated in the prompt. Whatever the
// backend does, Parse is the single gate every answer has to pass before the
// orchestrator acts on it.
//
// Parsing is deliberately lenient about the shape of the document (missing or
// null lists are fine) and strict about the values the orchestrator depends on:
// a status is always present, a file is never written to an empty path, and a
// question always has a positive id to answer.
package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Validation failures are exported so callers can tell a malformed answer from
// a transport error and decide whether a retry is worth the budget.
var (
	// ErrInvalidJSON means the content was not a single JSON document.
	ErrInvalidJSON = errors.New("response: invalid JSON")
	// ErrEmptyStatus means the answer did not say what it did.
	ErrEmptyStatus = errors.New("response: empty status")
	// ErrEmptyFilePath means a file entry had no path to write to.
	ErrEmptyFilePath = errors.New("response: empty file path")
	// ErrInvalidQuestionID means a question id was less than 1.
	ErrInvalidQuestionID = errors.New("response: question id must be >= 1")
	// ErrNilResponse means Validate was called on a nil pointer.
	ErrNilResponse = errors.New("response: nil response")
)

// File is one file the agent wants written, path relative to its own sandbox
// directory.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Question is a request for human input. ID is the number the human answers
// with, so it starts at 1 and must never be zero.
type Question struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// Response is the whole answer of one agent turn. Only Status is required;
// Summary, Files, Commands and Questions may be absent, in which case the
// slices stay nil.
type Response struct {
	Status    string     `json:"status"`
	Summary   string     `json:"summary"`
	Files     []File     `json:"files"`
	Commands  []string   `json:"commands"`
	Questions []Question `json:"questions"`
}

// Parse decodes content and validates it. A valid JSON document that violates
// the contract is not a response: it is an error, because acting on it would
// mean writing files to an empty path or answering a question nobody asked.
//
// Unknown fields are ignored on purpose. DeepSeek's JSON mode is only told
// about the schema in the prompt and often adds extra keys such as "notes";
// failing on those would burn a retry for nothing. The schema still forbids
// them on the vLLM side via additionalProperties: false.
func Parse(content string) (*Response, error) {
	var r Response
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// Validate reports the first contract violation in r. It never mutates the
// response: a value is rejected, not repaired, so the caller always sees what
// the model actually produced.
func (r *Response) Validate() error {
	if r == nil {
		return ErrNilResponse
	}
	// Whitespace-checked: a status of "   " carries as little information as
	// an empty one and must not pass as success.
	if strings.TrimSpace(r.Status) == "" {
		return ErrEmptyStatus
	}
	for i, f := range r.Files {
		if strings.TrimSpace(f.Path) == "" {
			return fmt.Errorf("%w: files[%d]", ErrEmptyFilePath, i)
		}
	}
	for i, q := range r.Questions {
		if q.ID < 1 {
			return fmt.Errorf("%w: questions[%d].id = %d", ErrInvalidQuestionID, i, q.ID)
		}
	}
	return nil
}
