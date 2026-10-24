package response

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseFull(t *testing.T) {
	const raw = `{
		"status": "done",
		"summary": "wrote the flake and the module",
		"files": [
			{"path": "code/flake.nix", "content": "{ inputs }: { }"},
			{"path": "code/default.nix", "content": "{}"}
		],
		"commands": ["nix flake check", "go vet ./..."],
		"questions": [
			{"id": 1, "text": "which package name?"},
			{"id": 2, "text": "is wine needed?"}
		]
	}`

	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("Status = %q, want %q", got.Status, "done")
	}
	if got.Summary != "wrote the flake and the module" {
		t.Errorf("Summary = %q", got.Summary)
	}
	if len(got.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(got.Files))
	}
	if got.Files[0].Path != "code/flake.nix" || got.Files[0].Content != "{ inputs }: { }" {
		t.Errorf("Files[0] = %+v", got.Files[0])
	}
	if got.Files[1].Path != "code/default.nix" {
		t.Errorf("Files[1].Path = %q", got.Files[1].Path)
	}
	if want := []string{"nix flake check", "go vet ./..."}; !reflect.DeepEqual(got.Commands, want) {
		t.Errorf("Commands = %v, want %v", got.Commands, want)
	}
	if len(got.Questions) != 2 {
		t.Fatalf("len(Questions) = %d, want 2", len(got.Questions))
	}
	if got.Questions[0].ID != 1 || got.Questions[0].Text != "which package name?" {
		t.Errorf("Questions[0] = %+v", got.Questions[0])
	}
	if got.Questions[1].ID != 2 {
		t.Errorf("Questions[1].ID = %d, want 2", got.Questions[1].ID)
	}
}

func TestParseMinimal(t *testing.T) {
	// Only status: every optional field must stay nil, and that is not an error.
	got, err := Parse(`{"status":"blocked"}`)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if got.Status != "blocked" {
		t.Errorf("Status = %q, want %q", got.Status, "blocked")
	}
	if got.Summary != "" {
		t.Errorf("Summary = %q, want empty", got.Summary)
	}
	if got.Files != nil {
		t.Errorf("Files = %#v, want nil", got.Files)
	}
	if got.Commands != nil {
		t.Errorf("Commands = %#v, want nil", got.Commands)
	}
	if got.Questions != nil {
		t.Errorf("Questions = %#v, want nil", got.Questions)
	}
}

func TestParseEmptyLists(t *testing.T) {
	got, err := Parse(`{"status":"done","files":[],"commands":[],"questions":[]}`)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(got.Files) != 0 || len(got.Commands) != 0 || len(got.Questions) != 0 {
		t.Errorf("expected empty slices, got %+v", got)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"truncated", `{"status":"done"`},
		{"not an object", `"done"`},
		{"array", `[{"status":"done"}]`},
		{"trailing garbage", `{"status":"done"} extra`},
		{"code fence", "```json\n{\"status\":\"done\"}\n```"},
		{"wrong types", `{"status":"done","files":"code/flake.nix"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.content)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, want error", tt.content, got)
			}
			if !errors.Is(err, ErrInvalidJSON) {
				t.Errorf("error = %v, want ErrInvalidJSON", err)
			}
			if got != nil {
				t.Errorf("Parse returned %+v alongside an error", got)
			}
		})
	}
}

func TestParseEmptyStatus(t *testing.T) {
	for _, content := range []string{
		`{"status":""}`,
		`{"status":"   "}`,
		`{"summary":"did things","files":[{"path":"a","content":"b"}]}`,
		`{"status":null}`,
		`null`,
	} {
		got, err := Parse(content)
		if err == nil {
			t.Fatalf("Parse(%q) = %+v, want error", content, got)
		}
		if !errors.Is(err, ErrEmptyStatus) {
			t.Errorf("Parse(%q) error = %v, want ErrEmptyStatus", content, err)
		}
	}
}

func TestParseEmptyFilePath(t *testing.T) {
	for _, content := range []string{
		`{"status":"done","files":[{"path":"","content":"b"}]}`,
		`{"status":"done","files":[{"path":"  ","content":"b"}]}`,
		`{"status":"done","files":[{"path":"ok","content":"a"},{"content":"b"}]}`,
	} {
		got, err := Parse(content)
		if err == nil {
			t.Fatalf("Parse(%q) = %+v, want error", content, got)
		}
		if !errors.Is(err, ErrEmptyFilePath) {
			t.Errorf("Parse(%q) error = %v, want ErrEmptyFilePath", content, err)
		}
	}
}

func TestParseInvalidQuestionID(t *testing.T) {
	for _, content := range []string{
		`{"status":"paused","questions":[{"id":0,"text":"which?"}]}`,
		`{"status":"paused","questions":[{"id":-3,"text":"which?"}]}`,
		`{"status":"paused","questions":[{"id":1,"text":"a"},{"text":"b"}]}`,
	} {
		got, err := Parse(content)
		if err == nil {
			t.Fatalf("Parse(%q) = %+v, want error", content, got)
		}
		if !errors.Is(err, ErrInvalidQuestionID) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalidQuestionID", content, err)
		}
	}
}

func TestParseIgnoresUnknownFields(t *testing.T) {
	// DeepSeek JSON mode adds keys nobody asked for; that must not cost a retry.
	got, err := Parse(`{"status":"done","notes":"extra","confidence":0.5}`)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("Status = %q, want %q", got.Status, "done")
	}
}

func TestRoundTrip(t *testing.T) {
	want := Response{
		Status:    "done",
		Summary:   "implemented the parser",
		Files:     []File{{Path: "internal/response/response.go", Content: "package response\n"}},
		Commands:  []string{"go build ./...", "go test ./..."},
		Questions: []Question{{ID: 7, Text: "ship it?"}},
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Parse(string(encoded))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", *got, want)
	}
}

func TestEmptyResponseValidatesAsError(t *testing.T) {
	var r Response
	if err := r.Validate(); !errors.Is(err, ErrEmptyStatus) {
		t.Errorf("Validate() = %v, want ErrEmptyStatus", err)
	}
	var nilResp *Response
	if err := nilResp.Validate(); !errors.Is(err, ErrNilResponse) {
		t.Errorf("nil Validate() = %v, want ErrNilResponse", err)
	}
}

func TestJSONSchemaIsValid(t *testing.T) {
	if !json.Valid([]byte(JSONSchema)) {
		t.Fatal("JSONSchema is not valid JSON")
	}
	if !strings.Contains(JSONSchema, `"status"`) {
		t.Error(`JSONSchema does not mention "status"`)
	}
	if !strings.Contains(JSONSchema, `"additionalProperties"`) {
		t.Error(`JSONSchema does not mention "additionalProperties"`)
	}

	var schema struct {
		Type                 string                     `json:"type"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(JSONSchema), &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("schema type = %q, want object", schema.Type)
	}
	if !reflect.DeepEqual(schema.Required, []string{"status"}) {
		t.Errorf("required = %v, want [status]", schema.Required)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Error("additionalProperties must be false at the top level")
	}
}

// TestJSONSchemaCoversResponse keeps the schema and the struct in sync: a field
// added to Response without a schema property would be rejected by guided
// decoding, and this test fails first.
func TestJSONSchemaCoversResponse(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(JSONSchema), &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}

	typ := reflect.TypeOf(Response{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("Response field %q is missing from JSONSchema.properties", name)
		}
	}
	if len(schema.Properties) != typ.NumField() {
		t.Errorf("schema has %d properties, struct has %d fields", len(schema.Properties), typ.NumField())
	}
}

func TestWrapSchema(t *testing.T) {
	wrapped := WrapSchema("developer")
	if wrapped == nil {
		t.Fatal("WrapSchema returned nil")
	}
	if !json.Valid(wrapped) {
		t.Fatalf("WrapSchema returned invalid JSON: %s", wrapped)
	}

	var env struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(wrapped, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Name != "developer" {
		t.Errorf("name = %q, want developer", env.Name)
	}
	// The envelope is compacted by json.Marshal, so compare the schema
	// semantically: the same document, whitespace aside.
	var gotSchema, wantSchema any
	if err := json.Unmarshal(env.Schema, &gotSchema); err != nil {
		t.Fatalf("decode wrapped schema: %v", err)
	}
	if err := json.Unmarshal([]byte(JSONSchema), &wantSchema); err != nil {
		t.Fatalf("decode JSONSchema: %v", err)
	}
	if !reflect.DeepEqual(gotSchema, wantSchema) {
		t.Error("wrapped schema differs from JSONSchema")
	}
}

func TestWrapSchemaDefaultName(t *testing.T) {
	var env struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(WrapSchema(""), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Name != defaultSchemaName {
		t.Errorf("name = %q, want %q", env.Name, defaultSchemaName)
	}
}
