package mcpadapter

import (
	"encoding/json"
	"strings"
	"testing"
)

type normalizedSchema struct {
	root map[string]any
}

type schemaObject struct {
	properties map[string]any
	required   map[string]struct{}
}

func inspectSchemaSemantics(t *testing.T, schema any) normalizedSchema {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal JSON Schema: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatalf("normalize JSON Schema: %v", err)
	}
	return normalizedSchema{root: root}
}

func (schema normalizedSchema) objectAt(t *testing.T, path ...string) schemaObject {
	t.Helper()
	node := schema.resolve(t, schema.root)
	for _, name := range path {
		object := schema.object(t, node)
		child, present := object.properties[name]
		if !present {
			t.Fatalf("JSON Schema path %q is absent", strings.Join(path, "."))
		}
		node = schema.resolve(t, schemaMap(t, child))
		if items, array := node["items"]; array {
			node = schema.resolve(t, schemaMap(t, items))
		}
	}
	return schema.object(t, node)
}

func (schema normalizedSchema) object(t *testing.T, node map[string]any) schemaObject {
	t.Helper()
	node = schema.resolve(t, node)
	properties, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatalf("JSON Schema object properties = %#v", node["properties"])
	}
	required := make(map[string]struct{})
	if encodedRequired, present := node["required"]; present {
		entries, ok := encodedRequired.([]any)
		if !ok {
			t.Fatalf("JSON Schema required = %#v", encodedRequired)
		}
		for _, entry := range entries {
			name, ok := entry.(string)
			if !ok {
				t.Fatalf("JSON Schema required entry = %#v", entry)
			}
			required[name] = struct{}{}
		}
	}
	return schemaObject{properties: properties, required: required}
}

func (schema normalizedSchema) resolve(t *testing.T, node map[string]any) map[string]any {
	t.Helper()
	for depth := 0; depth < 32; depth++ {
		ref, referenced := node["$ref"].(string)
		if !referenced {
			return node
		}
		if !strings.HasPrefix(ref, "#/") {
			t.Fatalf("JSON Schema reference %q is not local", ref)
		}
		var value any = schema.root
		for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			object, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("JSON Schema reference %q traverses a non-object", ref)
			}
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			value, ok = object[token]
			if !ok {
				t.Fatalf("JSON Schema reference %q is unresolved", ref)
			}
		}
		node = schemaMap(t, value)
	}
	t.Fatal("JSON Schema reference depth exceeds its bound")
	return nil
}

func schemaMap(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("JSON Schema node = %#v", value)
	}
	return object
}

func requireSchemaFields(t *testing.T, object schemaObject, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, present := object.properties[field]; !present {
			t.Errorf("JSON Schema property %q is absent", field)
		}
		if _, required := object.required[field]; !required {
			t.Errorf("JSON Schema property %q is optional", field)
		}
	}
}

func forbidSchemaFields(t *testing.T, schema normalizedSchema, fields ...string) {
	t.Helper()
	forbidden := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		forbidden[field] = struct{}{}
	}
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		node = schema.resolve(t, node)
		if properties, ok := node["properties"].(map[string]any); ok {
			for name, child := range properties {
				if _, denied := forbidden[name]; denied {
					t.Errorf("JSON Schema exposes property %q", name)
				}
				visit(schemaMap(t, child))
			}
		}
		if items, ok := node["items"]; ok {
			visit(schemaMap(t, items))
		}
	}
	visit(schema.root)
}
