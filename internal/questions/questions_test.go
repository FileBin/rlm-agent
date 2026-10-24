package questions

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   *QuestionList
	}{
		{
			name: "single question",
			in: &QuestionList{Questions: []Question{
				{ID: 1, Text: "Which flake input should the Wine app pin?"},
			}},
		},
		{
			name: "several questions",
			in: &QuestionList{Questions: []Question{
				{ID: 1, Text: "Does the container get network access?"},
				{ID: 2, Text: "Which role owns /workspace/code?"},
				{ID: 3, Text: "Should flake check failures re-plan or pause?"},
			}},
		},
		{
			name: "empty list",
			in:   &QuestionList{Questions: []Question{}},
		},
		{
			name: "nil list",
			in:   &QuestionList{},
		},
		{
			name: "unicode and quotes",
			in: &QuestionList{Questions: []Question{
				{ID: 7, Text: "Нужен ли «pre-fetch» в host-store? \"да\" или \"нет\"?"},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, tc.in); err != nil {
				t.Fatalf("Write() failed: %v", err)
			}

			got, err := Parse(&buf)
			if err != nil {
				t.Fatalf("Parse() failed on %q: %v", buf.String(), err)
			}
			if !reflect.DeepEqual(got, tc.in) {
				t.Fatalf("round trip mismatch:\n got = %#v\nwant = %#v", got, tc.in)
			}
		})
	}
}

// TestWireFormat pins the documented JSON shape.
func TestWireFormat(t *testing.T) {
	var buf bytes.Buffer
	in := &QuestionList{Questions: []Question{{ID: 1, Text: "ok?"}}}
	if err := Write(&buf, in); err != nil {
		t.Fatalf("Write() failed: %v", err)
	}

	compact := strings.Join(strings.Fields(buf.String()), "")
	if compact != `{"questions":[{"id":1,"text":"ok?"}]}` {
		t.Fatalf("wire format = %s", compact)
	}
}

func TestParseBroken(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"truncated", `{"questions":[{"id":1,"text":"a"`},
		{"not json", `definitely not json`},
		{"wrong type", `{"questions":"nope"}`},
		{"id not a number", `{"questions":[{"id":"1","text":"a"}]}`},
		{"empty input", ``},
		{"null document", `null`},
		{"trailing garbage", `{"questions":[]} {"questions":[]}`},
		{"top-level array", `[{"id":1,"text":"a"}]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tc.in))
			if err == nil {
				t.Fatalf("Parse(%q) = %#v, want error", tc.in, got)
			}
			if got != nil {
				t.Fatalf("Parse(%q) returned non-nil list %#v with error", tc.in, got)
			}
		})
	}
}

func TestParseNilReader(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Fatal("Parse(nil) returned no error")
	}
}

func TestWriteErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, nil); !errors.Is(err, ErrNilList) {
		t.Fatalf("Write(nil list) error = %v, want %v", err, ErrNilList)
	}
	if err := Write(nil, &QuestionList{}); err == nil {
		t.Fatal("Write(nil writer) returned no error")
	}

	wantErr := errors.New("disk full")
	fw := failingWriter{err: wantErr}
	err := Write(fw, &QuestionList{Questions: []Question{{ID: 1, Text: "a"}}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Write() error = %v, want it to wrap %v", err, wantErr)
	}
}

// failingWriter fails every write, to exercise the encoder error path.
type failingWriter struct {
	err error
}

func (w failingWriter) Write(p []byte) (int, error) {
	return 0, w.err
}
