package zcode

import (
	"context"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// Native hook_context is silently sliced at 24,000 JavaScript characters.
// A byte bound is conservative for valid UTF-8 and reserves space for native
// event labels/numbering. It does not bound other hooks' combined output.
const maxStandingInstructionBytes = 20_000

func validateStandingInstructions(ctx context.Context, text, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path != "" {
		file, err := os.Open(path) //nolint:gosec // AO-managed prompt path supplied by session manager.
		if err != nil {
			return fmt.Errorf("zcode: read standing instructions: %w", err)
		}
		defer func() { _ = file.Close() }()
		data, err := io.ReadAll(io.LimitReader(file, maxStandingInstructionBytes+1))
		if err != nil {
			return fmt.Errorf("zcode: read standing instructions: %w", err)
		}
		text = string(data)
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("zcode: standing instructions must be valid UTF-8")
	}
	if len(text) > maxStandingInstructionBytes {
		return fmt.Errorf("zcode: standing instructions exceed the supported %d-byte native hook context budget", maxStandingInstructionBytes)
	}
	return nil
}
