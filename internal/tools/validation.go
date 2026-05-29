package tools

import (
	"encoding/json"
	"fmt"

	"github.com/xeipuuv/gojsonschema"
)

// validateArgs checks raw JSON arguments against the given JSON Schema.
// Returns nil if valid, or a human-readable error message.
func validateArgs(schema map[string]any, rawArgs json.RawMessage) error {
	schemaLoader := gojsonschema.NewGoLoader(schema)
	docLoader := gojsonschema.NewBytesLoader(rawArgs)

	result, err := gojsonschema.Validate(schemaLoader, docLoader)
	if err != nil {
		return fmt.Errorf("schema validation internal error: %w", err)
	}

	if result.Valid() {
		return nil
	}

	// Build a compact error message from the first few validation errors.
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
