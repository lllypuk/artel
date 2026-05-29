package tools

import (
	"encoding/json"
	"fmt"

	"github.com/xeipuuv/gojsonschema"
)

// compiledSchema wraps a pre-compiled gojsonschema Schema.
// Storing a compiled schema avoids re-parsing the JSON Schema on every tool invocation.
type compiledSchema struct {
	schema *gojsonschema.Schema
}

// compileSchema parses and compiles a JSON Schema from a raw map.
// Returns an error if the schema itself is invalid.
func compileSchema(raw map[string]any) (*compiledSchema, error) {
	s, err := gojsonschema.NewSchema(gojsonschema.NewGoLoader(raw))
	if err != nil {
		return nil, fmt.Errorf("compile JSON Schema: %w", err)
	}
	return &compiledSchema{schema: s}, nil
}

// validateArgs checks raw JSON arguments against the pre-compiled schema.
// Returns nil if valid, or a human-readable error.
func validateArgs(cs *compiledSchema, rawArgs json.RawMessage) error {
	docLoader := gojsonschema.NewBytesLoader(rawArgs)

	result, err := cs.schema.Validate(docLoader)
	if err != nil {
		return fmt.Errorf("schema validation internal error: %w", err)
	}

	if result.Valid() {
		return nil
	}

	// Build a compact error message from the validation errors.
	errs := result.Errors()
	if len(errs) == 0 {
		return fmt.Errorf("validation failed (no details)")
	}
	// Use first error as the main message.
	msg := errs[0].String()
	if len(errs) > 1 {
		msg += fmt.Sprintf(" (and %d more)", len(errs)-1)
	}
	return fmt.Errorf("invalid args: %s", msg)
}
