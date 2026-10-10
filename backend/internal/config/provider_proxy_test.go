package config

import (
	"path/filepath"
	"testing"
)

func TestProviderProxyBinaryDevelopmentOverride(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "build with spaces", "ao-proxy-host")
	for _, tc := range []struct {
		name, value string
		invalid     bool
	}{
		{name: "bundled default"},
		{name: "explicit build", value: absolute},
		{name: "relative build rejected", value: "daemon/ao-proxy-host", invalid: true},
		{name: "bare PATH name rejected", value: "ao-proxy-host", invalid: true},
		{name: "whitespace is not a binary", value: " ", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_DATA_DIR", root)
			t.Setenv("AO_RUN_FILE", filepath.Join(root, "running.json"))
			t.Setenv("AO_PROXY_HOST_BINARY", tc.value)
			cfg, err := Load()
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted an ambiguous helper executable")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ProxyHostBinary != tc.value {
				t.Fatalf("helper path = %q, want %q", cfg.ProxyHostBinary, tc.value)
			}
			if cfg.Host != LoopbackHost || cfg.DataDir != root {
				t.Fatal("helper override changed listener or state directory")
			}
		})
	}
}
