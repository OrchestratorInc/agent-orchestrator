package kiro

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	signedOutWhoami = `{"account":null}`
	signedInWhoami  = `{"accountType":"IamIdentityCenter","email":"user@example.com","region":"eu-west-1","startUrl":"https://d-abc.awsapps.com/start"}

Profile:
KiroProfile-us-east-1
arn:aws:codewhisperer:us-east-1:111122223333:profile/ABCDEF
`
)

func withWhoami(t *testing.T, out string, err error) {
	t.Helper()
	original := authprobe.CmdRunner
	authprobe.CmdRunner = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(out), err
	}
	t.Cleanup(func() { authprobe.CmdRunner = original })
}

func TestKiroWhoamiAuthStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want ports.AgentAuthStatus
	}{
		{"signed in", signedInWhoami, nil, ports.AgentAuthStatusAuthorized},
		{"signed out", signedOutWhoami, errors.New("exit status 1"), ports.AgentAuthStatusUnauthorized},
		{"account object present", `{"account":{"id":"abc"}}`, nil, ports.AgentAuthStatusAuthorized},
		{"unrecognized json", `{"somethingElse":1}`, nil, ports.AgentAuthStatusUnknown},
		{"not json at all", `command not found`, errors.New("exit status 127"), ports.AgentAuthStatusUnknown},
		{"blank identity fields", `{"email":"  "}`, nil, ports.AgentAuthStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withWhoami(t, tc.out, tc.err)
			got, _, _, err := kiroWhoamiAuthDiagnostics(context.Background(), "kiro-cli")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKiroWhoamiIgnoresExitCode(t *testing.T) {
	withWhoami(t, signedInWhoami, errors.New("exit status 1"))
	got, _, _, err := kiroWhoamiAuthDiagnostics(context.Background(), "kiro-cli")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized even on a non-zero exit", got)
	}
}

func TestKiroWhoamiWithoutBinaryIsUnknown(t *testing.T) {
	got, _, _, err := kiroWhoamiAuthDiagnostics(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", got)
	}
}

func TestKiroWhoamiAccount(t *testing.T) {
	withWhoami(t, signedInWhoami, nil)
	status, account, _, err := kiroWhoamiAuthDiagnostics(context.Background(), "kiro-cli")
	if err != nil || status != ports.AgentAuthStatusAuthorized || account.Email != "user@example.com" {
		t.Fatalf("status=%s account=%+v err=%v", status, account, err)
	}
	withWhoami(t, signedOutWhoami, errors.New("exit status 1"))
	if _, account, _, _ := kiroWhoamiAuthDiagnostics(context.Background(), "kiro-cli"); account.Email != "" {
		t.Fatalf("signed-out account = %+v", account)
	}
}

func TestKiroLoginWinsOverEnvironmentKey(t *testing.T) {
	t.Setenv("KIRO_API_KEY", "kiro-env-key")
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	withWhoami(t, signedInWhoami, nil)
	status, account, _, err := plugin.AuthStatusWithDiagnostics(context.Background())
	if err != nil || status != ports.AgentAuthStatusAuthorized || account.Email != "user@example.com" {
		t.Fatalf("signed in: status=%s account=%+v err=%v", status, account, err)
	}

	withWhoami(t, signedOutWhoami, errors.New("exit status 1"))
	status, account, _, err = plugin.AuthStatusWithDiagnostics(context.Background())
	if err != nil || status != ports.AgentAuthStatusAuthorized || account.Source != "KIRO_API_KEY" {
		t.Fatalf("signed out with key: status=%s account=%+v err=%v", status, account, err)
	}
}
