//go:build darwin

package conpty

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// processArgv reads a process's complete, untruncated argv from the kernel
// (KERN_PROCARGS2), not a display command.
func processArgv(ctx context.Context, pid int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("conpty: read argv of pid %d: %w", pid, err)
	}
	return parseProcArgs2(raw)
}

// parseProcArgs2 decodes KERN_PROCARGS2: argc, the executable path, NUL
// padding, then argc NUL-terminated arguments (followed by the environment).
func parseProcArgs2(raw []byte) ([]string, error) {
	if len(raw) < 4 {
		return nil, errors.New("conpty: procargs too short")
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, errors.New("conpty: procargs missing executable path")
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	argv := make([]string, 0, argc)
	for len(argv) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, errors.New("conpty: procargs argument is not terminated")
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv, nil
}
