package response

import "encoding/json"

// JSONSchema is the draft-07 schema of Response, handed to vLLM as the
// response_format so guided decoding can only emit a shape Parse accepts.
//
// The constraints mirror Validate: status is required and non-empty, file paths
// are non-empty, question ids are integers >= 1, and no unknown property is
// allowed anywhere.
const JSONSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "AgentResponse",
  "type": "object",
  "properties": {
    "status": {
      "type": "string",
      "minLength": 1
    },
    "summary": {
      "type": "string"
    },
    "files": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "path": {
            "type": "string",
            "minLength": 1
          },
          "content": {
            "type": "string"
          }
        },
        "required": ["path", "content"],
        "additionalProperties": false
      }
    },
    "commands": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "questions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {
            "type": "integer",
            "minimum": 1
          },
          "text": {
            "type": "string"
          }
        },
        "required": ["id", "text"],
        "additionalProperties": false
      }
    }
  },
  "required": ["status"],
  "additionalProperties": false
}`

// defaultSchemaName is used when WrapSchema gets an empty name: vLLM rejects a
// response_format whose json_schema has no name.
const defaultSchemaName = "response"

// schemaEnvelope is the wrapper vLLM expects around a schema. The llm package
// passes the result through verbatim, which is why this package does not import
// it and does not depend on any other package of the project.
type schemaEnvelope struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// WrapSchema returns {"name": name, "schema": JSONSchema}, the value of
// response_format.json_schema for a vLLM request.
//
// The name identifies the schema to the server, so different agents can use
// different names for the same shape. An empty name falls back to "response".
// The result is nil only if JSONSchema itself stops being valid JSON, which
// TestJSONSchemaIsValid guards against; the signature stays error-free because
// that is a programming error, not a runtime condition.
func WrapSchema(name string) json.RawMessage {
	if name == "" {
		name = defaultSchemaName
	}
	wrapped, err := json.Marshal(schemaEnvelope{
		Name:   name,
		Schema: json.RawMessage(JSONSchema),
	})
	if err != nil {
		return nil
	}
	return wrapped
}
