package comiswire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"
)

type wireSchema struct {
	AdditionalProperties *bool                 `json:"additionalProperties"`
	AnyOf                []wireSchema          `json:"anyOf"`
	Const                json.RawMessage       `json:"const"`
	Enum                 []json.RawMessage     `json:"enum"`
	ExclusiveMinimum     *json.Number          `json:"exclusiveMinimum"`
	Format               string                `json:"format"`
	Items                *wireSchema           `json:"items"`
	Maximum              *json.Number          `json:"maximum"`
	MaxItems             *int                  `json:"maxItems"`
	MaxLength            *int                  `json:"maxLength"`
	Minimum              *json.Number          `json:"minimum"`
	MinItems             *int                  `json:"minItems"`
	MinLength            *int                  `json:"minLength"`
	OneOf                []wireSchema          `json:"oneOf"`
	Pattern              string                `json:"pattern"`
	Properties           map[string]wireSchema `json:"properties"`
	Required             []string              `json:"required"`
	Type                 string                `json:"type"`
}

func validateGeneratedDocument(schemaText string, document any) error {
	contents, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode generated document: %w", err)
	}
	if len(contents) > MaxLineBytes {
		return fmt.Errorf("generated document exceeds %d bytes", MaxLineBytes)
	}
	return validateGeneratedJSON(schemaText, contents)
}

func validateGeneratedJSON(schemaText string, contents []byte) error {
	var schema wireSchema
	schemaDecoder := json.NewDecoder(bytes.NewBufferString(schemaText))
	schemaDecoder.UseNumber()
	if err := schemaDecoder.Decode(&schema); err != nil {
		return fmt.Errorf("decode generated schema: %w", err)
	}
	value, err := decodeStrictValue(contents)
	if err != nil {
		return err
	}
	return validateWireValue(schema, value, "$")
}

func validateWireValue(schema wireSchema, value any, path string) error {
	if len(schema.AnyOf) > 0 {
		return validateUnion(schema.AnyOf, value, path, false)
	}
	if len(schema.OneOf) > 0 {
		return validateUnion(schema.OneOf, value, path, true)
	}
	if len(schema.Const) > 0 {
		constant, err := decodeStrictValue(schema.Const)
		if err != nil {
			return fmt.Errorf("decode %s constant: %w", path, err)
		}
		if !equalJSONValue(value, constant) {
			return fmt.Errorf("%s differs from its constant", path)
		}
	}
	if len(schema.Enum) > 0 {
		matched := false
		for _, encoded := range schema.Enum {
			candidate, err := decodeStrictValue(encoded)
			if err != nil {
				return fmt.Errorf("decode %s enum: %w", path, err)
			}
			matched = matched || equalJSONValue(value, candidate)
		}
		if !matched {
			return fmt.Errorf("%s is outside its closed enum", path)
		}
	}
	switch schema.Type {
	case "object":
		return validateWireObject(schema, value, path)
	case "array":
		return validateWireArray(schema, value, path)
	case "string":
		return validateWireString(schema, value, path)
	case "integer":
		return validateWireNumber(schema, value, path, true)
	case "number":
		return validateWireNumber(schema, value, path, false)
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s is not a boolean", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s is not null", path)
		}
	case "":
		return nil
	default:
		return fmt.Errorf("%s uses unsupported schema type %q", path, schema.Type)
	}
	return nil
}

func validateWireObject(schema wireSchema, value any, path string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("%s is not an object", path)
	}
	for _, required := range schema.Required {
		if _, exists := object[required]; !exists {
			return fmt.Errorf("%s.%s is required", path, required)
		}
	}
	for name, childValue := range object {
		childSchema, exists := schema.Properties[name]
		if !exists {
			if schema.AdditionalProperties != nil && !*schema.AdditionalProperties {
				return fmt.Errorf("%s.%s is unknown", path, name)
			}
			continue
		}
		if err := validateWireValue(childSchema, childValue, path+"."+name); err != nil {
			return err
		}
	}
	return nil
}

func validateWireArray(schema wireSchema, value any, path string) error {
	array, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%s is not an array", path)
	}
	if schema.MinItems != nil && len(array) < *schema.MinItems {
		return fmt.Errorf("%s has fewer than %d items", path, *schema.MinItems)
	}
	if schema.MaxItems != nil && len(array) > *schema.MaxItems {
		return fmt.Errorf("%s has more than %d items", path, *schema.MaxItems)
	}
	if schema.Items != nil {
		for index, item := range array {
			if err := validateWireValue(*schema.Items, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateWireString(schema wireSchema, value any, path string) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", path)
	}
	length := utf8.RuneCountInString(text)
	if schema.MinLength != nil && length < *schema.MinLength {
		return fmt.Errorf("%s is shorter than %d characters", path, *schema.MinLength)
	}
	if schema.MaxLength != nil && length > *schema.MaxLength {
		return fmt.Errorf("%s is longer than %d characters", path, *schema.MaxLength)
	}
	if schema.Pattern != "" {
		patternText := strings.ReplaceAll(schema.Pattern, `\u0000`, `\x00`)
		pattern, err := regexp.Compile(patternText)
		if err != nil {
			return fmt.Errorf("compile %s pattern: %w", path, err)
		}
		if !pattern.MatchString(text) {
			return fmt.Errorf("%s does not match its pattern", path)
		}
	}
	return nil
}

func validateWireNumber(schema wireSchema, value any, path string, integer bool) error {
	number, ok := value.(json.Number)
	if !ok {
		return fmt.Errorf("%s is not a number", path)
	}
	rational, ok := new(big.Rat).SetString(string(number))
	if !ok {
		return fmt.Errorf("%s is not a valid number", path)
	}
	if integer && !rational.IsInt() {
		return fmt.Errorf("%s is not an integer", path)
	}
	for _, bound := range []struct {
		value     *json.Number
		name      string
		inclusive bool
	}{
		{value: schema.Minimum, name: "minimum", inclusive: true},
		{value: schema.ExclusiveMinimum, name: "exclusive minimum", inclusive: false},
		{value: schema.Maximum, name: "maximum", inclusive: true},
	} {
		if bound.value == nil {
			continue
		}
		limit, valid := new(big.Rat).SetString(string(*bound.value))
		if !valid {
			return fmt.Errorf("%s has invalid %s", path, bound.name)
		}
		comparison := rational.Cmp(limit)
		if (bound.name == "maximum" && comparison > 0) || (bound.name != "maximum" && (comparison < 0 || (!bound.inclusive && comparison == 0))) {
			return fmt.Errorf("%s violates its %s", path, bound.name)
		}
	}
	return nil
}

func validateUnion(variants []wireSchema, value any, path string, exactlyOne bool) error {
	matches := 0
	for _, variant := range variants {
		if validateWireValue(variant, value, path) == nil {
			matches++
		}
	}
	if matches == 0 || (exactlyOne && matches != 1) {
		return fmt.Errorf("%s does not match its closed union", path)
	}
	return nil
}

func decodeStrictValue(contents []byte) (any, error) {
	if !utf8.Valid(contents) {
		return nil, fmt.Errorf("JSON is not valid UTF-8")
	}
	if err := rejectDuplicateJSONNames(contents); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if err := expectJSONEnd(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func equalJSONValue(left, right any) bool {
	leftEncoded, leftErr := json.Marshal(left)
	rightEncoded, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftEncoded, rightEncoded)
}

func expectJSONEnd(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("read JSON end: %w", err)
	}
	return nil
}
