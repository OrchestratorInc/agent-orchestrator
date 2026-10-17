package persistenthost

import (
	"errors"
	"strings"
)

// ParseArgs decodes the shared internal chat-host subprocess command line.
// The caller supplies the host context and environment before calling Run.
func ParseArgs(args []string) (Config, error) {
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
		return Config{}, errors.New("chat-host requires <session> <data-dir> <workdir> [acp|unreal <fingerprint>] -- <provider> [args...]")
	}
	return Config{
		SessionID: strings.TrimSpace(args[0]), DataDir: args[1], Workdir: args[2],
		Argv: args[separator+1:], Protocol: protocol, OwnershipFingerprint: fingerprint,
	}, nil
}
