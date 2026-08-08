package localapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func decodeStrict(data []byte, destination any) error {
	if len(data) == 0 {
		return errors.New("JSON value is required")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode strict JSON: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return err
	}
	return nil
}

func decodeObject(data []byte, destination any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("payload must be a JSON object")
	}
	return decodeStrict(trimmed, destination)
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON value: %w", err)
	}
	if err := walkJSONValue(decoder, first); err != nil {
		return err
	}
	return requireJSONEnd(decoder)
}

func walkJSONValue(decoder *json.Decoder, token json.Token) error {
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("read JSON object key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			value, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("read JSON object value: %w", err)
			}
			if err := walkJSONValue(decoder, value); err != nil {
				return err
			}
		}
		return consumeDelimiter(decoder, '}')
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("read JSON array value: %w", err)
			}
			if err := walkJSONValue(decoder, value); err != nil {
				return err
			}
		}
		return consumeDelimiter(decoder, ']')
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func consumeDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("close JSON value: %w", err)
	}
	if token != want {
		return errors.New("mismatched JSON delimiter")
	}
	return nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("read trailing JSON: %w", err)
	}
	return nil
}
