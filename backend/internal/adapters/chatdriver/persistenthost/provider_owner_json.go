package persistenthost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

func validateProviderOwnerJSON(raw []byte) error {
	fields, err := uniqueProviderOwnerFields(raw, []string{"version", "session", "identity", "state", "boot", "group", "processSession", "members", "proof", "hostProtocol"})
	if err != nil {
		return err
	}
	if members, exists := fields["members"]; exists {
		var records []json.RawMessage
		if err := json.Unmarshal(members, &records); err != nil {
			return ErrOwnershipInconclusive
		}
		for _, record := range records {
			if _, err := uniqueProviderOwnerFields(record, []string{"pid", "start"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func uniqueProviderOwnerFields(raw []byte, allowed []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrOwnershipInconclusive
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrOwnershipInconclusive
		}
		key, ok := token.(string)
		if !ok || !slices.Contains(allowed, key) {
			return nil, ErrOwnershipInconclusive
		}
		if _, exists := fields[key]; exists {
			return nil, ErrOwnershipInconclusive
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, ErrOwnershipInconclusive
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, ErrOwnershipInconclusive
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, ErrOwnershipInconclusive
	}
	return fields, nil
}
