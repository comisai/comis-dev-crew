package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func decodeStrict(contents []byte, destination interface{}) error {
	if err := rejectDuplicateNames(contents); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode strict JSON: %w", err)
	}
	if err := expectJSONEnd(decoder); err != nil {
		return err
	}
	return nil
}

func rejectDuplicateNames(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := scanJSONValue(decoder); err != nil {
		return fmt.Errorf("inspect JSON names: %w", err)
	}
	return expectJSONEnd(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return fmt.Errorf("object name has type %T", nameToken)
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("duplicate object name %q", name)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("object ended with %v", end)
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("array ended with %v", end)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func expectJSONEnd(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("read JSON end: %w", err)
	}
	return nil
}
