package zcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// Native hook_context is silently sliced at 24,000 JavaScript characters.
// A byte bound is conservative for valid UTF-8 and reserves space for native
// event labels/numbering. It does not bound other hooks' combined output.
const maxStandingInstructionBytes = 20_000
const nativeHookOutputBytes = 32_768

func validateStandingInstructions(ctx context.Context, text, path string) error {
	_, err := ReadStandingInstructions(ctx, text, path)
	return err
}

// ReadStandingInstructions reads the complete private input or refuses it;
// it never returns a silently truncated native reminder.
func ReadStandingInstructions(ctx context.Context, text, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if path != "" {
		file, err := os.Open(path) //nolint:gosec // AO-managed prompt path supplied by session manager.
		if err != nil {
			return "", fmt.Errorf("zcode: read standing instructions: %w", err)
		}
		defer func() { _ = file.Close() }()
		data, err := io.ReadAll(io.LimitReader(file, maxStandingInstructionBytes+1))
		if err != nil {
			return "", fmt.Errorf("zcode: read standing instructions: %w", err)
		}
		text = string(data)
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("zcode: standing instructions must be valid UTF-8")
	}
	if len(text) > maxStandingInstructionBytes {
		return "", fmt.Errorf("zcode: standing instructions exceed the supported %d-byte native hook context budget", maxStandingInstructionBytes)
	}
	// The native command runner also bounds serialized stdout. JSON escaping
	// can expand valid input beyond that bound before hook_context is parsed.
	encoded, err := json.Marshal(struct {
		AdditionalContext string `json:"additionalContext"`
	}{text})
	if err != nil {
		return "", err
	}
	if len(encoded)+1 > nativeHookOutputBytes {
		return "", fmt.Errorf("zcode: standing instructions exceed the serialized native hook output budget")
	}
	return text, nil
}
