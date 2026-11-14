package persistenthost

import (
	"context"
	"errors"
	"os"
	"strings"
)

// ErrCommandUsage distinguishes invalid arguments from runtime failures.
var ErrCommandUsage = errors.New("chat-host requires <session> <data-dir> <workdir> [acp|unreal <fingerprint>] -- <provider> [args...]")

// RunCommand serves both the packaged host executable and the CLI fallback.
func RunCommand(ctx context.Context, args []string) error {
	cfg, err := commandConfig(args)
	if err != nil {
		return err
	}
	cfg.Env = os.Environ()
	return Run(ctx, cfg)
}

func commandConfig(args []string) (Config, error) {
	protocol := ProtocolRaw
	fingerprint := ""
	separator := 3
	if len(args) > 3 && (args[3] == string(ProtocolACP) || args[3] == string(ProtocolUnreal)) {
		protocol = Protocol(args[3])
		if len(args) > 4 {
			fingerprint = strings.TrimSpace(args[4])
		}
		separator = 5
	}
	if len(args) < separator+2 || args[separator] != "--" || (protocol != ProtocolRaw && fingerprint == "") {
		return Config{}, ErrCommandUsage
	}
	return Config{
		SessionID: strings.TrimSpace(args[0]), DataDir: args[1], Workdir: args[2],
		Argv: args[separator+1:], Protocol: protocol, OwnershipFingerprint: fingerprint,
	}, nil
}
