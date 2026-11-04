package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestCoderLoginPromptsForDeploymentAndPreservesNativeInput(t *testing.T) {
	var output bytes.Buffer
	var gotName string
	var gotArgs []string
	var gotInput string
	deps := Deps{
		LookPath: func(string) (string, error) { t.Fatal("explicit binary must be used"); return "", nil },
		RunInteractiveCommand: func(_ context.Context, name string, args []string, in io.Reader, _, _ io.Writer) error {
			gotName, gotArgs = name, args
			data, err := io.ReadAll(in)
			gotInput = string(data)
			return err
		},
	}
	cmd := newCoderLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed Coder/coder"})
	cmd.SetIn(strings.NewReader(" https://coder.example.test/\r\nnative-token\n"))
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotName != "/managed Coder/coder" || !reflect.DeepEqual(gotArgs, []string{"login", "https://coder.example.test"}) {
		t.Fatalf("native invocation = %q %q", gotName, gotArgs)
	}
	if gotInput != "native-token\n" {
		t.Fatalf("native input = %q; URL prompt must not consume token input", gotInput)
	}
	if !strings.Contains(output.String(), "Coder deployment URL") || strings.Contains(output.String(), "native-token") {
		t.Fatalf("unexpected prompt output: %q", output.String())
	}
}

func TestCoderLoginRejectsInvalidDeploymentBeforeNativeLaunch(t *testing.T) {
	for _, input := range []string{"\n", "coder.example.com\n", "--help\n", "file:///tmp/coder\n", "https://user:secret@coder.example.com\n", "https://coder.example.com?token=secret\n", "https://coder.example.com#fragment\n"} {
		t.Run(input, func(t *testing.T) {
			called := false
			deps := Deps{
				LookPath: func(string) (string, error) { return "/bin/coder", nil },
				RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
					called = true
					return nil
				},
			}
			cmd := newCoderLoginCommand(&commandContext{deps: deps.withDefaults()})
			cmd.SetIn(strings.NewReader(input))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			var usage usageError
			if !errors.As(err, &usage) || called {
				t.Fatalf("login error = %v, native called = %v", err, called)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid URL credentials leaked in error")
			}
		})
	}
}

func TestCoderLoginPropagatesNativeFailure(t *testing.T) {
	sentinel := errors.New("native login failed")
	deps := Deps{
		LookPath: func(string) (string, error) { return "/bin/coder", nil },
		RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
			return sentinel
		},
	}
	cmd := newCoderLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetIn(strings.NewReader("https://coder.example.test\n"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); !errors.Is(err, sentinel) {
		t.Fatalf("native error = %v", err)
	}
}
