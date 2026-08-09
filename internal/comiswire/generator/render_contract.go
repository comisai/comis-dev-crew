package generator

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

func renderContract(manifest bundle.Manifest, schemas []schemaSpec) (string, error) {
	var output bytes.Buffer
	fmt.Fprintf(&output, "const ProtocolID = %s\n", quoted(manifest.ProtocolID))
	fmt.Fprintf(&output, "const BundleDigest = %s\n", quoted(manifest.BundleDigest))
	output.WriteString("const JSONRPCVersion = \"2.0\"\n\n")
	fmt.Fprintf(&output, "const MaxEvidenceBytes = %d\n", manifest.Limits.MaxEvidenceBytes)
	fmt.Fprintf(&output, "const MaxInFlightRequests = %d\n", manifest.Limits.MaxInFlightRequests)
	fmt.Fprintf(&output, "const MaxLineBytes = %d\n", manifest.Limits.MaxLineBytes)
	fmt.Fprintf(&output, "const MaxReportBytes = %d\n", manifest.Limits.MaxReportBytes)
	fmt.Fprintf(&output, "const MaxRequestBytes = %d\n", manifest.Limits.MaxRequestBytes)
	fmt.Fprintf(&output, "const MaxResponseBytes = %d\n", manifest.Limits.MaxResponseBytes)
	fmt.Fprintf(&output, "const ReportRetentionDays = %d\n\n", manifest.Limits.ReportRetentionDays)

	output.WriteString("type Method string\n\nconst (\n")
	for _, method := range manifest.Methods {
		fmt.Fprintf(&output, "\tMethod%s Method = %s\n", methodConstantName(method), quoted(method))
	}
	output.WriteString(")\n\n")

	if err := renderEnum(&output, "ErrorKind", manifest.ErrorKinds); err != nil {
		return "", err
	}
	if err := renderEnum(&output, "ManagedRunState", []string{"abandoned", "active", "prepared"}); err != nil {
		return "", err
	}
	for _, enum := range []struct {
		name   string
		schema string
		path   []string
	}{
		{name: "AbandonDisposition", schema: "schemas/abandon.request.schema.json", path: []string{"params", "disposition"}},
		{name: "AbandonReason", schema: "schemas/abandon.request.schema.json", path: []string{"params", "reason"}},
		{name: "HealthStatus", schema: "schemas/health.response.schema.json", path: []string{"result", "status"}},
		{name: "ReportKind", schema: "schemas/report.request.schema.json", path: []string{"params", "kind"}},
		{name: "ServiceScope", schema: "schemas/handshake.request.schema.json", path: []string{"params", "requestedScopes", "items"}},
	} {
		node, err := findNode(schemas, enum.schema, enum.path...)
		if err != nil {
			return "", err
		}
		values, err := enumStrings(node)
		if err != nil {
			return "", fmt.Errorf("read %s values: %w", enum.name, err)
		}
		if err := renderEnum(&output, enum.name, values); err != nil {
			return "", err
		}
	}
	terminalTransition, err := findNode(
		schemas,
		"schemas/abandon.response.schema.json",
		"result",
		"terminalTransition",
	)
	if err != nil {
		return "", err
	}
	terminalTransitionValue, err := rawString(terminalTransition.Const)
	if err != nil {
		return "", fmt.Errorf("read AbandonTerminalTransition value: %w", err)
	}
	if err := renderEnum(&output, "AbandonTerminalTransition", []string{terminalTransitionValue}); err != nil {
		return "", err
	}

	output.WriteString("func (failure RPCError) Error() string { return failure.Message }\n\n")
	for _, schema := range schemas {
		fmt.Fprintf(&output, "const %s = %s\n\n", schema.ConstName, strconv.Quote(string(schema.Contents)))
	}
	return output.String(), nil
}

func renderEnum(output *bytes.Buffer, name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("enum %s has no values", name)
	}
	fmt.Fprintf(output, "type %s string\n\nconst (\n", name)
	for _, value := range values {
		fmt.Fprintf(output, "\t%s%s %s = %s\n", name, exportedName(value), name, quoted(value))
	}
	output.WriteString(")\n\n")
	fmt.Fprintf(output, "func (value %s) Valid() bool {\n\tswitch value {\n\tcase ", name)
	for index, value := range values {
		if index > 0 {
			output.WriteString(", ")
		}
		fmt.Fprintf(output, "%s%s", name, exportedName(value))
	}
	output.WriteString(":\n\t\treturn true\n\tdefault:\n\t\treturn false\n\t}\n}\n\n")
	return nil
}

func findNode(schemas []schemaSpec, path string, properties ...string) (schemaNode, error) {
	var current schemaNode
	found := false
	for _, schema := range schemas {
		if schema.Path == path {
			current = schema.Node
			found = true
			break
		}
	}
	if !found {
		return schemaNode{}, fmt.Errorf("schema %q is not pinned", path)
	}
	for _, property := range properties {
		if property == "items" {
			if current.Items == nil {
				return schemaNode{}, fmt.Errorf("schema path %q has no items", strings.Join(properties, "."))
			}
			current = *current.Items
			continue
		}
		next, exists := current.Properties[property]
		if !exists {
			return schemaNode{}, fmt.Errorf("schema path %q is absent", strings.Join(properties, "."))
		}
		current = next
	}
	return current, nil
}

func methodConstantName(method string) string {
	parts := strings.FieldsFunc(method, func(character rune) bool { return character == '.' || character == '-' || character == '_' })
	for index, part := range parts {
		parts[index] = exportedName(part)
	}
	return strings.Join(parts, "")
}
