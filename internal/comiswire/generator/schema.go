package generator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

type schemaNode struct {
	ID                   string                `json:"$id"`
	Schema               string                `json:"$schema"`
	AdditionalProperties *bool                 `json:"additionalProperties"`
	AnyOf                []schemaNode          `json:"anyOf"`
	Const                json.RawMessage       `json:"const"`
	Enum                 []json.RawMessage     `json:"enum"`
	ExclusiveMinimum     *json.Number          `json:"exclusiveMinimum"`
	Format               string                `json:"format"`
	Items                *schemaNode           `json:"items"`
	Maximum              *json.Number          `json:"maximum"`
	MaxItems             *int                  `json:"maxItems"`
	MaxLength            *int                  `json:"maxLength"`
	Minimum              *json.Number          `json:"minimum"`
	MinItems             *int                  `json:"minItems"`
	MinLength            *int                  `json:"minLength"`
	OneOf                []schemaNode          `json:"oneOf"`
	Pattern              string                `json:"pattern"`
	Properties           map[string]schemaNode `json:"properties"`
	Required             []string              `json:"required"`
	Type                 string                `json:"type"`
}

type schemaSpec struct {
	ConstName string
	Contents  []byte
	Node      schemaNode
	Path      string
	TypeName  string
}

func loadSchemas(pin bundle.PinnedBundle) ([]schemaSpec, error) {
	var schemas []schemaSpec
	for _, artifact := range pin.Manifest.Artifacts {
		if !strings.HasPrefix(artifact.Path, "schemas/") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(pin.Root, filepath.FromSlash(artifact.Path)))
		if err != nil {
			return nil, fmt.Errorf("read schema %q: %w", artifact.Path, err)
		}
		var node schemaNode
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		decoder.UseNumber()
		if err := decoder.Decode(&node); err != nil {
			return nil, fmt.Errorf("decode schema %q: %w", artifact.Path, err)
		}
		if _, err := decoder.Token(); err != io.EOF {
			return nil, fmt.Errorf("schema %q has trailing JSON", artifact.Path)
		}
		typeName, constName, err := schemaNames(artifact.Path)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, schemaSpec{
			ConstName: constName,
			Contents:  contents,
			Node:      node,
			Path:      artifact.Path,
			TypeName:  typeName,
		})
	}
	sort.Slice(schemas, func(left, right int) bool { return schemas[left].Path < schemas[right].Path })
	if len(schemas) != 17 {
		return nil, fmt.Errorf("expected 17 pinned schemas, found %d", len(schemas))
	}
	return schemas, nil
}

func schemaNames(path string) (string, string, error) {
	base := strings.TrimSuffix(filepath.Base(path), ".schema.json")
	parts := strings.FieldsFunc(base, func(character rune) bool { return character == '.' || character == '-' })
	if len(parts) == 0 {
		return "", "", fmt.Errorf("schema path %q has no type name", path)
	}
	for index, part := range parts {
		parts[index] = exportedName(part)
	}
	typeName := strings.Join(parts, "")
	if typeName == "ErrorResponse" {
		return typeName, "schemaErrorResponse", nil
	}
	return typeName, "schema" + typeName, nil
}

func exportedName(value string) string {
	if name, exists := map[string]string{
		"agentId":             "AgentID",
		"conversationRef":     "ConversationRef",
		"externalRunRef":      "ExternalRunRef",
		"jsonrpc":             "JSONRPC",
		"managedRunGroupId":   "ManagedRunGroupID",
		"managedRunId":        "ManagedRunID",
		"operationId":         "OperationID",
		"protocolId":          "ProtocolID",
		"registrationNonce":   "RegistrationNonce",
		"rootRunId":           "RootRunID",
		"serviceInstanceId":   "ServiceInstanceID",
		"serviceReportId":     "ServiceReportID",
		"terminalSessionId":   "TerminalSessionID",
		"traceId":             "TraceID",
		"workspacePolicyHash": "WorkspacePolicyHash",
		"workspaceLeaseId":    "WorkspaceLeaseID",
	}[value]; exists {
		return name
	}
	words := strings.FieldsFunc(value, func(character rune) bool {
		return character == '-' || character == '_' || character == '.'
	})
	for index, word := range words {
		switch strings.ToLower(word) {
		case "api":
			words[index] = "API"
		case "id":
			words[index] = "ID"
		case "jsonrpc":
			words[index] = "JSONRPC"
		case "mcp":
			words[index] = "MCP"
		case "rpc":
			words[index] = "RPC"
		case "url":
			words[index] = "URL"
		default:
			if word != "" {
				words[index] = strings.ToUpper(word[:1]) + word[1:]
			}
		}
	}
	return strings.Join(words, "")
}
