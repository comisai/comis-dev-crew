package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func rejectDuplicateJSONKeys(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := walkGitHubJSONValue(decoder, first); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("GitHub response has trailing JSON")
	}
	return nil
}

func walkGitHubJSONValue(decoder *json.Decoder, token json.Token) error {
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return errors.New("GitHub response object key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("GitHub response key %q is duplicated", key)
			}
			seen[key] = struct{}{}
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkGitHubJSONValue(decoder, value); err != nil {
				return err
			}
		}
		return consumeGitHubJSONDelimiter(decoder, '}')
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkGitHubJSONValue(decoder, value); err != nil {
				return err
			}
		}
		return consumeGitHubJSONDelimiter(decoder, ']')
	default:
		return errors.New("GitHub response delimiter is invalid")
	}
}

func consumeGitHubJSONDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return errors.New("GitHub response delimiter is mismatched")
	}
	return nil
}
