package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentagy "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
)

type agyLoginCall struct {
	name   string
	args   []string
	adcEnv string
	adcSet bool
}

func runAgyLoginForTest(t *testing.T, selection string, lookPath func(string) (string, error), runErr error) ([]agyLoginCall, string, error) {
	t.Helper()
	var calls []agyLoginCall
	var stdout bytes.Buffer
	deps := Deps{
		In:       strings.NewReader(selection),
		Out:      &stdout,
		Err:      io.Discard,
		LookPath: lookPath,
		RunInteractiveCommand: func(_ context.Context, name, _ string, args []string, _ io.Reader, _, _ io.Writer) error {
			value, set := os.LookupEnv(agyADCAuthEnv)
			calls = append(calls, agyLoginCall{name: name, args: append([]string(nil), args...), adcEnv: value, adcSet: set})
			return runErr
		},
	}
	cmd := newAgyLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/agy"})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	err := cmd.Execute()
	return calls, stdout.String(), err
}

func noGcloud(string) (string, error) { return "", errors.New("not on PATH") }

func TestAgyLoginGoogleAccountOpensAgyAndRecordsMode(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv(agyADCAuthEnv, "true") // inherited ADC selection must not leak into Google sign-in

	calls, stdout, err := runAgyLoginForTest(t, "1\n", noGcloud, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(calls) != 1 || calls[0].name != "/managed/bin/agy" || len(calls[0].args) != 0 {
		t.Fatalf("calls = %#v, want one bare launch of the adapter-resolved agy", calls)
	}
	if calls[0].adcSet {
		t.Fatalf("%s = %q was visible to agy during Google sign-in", agyADCAuthEnv, calls[0].adcEnv)
	}
	if got := agentagy.ReadAuthMode(dataDir); got != agentagy.AuthModeGoogle {
		t.Fatalf("recorded auth mode = %q, want google", got)
	}
	for _, fragment := range []string{"/login", "/exit", "Google account sign-in"} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("output missing %q:\n%s", fragment, stdout)
		}
	}
}

func TestAgyLoginGoogleAccountKeepsChoiceWhenAgyExitsNonZero(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv(agyADCAuthEnv, "")

	_, _, err := runAgyLoginForTest(t, "1\n", noGcloud, errors.New("exit status 130"))
	if err == nil {
		t.Fatal("Execute succeeded, want the agy exit error surfaced")
	}
	if got := agentagy.ReadAuthMode(dataDir); got != agentagy.AuthModeGoogle {
		t.Fatalf("recorded auth mode = %q, want google", got)
	}
}

func TestAgyLoginADCRunsGcloudAndRecordsMode(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	lookPath := func(name string) (string, error) {
		if name == "gcloud" {
			return "/opt/google-cloud-sdk/bin/gcloud", nil
		}
		return "", errors.New("not on PATH")
	}
	calls, stdout, err := runAgyLoginForTest(t, "2\n", lookPath, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(calls) != 1 || calls[0].name != "/opt/google-cloud-sdk/bin/gcloud" || strings.Join(calls[0].args, " ") != "auth application-default login" {
		t.Fatalf("calls = %#v, want gcloud auth application-default login", calls)
	}
	if got := agentagy.ReadAuthMode(dataDir); got != agentagy.AuthModeADC {
		t.Fatalf("recorded auth mode = %q, want adc", got)
	}
	if !strings.Contains(stdout, "Application Default Credentials") {
		t.Fatalf("output missing ADC confirmation:\n%s", stdout)
	}
}

func TestAgyLoginADCSkipsGcloudWithServiceAccountFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "sa.json"))

	calls, _, err := runAgyLoginForTest(t, "2\n", noGcloud, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %#v, want no gcloud sign-in", calls)
	}
	if got := agentagy.ReadAuthMode(dataDir); got != agentagy.AuthModeADC {
		t.Fatalf("recorded auth mode = %q, want adc", got)
	}
}

func TestAgyLoginADCFailureDoesNotRecordMode(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	if _, _, err := runAgyLoginForTest(t, "2\n", noGcloud, nil); err == nil || !strings.Contains(err.Error(), "gcloud") {
		t.Fatalf("Execute error = %v, want missing gcloud guidance", err)
	}
	lookPath := func(string) (string, error) { return "/bin/gcloud", nil }
	if _, _, err := runAgyLoginForTest(t, "2\n", lookPath, errors.New("exit status 1")); err == nil {
		t.Fatal("Execute succeeded, want gcloud failure surfaced")
	}
	if got := agentagy.ReadAuthMode(dataDir); got != agentagy.AuthModeNone {
		t.Fatalf("recorded auth mode = %q, want none after failed ADC sign-in", got)
	}
}

func TestAgyLoginRejectsUnknownSelection(t *testing.T) {
	t.Setenv("AO_DATA_DIR", t.TempDir())

	calls, _, err := runAgyLoginForTest(t, "3\n", noGcloud, nil)
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("Execute error = %v, want usage error", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %#v, want none", calls)
	}
}

func TestAgyLoginMenuListsSupportedMethods(t *testing.T) {
	var stdout bytes.Buffer
	if err := writeAgyLoginMenu(&stdout, codexLoginStyle{}); err != nil {
		t.Fatalf("writeAgyLoginMenu: %v", err)
	}
	for _, fragment := range []string{"Sign in to Agy", "Google account", "Google Cloud (ADC)", "Selection [1-2]"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("menu missing %q:\n%s", fragment, stdout.String())
		}
	}
}
