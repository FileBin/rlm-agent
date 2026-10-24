// Package questions reads and writes user_questions.json, the file the
// orchestrator uses to ask a human for input during a CRITICAL_PAUSE.
//
// On-disk format:
//
//	{"questions":[{"id":1,"text":"..."}]}
package questions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrNilList is returned when a nil *QuestionList is passed to Write.
var ErrNilList = errors.New("questions: nil question list")

// Question is a single question addressed to the operator.
type Question struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// QuestionList is the root document of user_questions.json.
type QuestionList struct {
	Questions []Question `json:"questions"`
}

// Parse decodes a question list from r.
//
// The document must be exactly one JSON object; trailing data is rejected so a
// half-written or hand-edited file cannot be silently ignored.
func Parse(r io.Reader) (*QuestionList, error) {
	if r == nil {
		return nil, errors.New("questions: nil reader")
	}

	dec := json.NewDecoder(r)
	var list *QuestionList
	if err := dec.Decode(&list); err != nil {
		return nil, fmt.Errorf("questions: decode: %w", err)
	}
	if list == nil {
		return nil, errors.New("questions: decode: null document")
	}

	// A second value means the file holds more than one document.
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errors.New("questions: decode: trailing data after JSON object")
	}

	return list, nil
}

// Write encodes q into w as indented JSON, which keeps the file readable for
// the human who has to answer it.
func Write(w io.Writer, q *QuestionList) error {
	if w == nil {
		return errors.New("questions: nil writer")
	}
	if q == nil {
		return ErrNilList
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(q); err != nil {
		return fmt.Errorf("questions: encode: %w", err)
	}
	return nil
}
