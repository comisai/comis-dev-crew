package mcpadapter

import (
	"encoding/json"
	"testing"
)

type schemaSemantics struct {
	properties map[string]struct{}
	required   map[string]struct{}
}

func inspectSchemaSemantics(t *testing.T, schema any) schemaSemantics {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal JSON Schema: %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatalf("normalize JSON Schema: %v", err)
	}
	semantics := schemaSemantics{
		properties: make(map[string]struct{}),
		required:   make(map[string]struct{}),
	}
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for keyword, child := range node {
				switch keyword {
				case "properties":
					properties, ok := child.(map[string]any)
					if !ok {
						t.Fatalf("JSON Schema properties = %#v", child)
					}
					for name := range properties {
						semantics.properties[name] = struct{}{}
					}
				case "required":
					required, ok := child.([]any)
					if !ok {
						t.Fatalf("JSON Schema required = %#v", child)
					}
					for _, entry := range required {
						name, ok := entry.(string)
						if !ok {
							t.Fatalf("JSON Schema required entry = %#v", entry)
						}
						semantics.required[name] = struct{}{}
					}
				}
				visit(child)
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(normalized)
	return semantics
}

func requireSchemaFields(t *testing.T, semantics schemaSemantics, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, present := semantics.properties[field]; !present {
			t.Errorf("JSON Schema property %q is absent", field)
		}
		if _, required := semantics.required[field]; !required {
			t.Errorf("JSON Schema property %q is optional", field)
		}
	}
}

func forbidSchemaFields(t *testing.T, semantics schemaSemantics, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, present := semantics.properties[field]; present {
			t.Errorf("JSON Schema exposes property %q", field)
		}
	}
}
