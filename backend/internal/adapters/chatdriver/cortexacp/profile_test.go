package cortexacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConstructorRejectsNestedSourceProfileLinksBeforeNativeStartup(t *testing.T) {
	for _, relative := range []string{".snowflake", ".snowflake/cortex", ".snowflake/cortex/logs", ".snowflake/cortex/conversations", ".snowflake/cortex/settings.json", ".snowflake/cortex/permissions.json", ".snowflake/cortex/logs/coco.log", ".config", ".claude"} {
		t.Run(relative, func(t *testing.T) {
			driver, cfg := protocolDriver(t)
			source := t.TempDir()
			sentinel := filepath.Join(source, "source-must-not-change")
			if err := os.WriteFile(sentinel, []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(cfg.Env["HOME"], filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			target := source
			if filepath.Ext(link) != "" && filepath.Base(link) != ".snowflake" && filepath.Base(link) != ".config" && filepath.Base(link) != ".claude" {
				target = sentinel
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			conv, err := driver.Start(context.Background(), cfg)
			if conv != nil {
				closeFixture(t, conv)
			}
			if !errors.Is(err, ports.ErrChatDriverIncompatible) {
				t.Fatalf("source link admitted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg.Env["HOME"], "protocol-calls")); !os.IsNotExist(err) {
				t.Fatalf("native started through source profile: %v", err)
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "preserved" {
				t.Fatal("source profile was modified")
			}
		})
	}
}

func TestProfileAdmissionAllowsOwnedRegularStateAndBoundsMetadata(t *testing.T) {
	cfg := isolatedConfig(t)
	dir := filepath.Join(cfg.Env["HOME"], ".snowflake", "cortex", "conversations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "native.json"), []byte("private native bytes are not parsed by profile admission"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateProfile(context.Background(), cfg.Env["HOME"]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateProfile(ctx, cfg.Env["HOME"]); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled traversal: %v", err)
	}
	// A private profile should be small and intentionally copied. Fail closed on
	// unbounded state rather than traversing a large source profile.
	for i := 0; i < 4097; i++ {
		if _, err := os.MkdirTemp(cfg.Env["HOME"], "entry-"); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateProfile(context.Background(), cfg.Env["HOME"]); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("oversized profile admitted: %v", err)
	}
}
