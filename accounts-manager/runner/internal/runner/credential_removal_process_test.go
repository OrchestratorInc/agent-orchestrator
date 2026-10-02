package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const removalProcessEnvironment = "AO_TEST_REMOVAL_PROCESS"

func TestCredentialRemovalCrashHelper(t *testing.T) {
	phase := os.Getenv(removalProcessEnvironment)
	if phase == "" {
		return
	}
	http.DefaultTransport = runnerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("external transport disabled in removal fixture")
	})
	work := func(ctx context.Context, account string) error {
		if account != "removal-account-a" {
			return nil
		}
		if phase != "pending" {
			fmt.Println("forbidden removed-account provider request")
			return errCredentialFenced
		}
		fmt.Println("removal worker entered")
		<-ctx.Done()
		fmt.Println("removal worker cancelled")
		// Only process death releases this deliberately uncooperative cleanup.
		<-make(chan struct{})
		return ctx.Err()
	}
	err := serveWithCredentialSetup(t.Context(), os.Getenv("AO_TEST_REMOVAL_STATE"), func(source *coreauth.Manager) {
		source.RegisterExecutor(credentialRefreshExecutor{refresh: func(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
			account, _ := auth.Metadata["account_id"].(string)
			if err := work(ctx, account); err != nil {
				return nil, err
			}
			return auth, nil
		}})
	}, func(runtime *credentialRuntime) {
		runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if err := work(request.Context(), request.Header.Get("ChatGPT-Account-Id")); err != nil {
				return nil, err
			}
			return successfulCredentialCheck(request)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCredentialRemovalProcessRecovery(t *testing.T) {
	for _, kind := range []string{"refresh", "quota", "recheck"} {
		t.Run(kind, func(t *testing.T) {
			stateDir, _ := validStateFixture(t)
			port := reservePort(t)
			replaceInFile(t, filepath.Join(stateDir, configFileName), "43127", strconv.Itoa(port))
			vault, err := openCredentialVault(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = vault.Close() })
			auths := make(map[string]*coreauth.Auth)
			for _, name := range []string{"a", "b"} {
				fixture := vaultFixture()
				fixture.Metadata["account_id"] = "removal-account-" + name
				fixture.Metadata["expired"] = time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339)
				fixture.LastRefreshedAt = time.Now()
				if kind == "recheck" {
					delete(fixture.Metadata, "refresh_token")
				}
				if err := vault.Begin(t.Context(), "connect-"+name, fixture.Provider, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				auth, err := vault.Commit(t.Context(), "connect-"+name, fixture)
				if err != nil {
					t.Fatal(err)
				}
				if err := vault.recordVerification(t.Context(), auth, true); err != nil {
					t.Fatal(err)
				}
				auths[name] = auth
			}
			if err := vault.Close(); err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			r := &parallelRunner{t: t, stateDir: stateDir, baseURL: "http://127.0.0.1:" + strconv.Itoa(port),
				client: &http.Client{Transport: transport, Timeout: 10 * time.Second}, logs: &lockedBuffer{}}
			t.Cleanup(func() {
				r.stop()
				for _, marker := range []string{"vault-secret", "vault-private@example.invalid", "management-secret", "control-secret", "WARNING: DATA RACE"} {
					if strings.Contains(r.logs.String(), marker) {
						t.Error("subprocess diagnostics exposed a secret or a data race")
					}
				}
				assertVaultHasNoSecret(t, stateDir)
			})
			start := func(phase string) {
				t.Helper()
				r.command = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCredentialRemovalCrashHelper$", "-test.timeout=40s")
				r.command.Env = append(os.Environ(), removalProcessEnvironment+"="+phase, "AO_TEST_REMOVAL_STATE="+stateDir,
					"GIN_MODE=release", "GORACE=atexit_sleep_ms=0")
				r.command.Stdout, r.command.Stderr = r.logs, r.logs
				if err := r.command.Start(); err != nil {
					t.Fatal(err)
				}
				waitForRunnerHealth(t, r.baseURL)
			}
			crash := func() {
				t.Helper()
				if r.command == nil || r.command.Process == nil {
					t.Fatal("no owned runner process to crash")
				}
				killErr := r.command.Process.Kill()
				waitErr := r.command.Wait()
				state := r.command.ProcessState
				r.command = nil
				r.client.CloseIdleConnections()
				var exitErr *exec.ExitError
				if killErr != nil || !errors.As(waitErr, &exitErr) || state == nil || state.Success() {
					t.Fatal("owned runner did not terminate through the explicit crash cut")
				}
			}
			waitMarker := func(marker string) {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for !strings.Contains(r.logs.String(), marker) {
					if time.Now().After(deadline) {
						t.Fatal("subprocess did not reach the expected lifecycle barrier", marker)
					}
					time.Sleep(time.Millisecond)
				}
			}
			inventory := func(wantA bool) {
				t.Helper()
				raw := r.control(http.MethodGet, credentialPath, nil, http.StatusOK)
				var response struct {
					Files []credentialRecord `json:"files"`
				}
				if json.Unmarshal(raw, &response) != nil {
					t.Fatal("invalid process inventory")
				}
				seenA, seenB := false, false
				for _, record := range response.Files {
					switch record.AuthIndex {
					case auths["a"].Index:
						seenA = true
						if !record.Disabled || record.Verification == "verified" {
							t.Fatal("fresh process authorized the removing account")
						}
					case auths["b"].Index:
						seenB = true
						if record.Disabled || record.Verification != "verified" {
							t.Fatal("A removal affected the unrelated account")
						}
					default:
						t.Fatal("unexpected account in process inventory")
					}
				}
				if seenA != wantA || !seenB || len(response.Files) != 1+boolCount(wantA) {
					t.Fatal("process recovery lost or recreated an account")
				}
			}
			verifyB := func() {
				t.Helper()
				raw := r.control(http.MethodGet, credentialPath+"/quota?ref="+auths["b"].Index, nil, http.StatusOK)
				var observed credentialQuota
				if json.Unmarshal(raw, &observed) != nil || len(observed.Groups) != 1 || len(observed.Groups[0].Buckets) != 1 || observed.Groups[0].Buckets[0].RemainingFraction != .75 {
					t.Fatal("unrelated account lost its positive usage observation")
				}
			}
			start("pending")
			type controlResult struct {
				status int
				err    error
			}
			requestDone := make(chan controlResult, 1)
			path, method := credentialPath+"/refresh?ref="+auths["a"].Index, http.MethodPost
			if kind == "quota" {
				path, method = credentialPath+"/quota?ref="+auths["a"].Index, http.MethodGet
			}
			go func() {
				status, _, err := r.call(t.Context(), method, path, "management-secret", nil)
				requestDone <- controlResult{status: status, err: err}
			}()
			waitMarker("removal worker entered")
			removed := make(chan controlResult, 1)
			go func() {
				status, _, err := r.call(t.Context(), http.MethodDelete, credentialPath+"?ref="+auths["a"].Index, "management-secret", nil)
				removed <- controlResult{status: status, err: err}
			}()
			waitMarker("removal worker cancelled")
			select {
			case result := <-removed:
				t.Fatalf("removal returned before the process crash: status=%d transportError=%t", result.status, result.err != nil)
			default:
			}
			inventory(true)
			verifyB()
			crash()
			if result := <-removed; result.err == nil || result.status != 0 {
				t.Fatal("crashed removal unexpectedly returned a response")
			}
			if result := <-requestDone; result.status != http.StatusConflict && (result.status != 0 || result.err == nil) {
				t.Fatalf("pending worker returned neither fenced nor disconnected: status=%d transportError=%t", result.status, result.err != nil)
			}
			start("recovered")
			inventory(true)
			for _, action := range []string{"quota", "refresh"} {
				method := http.MethodGet
				if action == "refresh" {
					method = http.MethodPost
				}
				r.control(method, credentialPath+"/"+action+"?ref="+auths["a"].Index, nil, http.StatusConflict)
			}
			r.control(http.MethodPatch, credentialPath+"/status?ref="+auths["a"].Index, map[string]bool{"disabled": false}, http.StatusConflict)
			verifyB()
			for range 2 {
				r.control(http.MethodDelete, credentialPath+"?ref="+auths["a"].Index, nil, http.StatusNoContent)
			}
			inventory(false)
			crash()
			start("erased")
			inventory(false)
			r.control(http.MethodGet, credentialPath+"/quota?ref="+auths["a"].Index, nil, http.StatusNotFound)
			verifyB()
			crash()
			if strings.Contains(r.logs.String(), "forbidden removed-account provider request") {
				t.Fatal("fresh process dispatched a removed-account request")
			}
			reopened, err := openCredentialVault(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			entry := reopened.state.Records[auths["a"].ID]
			if !entry.Deleted || len(entry.Sealed) != 0 || entry.Generation != 2 || reopened.removingLocked(auths["a"].ID) {
				t.Fatal("fresh process did not finish exactly one removal generation")
			}
		})
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
