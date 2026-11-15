package providerkeys

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const DeviceSupported = true

func Publish(ctx context.Context, name, value, previous string) error {
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	current, err := systemdValue(ctx, name)
	if err != nil {
		return err
	}
	if alreadyUsers(current, value, previous) {
		return ErrAlreadySet
	}
	if !ownedValue(current, value, previous) {
		return ErrUserValue
	}
	cmd := process.CommandContext(ctx, "systemctl", "--user", "import-environment", name)
	cmd.Env = append(os.Environ(), name+"="+value)
	return cmd.Run()
}

func Unpublish(ctx context.Context, name, value string) error {
	if !validName(name) {
		return errors.New("invalid provider credential")
	}
	current, err := systemdValue(ctx, name)
	if err != nil {
		return err
	}
	if current == "" || current != value {
		return nil
	}
	return process.CommandContext(ctx, "systemctl", "--user", "unset-environment", name).Run()
}

func systemdValue(ctx context.Context, name string) (string, error) {
	out, err := process.CommandContext(ctx, "systemctl", "--user", "show-environment").Output()
	if err != nil {
		return "", err
	}
	return environmentValue(string(out), name), nil
}

func environmentValue(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		raw, ok := strings.CutPrefix(line, name+"=")
		if !ok {
			continue
		}
		if len(raw) >= 3 && strings.HasPrefix(raw, "$'") && strings.HasSuffix(raw, "'") {
			return unescapeC(raw[2 : len(raw)-1])
		}
		return raw
	}
	return ""
}

func unescapeC(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; c {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'x':
			if i+2 < len(s) {
				if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(n))
					i += 2
					continue
				}
			}
			b.WriteString(`\x`)
		default:
			if c >= '0' && c <= '7' && i+2 < len(s) {
				if n, err := strconv.ParseUint(s[i:i+3], 8, 8); err == nil {
					b.WriteByte(byte(n))
					i += 2
					continue
				}
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
