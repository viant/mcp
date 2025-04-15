package schema

import (
	"fmt"
	"github.com/viant/tagly/format"
	"reflect"
	"time"
)

// InputSchema is a JSON InputSchema representation of a struct.

// schemaForTypeInternal returns a JSON InputSchema representation for a given reflect.Type.
// The inSlice flag is used to determine if we are processing an element inside a slice.
func schemaForTypeInternal(t reflect.Type, inSlice bool) map[string]interface{} {
	schema := make(map[string]interface{})

	// Special handling for time.Time: treat as ISO 8601 string.
	if t == reflect.TypeOf(time.Time{}) {
		schema["type"] = "string"
		schema["format"] = "date-time"
		return schema
	}

	// Handle pointer types.
	if t.Kind() == reflect.Ptr {
		// Unwrap pointer.
		schema = schemaForTypeInternal(t.Elem(), inSlice)
		// Mark as nullable unless we are processing a slice element.
		if !inSlice {
			schema["nullable"] = true
		}
		return schema
	}

	switch t.Kind() {
	case reflect.Bool:
		schema["type"] = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		schema["type"] = "integer"
	case reflect.Float32, reflect.Float64:
		schema["type"] = "number"
	case reflect.String:
		schema["type"] = "string"
	case reflect.Slice, reflect.Array:
		schema["type"] = "array"
		// When processing slice items, set inSlice = true.
		schema["items"] = schemaForTypeInternal(t.Elem(), true)
	case reflect.Map:
		schema["type"] = "object"
		// For maps, set additionalProperties based on the element type.
		schema["additionalProperties"] = schemaForTypeInternal(t.Elem(), false)
	case reflect.Struct:
		// For structs, recursively convert their fields.
		schema["type"] = "object"
		properties, required := structToProperties(t)
		schema["properties"] = properties
		if len(required) > 0 {
			schema["required"] = required
		}
	default:
		// Fallback to string type.
		schema["type"] = "string"
	}
	return schema
}

// schemaForType is a wrapper that starts schema generation with inSlice=false.
func schemaForType(t reflect.Type) map[string]interface{} {
	return schemaForTypeInternal(t, false)
}

// structToProperties converts a struct type into MCP InputSchema properties and required fields.
func structToProperties(t reflect.Type) (ToolInputSchemaProperties, []string) {
	properties := make(ToolInputSchemaProperties)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Only process exported fields.
		if !field.IsExported() {
			continue
		}

		tag, _ := format.Parse(field.Tag, "json", "format")
		if tag == nil {
			tag = &format.Tag{}
		}

		if tag.Ignore {
			continue
		}

		// Determine field JSON name (if provided via tag).
		fieldName := field.Name
		if tag.Name != "" {
			fieldName = tag.Name
		}

		// Generate the field's JSON schema.
		fieldSchema := schemaForType(field.Type)

		if tag.DateFormat != "" {
			fieldSchema["format"] = tag.DateFormat
		}
		// Set the property in overall schema.
		properties[fieldName] = fieldSchema
		if field.Type.Kind() != reflect.Ptr && !tag.Omitempty {
			required = append(required, fieldName)
		}
	}

	return properties, required
}

func (s *ToolInputSchema) Load(v any) error {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("expected a struct type, got %s", t.Kind())
	}
	properties, required := structToProperties(t)
	s.Properties = properties
	s.Required = required
	s.Type = "object"
	return nil
}
