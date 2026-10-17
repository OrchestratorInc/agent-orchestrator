package persistenthost

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, test := range []struct {
		name        string
		args        []string
		protocol    Protocol
		fingerprint string
	}{
		{"raw", []string{" session ", "/data", "/work", "--", "provider", "--model", "mini"}, ProtocolRaw, ""},
		{"acp", []string{" session ", "/data", "/work", "acp", " ownership ", "--", "provider", "--model", "mini"}, ProtocolACP, "ownership"},
		{"unreal", []string{" session ", "/data", "/work", "unreal", " ownership ", "--", "provider", "--model", "mini"}, ProtocolUnreal, "ownership"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := ParseArgs(test.args)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.SessionID != "session" || cfg.DataDir != "/data" || cfg.Workdir != "/work" || cfg.Protocol != test.protocol || cfg.OwnershipFingerprint != test.fingerprint {
				t.Fatalf("parsed config = %+v", cfg)
			}
			if !reflect.DeepEqual(cfg.Argv, []string{"provider", "--model", "mini"}) {
				t.Fatalf("provider argv = %v", cfg.Argv)
			}
			if cfg.Env != nil {
				t.Fatalf("parser supplied a process environment: %v", cfg.Env)
			}
		})
	}
}

func TestParseArgsRejectsMalformedHostCommand(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"session", "/data", "/work"},
		{"session", "/data", "/work", "--"},
		{"session", "/data", "/work", "provider", "argument"},
		{"session", "/data", "/work", "acp"},
		{"session", "/data", "/work", "acp", "", "--", "provider"},
		{"session", "/data", "/work", "unreal", " ", "--", "provider"},
		{"session", "/data", "/work", "acp", "ownership", "--"},
		{"session", "/data", "/work", "acp", "ownership", "wrong", "provider"},
		{"session", "/data", "/work", "unknown", "ownership", "--", "provider"},
	} {
		if _, err := ParseArgs(args); err == nil {
			t.Errorf("ParseArgs(%q) accepted malformed command", args)
		}
	}
}
