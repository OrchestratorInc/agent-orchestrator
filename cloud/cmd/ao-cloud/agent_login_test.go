package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/config"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

type loginVMFake struct {
	sandbox.Provider
	spec           sandbox.Spec
	lookup         string
	deleted        sandbox.ID
	command        string
	env            map[string]string
	execErr        error
	found          bool
	skippedCleanup bool
	deleting       bool
}

func (v *loginVMFake) Create(_ context.Context, spec sandbox.Spec) (sandbox.Environment, error) {
	v.spec = spec
	v.found = true
	return sandbox.Environment{ID: "private-vm", State: sandbox.StateRunning}, nil
}
func (v *loginVMFake) FindBySession(_ context.Context, id string) (sandbox.Environment, bool, error) {
	v.lookup = id
	return sandbox.Environment{ID: "private-vm"}, v.found, nil
}
func (v *loginVMFake) Delete(ctx context.Context, id sandbox.ID) error {
	v.deleted = id
	v.skippedCleanup = sandbox.CleanupSkipped(ctx)
	return nil
}
func (v *loginVMFake) Get(context.Context, sandbox.ID) (sandbox.Environment, error) {
	if v.deleting {
		return sandbox.Environment{State: sandbox.StateDeleting}, nil
	}
	return sandbox.Environment{}, sandbox.ErrNotFound
}
func (v *loginVMFake) ExecRoot(_ context.Context, _ sandbox.ID, command string, env map[string]string, _ time.Duration) (string, error) {
	v.command = command
	v.env = env
	return "AO_AGENT_LOGIN_STARTED", v.execErr
}

func TestNativeLoginProvisionsOnlyDedicatedComputeAndPassesScopedSecretOutsideCommand(t *testing.T) {
	vms := &loginVMFake{}
	runner := &agentLoginSandboxRunner{vms: vms, publicURL: "https://cloud.test", workerHash: strings.Repeat("a", 64), defaultSnapshot: "clean-base", snapshots: map[string]string{"codex": "codex-base"}}
	id := "bb47ab85-1ae4-4bcc-8a7a-6e6f00b2f115"
	handle, err := runner.Start(context.Background(), agentconnect.Launch{ID: id, OrgID: "org-1", Harness: "codex", Token: "private-runner-token", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if handle != id || vms.spec.SessionID != "login-"+id || vms.spec.RootFS != "codex-base" {
		t.Fatalf("wrong private sandbox: %#v, %s", vms.spec, handle)
	}
	if vms.spec.Labels["ao.agent_login_id"] != id || vms.spec.Labels["ao.session_id"] != "" {
		t.Fatal("login job must not masquerade as a user session")
	}
	if vms.env["AO_AGENT_LOGIN_TOKEN"] != "private-runner-token" || vms.env["AO_AGENT_LOGIN_ID"] != id {
		t.Fatal("missing scoped runner environment")
	}
	if strings.Contains(vms.command, "private-runner-token") || strings.Contains(vms.command, "org-1") {
		t.Fatal("job secret or user identity leaked into launch command")
	}
	if !strings.Contains(vms.command, "/worker/binary/"+strings.Repeat("a", 64)) || !strings.Contains(vms.command, "sha256sum") {
		t.Fatal("login must run the current verified worker, not a stale snapshot binary")
	}
	if err := runner.Stop(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if vms.lookup != "login-"+id || vms.deleted != "private-vm" {
		t.Fatal("cleanup must target the dedicated login sandbox only")
	}
}

func TestNativeLoginNeverLaunchesUntrustedOrExpiredAttempts(t *testing.T) {
	for _, tc := range []struct {
		url, harness string
		deadline     time.Time
	}{
		{"http://cloud.test", "codex", time.Now().Add(time.Minute)},
		{"https://user:secret@cloud.test", "codex", time.Now().Add(time.Minute)},
		{"https://cloud.test/unsupported-prefix", "codex", time.Now().Add(time.Minute)},
		{"https://cloud.test", "unknown", time.Now().Add(time.Minute)},
		{"https://cloud.test", "codex", time.Now().Add(-time.Second)},
	} {
		vms := &loginVMFake{}
		runner := &agentLoginSandboxRunner{vms: vms, publicURL: tc.url, workerHash: strings.Repeat("a", 64), defaultSnapshot: "base"}
		_, err := runner.Start(context.Background(), agentconnect.Launch{ID: "bb47ab85-1ae4-4bcc-8a7a-6e6f00b2f115", Harness: tc.harness, Token: "private", ExpiresAt: tc.deadline})
		if err == nil || vms.spec.SessionID != "" {
			t.Fatal("invalid login should fail before provisioning")
		}
	}
}

func TestNativeLoginCleanupIsIdempotentAndCannotAddressUserSessions(t *testing.T) {
	vms := &loginVMFake{}
	runner := &agentLoginSandboxRunner{vms: vms}
	if err := runner.Stop(context.Background(), "bad-handle"); err == nil {
		t.Fatal("invalid cleanup handle accepted")
	}
	if vms.lookup != "" {
		t.Fatal("invalid handle reached provider")
	}
	if err := runner.Stop(context.Background(), "bb47ab85-1ae4-4bcc-8a7a-6e6f00b2f115"); err != nil {
		t.Fatal(err)
	}
	if vms.deleted != "" {
		t.Fatal("nonexistent sandbox deleted")
	}
	if found, err := runner.Cleanup(context.Background(), "bb47ab85-1ae4-4bcc-8a7a-6e6f00b2f115"); err != nil || found {
		t.Fatal("an absent VM must not falsely confirm cleanup of a still-provisioning job")
	}
	vms.found = true
	if found, err := runner.Cleanup(context.Background(), "bb47ab85-1ae4-4bcc-8a7a-6e6f00b2f115"); err != nil || !found || vms.deleted != "private-vm" {
		t.Fatal("a confirmed private VM deletion must release its cleanup reservation")
	}
	if !vms.skippedCleanup {
		t.Fatal("private authentication compute must bypass project Cleanup, whose nil result can mean deletion has not started")
	}
	vms.deleting = true
	if found, err := runner.Cleanup(context.Background(), "bb47ab85-1ae4-4bcc-8a7a-6e6f00b2f115"); err == nil || found {
		t.Fatal("pending provider deletion must retain the durable cleanup handle")
	}
}

func TestNativeLoginFailedLaunchKeepsRecoverableCleanupHandle(t *testing.T) {
	vms := &loginVMFake{execErr: errors.New("provider error with sensitive details")}
	runner := &agentLoginSandboxRunner{vms: vms, publicURL: "https://cloud.test", workerHash: strings.Repeat("a", 64), defaultSnapshot: "base"}
	id := "bb47ab85-1ae4-4bcc-8a7a-6e6f00b2f115"
	handle, err := runner.Start(context.Background(), agentconnect.Launch{ID: id, Harness: "claude-code", Token: "private", ExpiresAt: time.Now().Add(time.Minute)})
	if err == nil || handle != id || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("failed launch must leave a safe recoverable handle without provider output")
	}
}

func TestNativeLoginDisabledDoesNotRequireProviderOrMutateExistingDeployment(t *testing.T) {
	service, err := newNativeAgentLogins(config.Config{}, nil, nil, nil)
	if err != nil || service != nil {
		t.Fatalf("native login must be opt-in: %v", err)
	}
}

func TestNativeLoginDisabledRetainsCleanupForPreviouslyCreatedJobs(t *testing.T) {
	cfg := config.Config{AvailableSandboxProviders: []string{"freestyle"}, FreestyleAPIKey: "test-only", FreestyleDefaultSnapshot: "base"}
	service, err := newNativeAgentLogins(cfg, &postgres.Store{}, nil, nil)
	if err != nil || service == nil || len(service.Capabilities()) != 0 {
		t.Fatalf("disabled admission must retain a cleanup-only service: %v", err)
	}
}

func TestNativeLoginDisabledPreservesLegacyLongDockerNamespace(t *testing.T) {
	cfg := config.Config{SandboxProvider: "docker", DockerNamespace: strings.Repeat("a", 63), PublicURL: "http://localhost:8081"}
	service, err := newNativeAgentLogins(cfg, &postgres.Store{}, nil, nil)
	if err != nil || service != nil {
		t.Fatalf("disabled optional login must not break a valid legacy Docker namespace: %v", err)
	}
}
