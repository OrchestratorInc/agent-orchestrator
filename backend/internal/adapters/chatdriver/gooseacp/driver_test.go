package gooseacp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigureStartsACPSubcommand(t *testing.T) {
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := []string{"acp"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if env["GOOSE_MODE"] != "approve" {
		t.Fatalf("default GOOSE_MODE = %q, want approve", env["GOOSE_MODE"])
	}
}

func TestConfigureMapsPermissionModesToGooseMode(t *testing.T) {
	tests := map[ports.PermissionMode]string{
		"":                                    "approve",
		ports.PermissionModeDefault:           "approve",
		ports.PermissionMode("unknown"):       "approve",
		ports.PermissionModeAcceptEdits:       "smart_approve",
		ports.PermissionModeAuto:              "auto",
		ports.PermissionModeBypassPermissions: "auto",
	}
	for mode, want := range tests {
		args, env, err := configure(context.Background(), acpdriver.LaunchConfig{
			Permissions: mode, Env: map[string]string{"GOOSE_MODE": "auto"},
		})
		if err != nil {
			t.Fatalf("configure(%q): %v", mode, err)
		}
		if wantArgs := []string{"acp"}; !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("mode %q: args = %#v, want %#v", mode, args, wantArgs)
		}
		if env["GOOSE_MODE"] != want {
			t.Fatalf("mode %q: GOOSE_MODE = %q, want %q", mode, env["GOOSE_MODE"], want)
		}
	}
}

func TestApprovalTransitionsCompareLaunchedGooseMode(t *testing.T) {
	validate := acpdriver.ApprovalFixedAtLaunch("Goose ACP approval mode", gooseMode)
	for _, tt := range []struct {
		name               string
		initial, requested ports.PermissionMode
		wantRestart        bool
	}{
		{"implicit default to explicit default", "", ports.PermissionModeDefault, false},
		{"auto to bypass", ports.PermissionModeAuto, ports.PermissionModeBypassPermissions, false},
		{"bypass to auto", ports.PermissionModeBypassPermissions, ports.PermissionModeAuto, false},
		{"auto to default", ports.PermissionModeAuto, ports.PermissionModeDefault, true},
		{"default to auto", ports.PermissionModeDefault, ports.PermissionModeAuto, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.initial, ports.ChatTurnSettings{Approval: tt.requested})
			if tt.wantRestart {
				if !errors.Is(err, acpdriver.ErrACPSetterUnsupported) {
					t.Fatalf("error = %v, want restart required", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
