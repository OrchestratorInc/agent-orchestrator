package primeagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const primeDaemonProbeTimeout = 3 * time.Second

func (p *Plugin) requireDaemonSocket(ctx context.Context, binary string) error {
	p.extensionMu.Lock()
	defer p.extensionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	supported, cached := p.daemonSocketFlag[binary]
	if !cached {
		runner := p.runCommand
		if runner == nil {
			runner = runPrimeCommand
		}
		probeCtx, cancel := context.WithTimeout(ctx, primeDaemonProbeTimeout)
		defer cancel()
		output, err := runner(probeCtx, binary, "", nil, "--help")
		if probeCtx.Err() != nil {
			return fmt.Errorf("cannot verify prime --daemon-socket support: %w", probeCtx.Err())
		}
		if err != nil {
			return fmt.Errorf("cannot verify prime --daemon-socket support: %w", err)
		}
		supported = primeHelpDeclaresDaemonSocket(string(output))
		if p.daemonSocketFlag == nil {
			p.daemonSocketFlag = make(map[string]bool)
		}
		p.daemonSocketFlag[binary] = supported
	}
	if !supported {
		return errors.New("prime binary does not advertise --daemon-socket; cannot isolate the native daemon")
	}
	return nil
}

func primeHelpDeclaresDaemonSocket(help string) bool {
	for _, line := range strings.Split(help, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && len(fields[0]) == 3 && fields[0][0] == '-' && fields[0][2] == ',' {
			fields = fields[1:]
		}
		if len(fields) < 2 || fields[0] != "--daemon-socket" {
			continue
		}
		// Require an option declaration with a value placeholder, not prose,
		// usage/examples mentioning the flag, or a similarly named option.
		if strings.HasPrefix(fields[1], "<") && strings.HasSuffix(fields[1], ">") {
			return true
		}
	}
	return false
}

func primeCommandSessionDir(argv []string, root string) ([]string, error) {
	out := append([]string(nil), argv...)
	found := false
	for i := 1; i < len(out); i++ {
		if out[i] == "--" {
			break
		}
		name, value, equals := strings.Cut(out[i], "=")
		if name != "--session-dir" {
			// These native options consume a value; their prompt/model text is
			// never another option, even when that text begins with a hyphen.
			switch name {
			case "--daemon-socket", "--extension", "--append-system-prompt", "--model", "--thinking", "--cwd", "--resume", "--mode":
				if !equals {
					i++
				}
			}
			continue
		}
		if found {
			return nil, errors.New("prime command has duplicate --session-dir options")
		}
		found = true
		if equals {
			if value == "" {
				return nil, errors.New("prime command has no --session-dir value")
			}
			out[i] = "--session-dir=" + root
			continue
		}
		if i+1 >= len(out) || strings.HasPrefix(out[i+1], "--") {
			return nil, errors.New("prime command has no --session-dir value")
		}
		i++
		out[i] = root
	}
	if !found {
		return nil, errors.New("prime command is missing --session-dir")
	}
	return out, nil
}
