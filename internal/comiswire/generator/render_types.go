package generator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type typeRenderer struct {
	buffer  bytes.Buffer
	defined map[string]struct{}
}

func renderTypes(schemas []schemaSpec) (string, error) {
	renderer := typeRenderer{defined: make(map[string]struct{})}
	for _, declaration := range []string{
		"type OperationID string\n",
		"type ManagedRunID string\n",
		"type ManagedRunGroupID string\n",
		"type WorkspaceLeaseID string\n",
		"type TerminalSessionID string\n",
		"type ExecutionAttachmentID string\n",
		"type AttachmentTargetName string\n",
		"type RegistrationNonce string\n",
		"type ServiceReportID string\n",
		"type ArtifactRef string\n",
		"type RootRunID string\n",
	} {
		renderer.buffer.WriteString(declaration)
	}
	for _, schema := range schemas {
		if err := renderer.renderNamed(schema.TypeName, schema.Node); err != nil {
			return "", fmt.Errorf("render %s: %w", schema.Path, err)
		}
	}
	return renderer.buffer.String(), nil
}

func (renderer *typeRenderer) renderNamed(name string, node schemaNode) error {
	if _, exists := renderer.defined[name]; exists {
		return nil
	}
	renderer.defined[name] = struct{}{}
	if name == "ErrorResponse" {
		return renderer.renderErrorResponse(node)
	}
	if objectVariantUnion(node) {
		var err error
		node, err = mergeObjectVariants(node)
		if err != nil {
			return err
		}
	}
	switch node.Type {
	case "string":
		fmt.Fprintf(&renderer.buffer, "type %s string\n\n", name)
		return nil
	case "object":
		return renderer.renderStruct(name, node)
	default:
		return fmt.Errorf("unsupported root schema type %q", node.Type)
	}
}

func (renderer *typeRenderer) renderStruct(name string, node schemaNode) error {
	if objectVariantUnion(node) {
		var err error
		node, err = mergeObjectVariants(node)
		if err != nil {
			return err
		}
	}
	if node.AdditionalProperties == nil || *node.AdditionalProperties {
		return fmt.Errorf("object %s is not closed", name)
	}
	required := make(map[string]struct{}, len(node.Required))
	for _, property := range node.Required {
		required[property] = struct{}{}
	}
	properties := sortedProperties(node.Properties)
	fmt.Fprintf(&renderer.buffer, "type %s struct {\n", name)
	for _, property := range properties {
		child := node.Properties[property]
		fieldName := exportedName(property)
		childName := nestedTypeName(name, fieldName)
		fieldType, err := renderer.fieldType(name, property, childName, child)
		if err != nil {
			return err
		}
		_, isRequired := required[property]
		if !isRequired && !strings.HasPrefix(fieldType, "[]") {
			fieldType = "*" + fieldType
		}
		tag := property
		if !isRequired {
			tag += ",omitempty"
		}
		fmt.Fprintf(&renderer.buffer, "\t%s %s `json:%q`\n", fieldName, fieldType, tag)
	}
	renderer.buffer.WriteString("}\n\n")
	for _, property := range properties {
		child := node.Properties[property]
		fieldName := exportedName(property)
		childName := nestedTypeName(name, fieldName)
		if special := specialFieldType(name, property); special != "" {
			if child.Type == "object" {
				if err := renderer.renderNamed(special, child); err != nil {
					return err
				}
			}
			continue
		}
		if child.Type == "object" || objectVariantUnion(child) {
			if err := renderer.renderNamed(childName, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (renderer *typeRenderer) renderErrorResponse(node schemaNode) error {
	errorNode, exists := node.Properties["error"]
	if !exists || len(errorNode.OneOf) == 0 {
		return fmt.Errorf("error response has no closed variants")
	}
	renderer.buffer.WriteString("type ErrorResponse struct {\n\tError RPCError `json:\"error\"`\n\tID *OperationID `json:\"id\"`\n\tJSONRPC string `json:\"jsonrpc\"`\n}\n\n")
	renderer.buffer.WriteString("type RPCError struct {\n\tCode int `json:\"code\"`\n\tHint *string `json:\"hint,omitempty\"`\n\tKind ErrorKind `json:\"kind\"`\n\tMessage string `json:\"message\"`\n\tRetryable bool `json:\"retryable\"`\n}\n\n")
	return nil
}

func (renderer *typeRenderer) fieldType(parent, property, childName string, node schemaNode) (string, error) {
	if special := specialFieldType(parent, property); special != "" {
		return special, nil
	}
	if objectVariantUnion(node) {
		return childName, nil
	}
	if len(node.AnyOf) > 0 {
		for _, candidate := range node.AnyOf {
			if candidate.Type != "null" {
				base, err := renderer.fieldType(parent, property, childName, candidate)
				if err != nil {
					return "", err
				}
				return "*" + base, nil
			}
		}
		return "", fmt.Errorf("field %s.%s is only nullable", parent, property)
	}
	switch node.Type {
	case "string":
		return "string", nil
	case "integer":
		return "int64", nil
	case "number":
		return "int", nil
	case "boolean":
		return "bool", nil
	case "object":
		return childName, nil
	case "array":
		if node.Items == nil {
			return "", fmt.Errorf("array field %s.%s has no item schema", parent, property)
		}
		itemType, err := renderer.fieldType(parent, property+"Item", childName+"Item", *node.Items)
		if err != nil {
			return "", err
		}
		return "[]" + itemType, nil
	default:
		return "", fmt.Errorf("field %s.%s has unsupported type %q", parent, property, node.Type)
	}
}

func specialFieldType(parent, property string) string {
	switch property {
	case "id", "operationId":
		return "OperationID"
	case "method":
		return "Method"
	case "serviceInstanceId":
		return "ServiceInstanceID"
	case "managedRunId":
		return "ManagedRunID"
	case "managedRunGroupId":
		return "ManagedRunGroupID"
	case "workspaceLeaseId":
		return "WorkspaceLeaseID"
	case "executionAttachmentId":
		return "ExecutionAttachmentID"
	case "attachmentTargetName":
		return "AttachmentTargetName"
	case "terminalSessionId":
		return "TerminalSessionID"
	case "externalRunRef":
		return "ExternalRunRef"
	case "registrationNonce":
		return "RegistrationNonce"
	case "serviceReportId":
		return "ServiceReportID"
	case "rootRunId":
		return "RootRunID"
	case "reason":
		if parent == "AbandonRequestParams" {
			return "AbandonReason"
		}
	case "disposition":
		if parent == "AbandonRequestParams" || parent == "AbandonResponseResult" {
			return "AbandonDisposition"
		}
	case "terminalTransition":
		if parent == "AbandonResponseResult" {
			return "AbandonTerminalTransition"
		}
	case "transition":
		if parent == "TerminalEventRequestParams" || parent == "TerminalEventResponseResult" {
			return "CapabilityTerminalTransition"
		}
	case "kind":
		if parent == "ReportRequestParams" {
			return "ReportKind"
		}
		if parent == "MCPManagedRunResultRequestedAttachment" {
			return "ExecutionAttachmentKind"
		}
	case "status":
		if parent == "HealthResponseResult" {
			return "HealthStatus"
		}
	case "state":
		return "ManagedRunState"
	case "requestedScopes", "activeScopes":
		return "[]ServiceScope"
	case "artifactRefs":
		return "[]ArtifactRef"
	case "limits":
		if parent == "HandshakeResponseResult" {
			return "ProtocolLimits"
		}
	}
	return ""
}

func objectVariantUnion(node schemaNode) bool {
	if len(node.AnyOf) < 2 {
		return false
	}
	for _, variant := range node.AnyOf {
		if variant.Type != "object" {
			return false
		}
	}
	return true
}

func mergeObjectVariants(node schemaNode) (schemaNode, error) {
	if !objectVariantUnion(node) {
		return schemaNode{}, fmt.Errorf("schema is not a closed object variant union")
	}
	closed := false
	merged := schemaNode{
		Type:                 "object",
		AdditionalProperties: &closed,
		Properties:           make(map[string]schemaNode),
	}
	requiredCounts := make(map[string]int)
	for _, variant := range node.AnyOf {
		if variant.AdditionalProperties == nil || *variant.AdditionalProperties {
			return schemaNode{}, fmt.Errorf("object union variant is not closed")
		}
		for property, child := range variant.Properties {
			if previous, exists := merged.Properties[property]; exists && !reflect.DeepEqual(previous, child) {
				return schemaNode{}, fmt.Errorf("object union property %s has conflicting schemas", property)
			}
			merged.Properties[property] = child
		}
		seenRequired := make(map[string]struct{}, len(variant.Required))
		for _, property := range variant.Required {
			if _, exists := variant.Properties[property]; !exists {
				return schemaNode{}, fmt.Errorf("object union requires absent property %s", property)
			}
			if _, duplicate := seenRequired[property]; duplicate {
				return schemaNode{}, fmt.Errorf("object union repeats required property %s", property)
			}
			seenRequired[property] = struct{}{}
			requiredCounts[property]++
		}
	}
	for property, count := range requiredCounts {
		if count == len(node.AnyOf) {
			merged.Required = append(merged.Required, property)
		}
	}
	sort.Strings(merged.Required)
	return merged, nil
}

func nestedTypeName(parent, field string) string {
	if parent == "HandshakeResponseResult" && field == "Limits" {
		return "ProtocolLimits"
	}
	return parent + field
}

func sortedProperties(properties map[string]schemaNode) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func rawString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func enumStrings(node schemaNode) ([]string, error) {
	values := make([]string, 0, len(node.Enum))
	for _, raw := range node.Enum {
		value, err := rawString(raw)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func quoted(value string) string { return strconv.Quote(value) }
