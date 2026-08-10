package generator

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

func TestGeneratorRejectsUnsupportedSchemaAndCatalogShapes(t *testing.T) {
	closed := false
	open := true
	tests := []struct {
		name   string
		invoke func() error
	}{
		{name: "unsupported root type", invoke: func() error {
			return (&typeRenderer{defined: map[string]struct{}{}}).renderNamed("Root", schemaNode{Type: "boolean"})
		}},
		{name: "object is not closed", invoke: func() error {
			return (&typeRenderer{defined: map[string]struct{}{}}).renderNamed("Root", schemaNode{Type: "object", AdditionalProperties: &open})
		}},
		{name: "error response has no variants", invoke: func() error {
			return (&typeRenderer{defined: map[string]struct{}{}}).renderNamed("ErrorResponse", schemaNode{Type: "object", AdditionalProperties: &closed, Properties: map[string]schemaNode{"error": {}}})
		}},
		{name: "array has no item schema", invoke: func() error {
			_, err := (&typeRenderer{}).fieldType("Root", "items", "RootItems", schemaNode{Type: "array"})
			return err
		}},
		{name: "nullable field has no concrete type", invoke: func() error {
			_, err := (&typeRenderer{}).fieldType("Root", "value", "RootValue", schemaNode{AnyOf: []schemaNode{{Type: "null"}}})
			return err
		}},
		{name: "field type is unsupported", invoke: func() error {
			_, err := (&typeRenderer{}).fieldType("Root", "value", "RootValue", schemaNode{Type: "function"})
			return err
		}},
		{name: "empty enum", invoke: func() error { var output bytes.Buffer; return renderEnum(&output, "Empty", nil) }},
		{name: "required method missing", invoke: func() error { _, err := renderClient(bundle.Manifest{}); return err }},
		{name: "unknown method", invoke: func() error {
			_, err := renderClient(bundle.Manifest{MethodCatalog: []bundle.Method{{Name: "admin.call"}}})
			return err
		}},
		{name: "schema name is empty", invoke: func() error { _, _, err := schemaNames("schemas/.schema.json"); return err }},
		{name: "enum contains non-string", invoke: func() error {
			_, err := enumStrings(schemaNode{Enum: []json.RawMessage{json.RawMessage("1")}})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.invoke(); err == nil {
				t.Fatal("expected unsupported generator input rejection")
			}
		})
	}
}

func TestLoadSchemasRejectsMalformedAndIncompleteInputs(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents string
	}{
		{name: "malformed schema", contents: "{"},
		{name: "unknown schema keyword", contents: `{"type":"string","unsupported":true}`},
		{name: "trailing schema value", contents: `{"type":"string"} {}`},
		{name: "incomplete schema inventory", contents: `{"type":"string"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "schemas", "value.schema.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create schema directory: %v", err)
			}
			if err := os.WriteFile(path, []byte(test.contents), 0o644); err != nil {
				t.Fatalf("write schema: %v", err)
			}
			pin := bundle.PinnedBundle{Bundle: bundle.Bundle{Root: root, Manifest: bundle.Manifest{Artifacts: []bundle.Artifact{{Path: "schemas/value.schema.json"}}}}}
			if _, err := loadSchemas(pin); err == nil {
				t.Fatal("expected schema loading rejection")
			}
		})
	}

	missing := bundle.PinnedBundle{Bundle: bundle.Bundle{Root: t.TempDir(), Manifest: bundle.Manifest{Artifacts: []bundle.Artifact{{Path: "schemas/missing.schema.json"}}}}}
	if _, err := loadSchemas(missing); err == nil {
		t.Fatal("expected missing schema rejection")
	}
}

func TestGeneratorHelpersCoverSupportedPrimitiveShapes(t *testing.T) {
	renderer := &typeRenderer{}
	for _, test := range []struct {
		node schemaNode
		want string
	}{
		{node: schemaNode{Type: "string"}, want: "string"},
		{node: schemaNode{Type: "integer"}, want: "int64"},
		{node: schemaNode{Type: "number"}, want: "int"},
		{node: schemaNode{Type: "boolean"}, want: "bool"},
		{node: schemaNode{Type: "object"}, want: "RootValue"},
		{node: schemaNode{Type: "array", Items: &schemaNode{Type: "string"}}, want: "[]string"},
		{node: schemaNode{AnyOf: []schemaNode{{Type: "null"}, {Type: "string"}}}, want: "*string"},
	} {
		got, err := renderer.fieldType("Root", "value", "RootValue", test.node)
		if err != nil || got != test.want {
			t.Errorf("field type = %q, %v; want %q", got, err, test.want)
		}
	}
	for input, want := range map[string]string{
		"api": "API", "id": "ID", "jsonrpc": "JSONRPC", "mcp": "MCP", "rpc": "RPC", "url": "URL", "two-words": "TwoWords",
	} {
		if got := exportedName(input); got != want {
			t.Errorf("exportedName(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := Generate(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing protocol root rejection")
	}
}

func TestMergeObjectVariantsKeepsSharedRequirementsAndOptionalVariantFields(t *testing.T) {
	closed := false
	shared := schemaNode{Type: "string", MinLength: intPointer(1)}
	attachmentID := schemaNode{Type: "string", MaxLength: intPointer(256)}
	merged, err := mergeObjectVariants(schemaNode{AnyOf: []schemaNode{
		{
			Type:                 "object",
			AdditionalProperties: &closed,
			Properties: map[string]schemaNode{
				"operationId":           shared,
				"executionAttachmentId": attachmentID,
			},
			Required: []string{"operationId", "executionAttachmentId"},
		},
		{
			Type:                 "object",
			AdditionalProperties: &closed,
			Properties:           map[string]schemaNode{"operationId": shared},
			Required:             []string{"operationId"},
		},
	}})
	if err != nil {
		t.Fatalf("merge object variants: %v", err)
	}
	if got, want := merged.Required, []string{"operationId"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged required = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(merged.Properties["executionAttachmentId"], attachmentID) {
		t.Fatal("variant-only attachment field was not preserved")
	}
	if merged.AdditionalProperties == nil || *merged.AdditionalProperties {
		t.Fatal("merged object union must remain closed")
	}
	typeName, err := (&typeRenderer{}).fieldType("Root", "params", "RootParams", schemaNode{AnyOf: []schemaNode{
		{Type: "object", AdditionalProperties: &closed},
		{Type: "object", AdditionalProperties: &closed},
	}})
	if err != nil || typeName != "RootParams" {
		t.Fatalf("object variant field type = %q, %v", typeName, err)
	}
}

func TestMergeObjectVariantsRejectsUnsafeShapes(t *testing.T) {
	closed := false
	open := true
	stringNode := schemaNode{Type: "string"}
	for _, test := range []struct {
		name string
		node schemaNode
	}{
		{name: "not an object union", node: schemaNode{AnyOf: []schemaNode{{Type: "object"}, {Type: "null"}}}},
		{name: "open variant", node: schemaNode{AnyOf: []schemaNode{{Type: "object", AdditionalProperties: &closed}, {Type: "object", AdditionalProperties: &open}}}},
		{name: "missing closed declaration", node: schemaNode{AnyOf: []schemaNode{{Type: "object", AdditionalProperties: &closed}, {Type: "object"}}}},
		{name: "conflicting property", node: schemaNode{AnyOf: []schemaNode{
			{Type: "object", AdditionalProperties: &closed, Properties: map[string]schemaNode{"value": stringNode}},
			{Type: "object", AdditionalProperties: &closed, Properties: map[string]schemaNode{"value": {Type: "integer"}}},
		}}},
		{name: "required absent property", node: schemaNode{AnyOf: []schemaNode{
			{Type: "object", AdditionalProperties: &closed, Required: []string{"missing"}},
			{Type: "object", AdditionalProperties: &closed},
		}}},
		{name: "duplicate required property", node: schemaNode{AnyOf: []schemaNode{
			{Type: "object", AdditionalProperties: &closed, Properties: map[string]schemaNode{"value": stringNode}, Required: []string{"value", "value"}},
			{Type: "object", AdditionalProperties: &closed, Properties: map[string]schemaNode{"value": stringNode}},
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := mergeObjectVariants(test.node); err == nil {
				t.Fatal("expected object union rejection")
			}
		})
	}
}

func intPointer(value int) *int {
	return &value
}
