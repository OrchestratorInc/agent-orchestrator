//go:build windows

package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestAgentProcessSuperviseAppliesEnvPrefixOnWindows(t *testing.T) {
	if os.Getenv("AO_AGENT_PROCESS_ENV_HELPER") == "1" {
		_, _ = fmt.Fprintf(os.Stdout, "%s|%s", os.Getenv("AO_AGENT_PROCESS_TEST_VALUE"), os.Getenv("AO_AGENT_PROCESS_TEST_SECOND"))
		os.Exit(0)
	}

	cfg := setConfigEnv(t)
	srv, _ := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("AO_AGENT_PROCESS_ENV_HELPER", "1")
	// Reproduce a GUI-launched AO process whose PATH has no Unix env utility.
	t.Setenv("PATH", t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader("")},
		"agent-process", "supervise", "--session", "ao-env-1", "--launch", "launch-env-1", "--",
		"env", "AO_AGENT_PROCESS_TEST_VALUE=reproduced", exe, "-test.run=^TestAgentProcessSuperviseAppliesEnvPrefixOnWindows$",
	)
	if err != nil {
		t.Fatalf("supervise failed: %v\nstderr=%s", err, errOut)
	}
	if out != "reproduced|" {
		t.Fatalf("stdout = %q, want reproduced|; stderr=%s", out, errOut)
	}
}

func TestSupervisedProcessCommandOnWindows(t *testing.T) {
	if os.Getenv("AO_AGENT_PROCESS_ENV_HELPER") == "1" {
		_, _ = fmt.Fprintf(os.Stdout, "%s|%s", os.Getenv("AO_AGENT_PROCESS_TEST_VALUE"), os.Getenv("AO_AGENT_PROCESS_TEST_SECOND"))
		os.Exit(0)
	}

	t.Setenv("AO_AGENT_PROCESS_ENV_HELPER", "1")
	t.Setenv("PATH", t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := []string{exe, "-test.run=^TestSupervisedProcessCommandOnWindows$"}

	tests := []struct {
		name string
		argv []string
		want string
	}{
		{
			name: "one variable",
			argv: append([]string{"env", "AO_AGENT_PROCESS_TEST_VALUE=one"}, helper...),
			want: "one|",
		},
		{
			name: "multiple variables and equals in value",
			argv: append([]string{"env", "AO_AGENT_PROCESS_TEST_VALUE=first", "AO_AGENT_PROCESS_TEST_SECOND=left=right"}, helper...),
			want: "first|left=right",
		},
		{
			name: "last assignment wins case insensitively",
			argv: append([]string{"env", "AO_AGENT_PROCESS_TEST_VALUE=first", "ao_agent_process_test_value=last"}, helper...),
			want: "last|",
		},
		{
			name: "no prefix",
			argv: helper,
			want: "ambient|",
		},
	}
	t.Setenv("AO_AGENT_PROCESS_TEST_VALUE", "ambient")
	t.Setenv("AO_AGENT_PROCESS_TEST_SECOND", "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := supervisedProcessCommand(context.Background(), tt.argv)
			if err != nil {
				t.Fatalf("build command: %v", err)
			}
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run command: %v; output=%s", err, got)
			}
			if string(got) != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
		})
	}

	for _, argv := range [][]string{nil, {"env", "A=1"}} {
		if _, err := supervisedProcessCommand(context.Background(), argv); err == nil {
			t.Fatalf("argv %#v should reject an empty command", argv)
		}
	}

	cmd, err := supervisedProcessCommand(context.Background(), []string{"ao-agent-does-not-exist.exe"})
	if err != nil {
		t.Fatalf("build nonexistent command: %v", err)
	}
	if err := cmd.Run(); err == nil {
		t.Fatal("nonexistent executable should fail to start")
	}
}
