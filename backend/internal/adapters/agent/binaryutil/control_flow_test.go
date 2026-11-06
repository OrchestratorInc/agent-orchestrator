package binaryutil

import (
	"strings"
	"testing"
)

func TestDiscoveryExecutableRejectsConditionalAndPrefixedRedirects(t *testing.T) {
	for _, body := range []string{
		`IF "%X%"=="" autohand %*`,
		`IF DEFINED X autohand`,
		`IF NOT DEFINED X autohand`,
		`IF /I NOT "%X%"=="" autohand %*`,
		`IF 1 EQU 1 autohand`,
		`IF "a" == "a" autohand`,
		`IF ERRORLEVEL 0 autohand`,
		`IF CMDEXTVERSION 1 autohand`,
		"#!/bin/sh\nif autohand; then echo ready; fi",
		"#!/bin/sh\nif ! autohand; then echo ready; fi",
		"#!/bin/sh\nelif autohand; then echo ready; fi",
		"#!/bin/sh\ncommand autohand \"$@\"",
		"#!/bin/sh\ncommand -p -- autohand \"$@\"",
		"#!/bin/sh\nnohup autohand \"$@\"",
		"#!/bin/sh\n/usr/bin/nohup -- autohand \"$@\"",
		"#!/bin/sh\ntime -p autohand \"$@\"",
		"#!/bin/sh\n/usr/bin/time -f format -o output autohand \"$@\"",
		"#!/bin/sh\nsh -c 'autohand \"$@\"'",
		"#!/bin/sh\nbash -lc 'autohand \"$@\"'",
		"#!/bin/sh\n/bin/bash --noprofile -c 'echo ready; autohand'",
		"#!/bin/sh\nsh -c 'sh -c autohand'",
		"#!/bin/sh\ncommand nohup time sh -c autohand",
		`bash.exe -c autohand`,
	} {
		t.Run(body, func(t *testing.T) {
			launcher := writeDiscoveryLauncher(t, t.TempDir(), "grok.cmd", body+"\n")
			if _, err := VerifyExecutable(t.Context(), "grok", launcher); err == nil || !strings.Contains(err.Error(), "autohand") {
				t.Fatalf("verification = %v, want Autohand refused", err)
			}
		})
	}
}

func TestDiscoveryExecutableAllowsConditionalAndPrefixedControls(t *testing.T) {
	for _, body := range []string{
		`IF "%X%"=="autohand" grok`,
		`IF DEFINED autohand grok`,
		`IF /I NOT "%X%"=="" grok`,
		`IF 1 EQU 1 grok`,
		`IF ERRORLEVEL 0 grok`,
		"#!/bin/sh\nif grok; then echo autohand; fi",
		"#!/bin/sh\nif test -f autohand; then grok; fi",
		"#!/bin/sh\nif command -v autohand; then grok; fi",
		"#!/bin/sh\ncommand -V autohand",
		"#!/bin/sh\ncommand -p grok \"$@\"",
		"#!/bin/sh\nnohup -- grok \"$@\"",
		"#!/bin/sh\ntime -f autohand grok \"$@\"",
		"#!/bin/sh\nsh -c 'echo autohand; grok'",
		"#!/bin/sh\nbash -lc 'grok \"$@\"' autohand",
		"#!/bin/sh\n/bin/bash --noprofile -c 'grok \"$1\"' -- autohand",
	} {
		t.Run(body, func(t *testing.T) {
			launcher := writeDiscoveryLauncher(t, t.TempDir(), "grok.cmd", body+"\n")
			if _, err := VerifyExecutable(t.Context(), "grok", launcher); err != nil {
				t.Fatalf("valid launcher refused: %v", err)
			}
		})
	}
}

func TestDiscoveryExecutableRefusesUninspectableShellModes(t *testing.T) {
	for _, body := range []string{
		`sh -c "$DYNAMIC_COMMAND"`,
		"sh -c 'echo $(autohand)'",
		"bash -c 'echo `autohand`'",
		`sh -s`, `sh -`, `sh -c`,
		"#!/bin/sh\ncommand -Z autohand",
		"#!/bin/sh\nnohup -Z autohand",
		`IF UNKNOWN X autohand`,
	} {
		t.Run(body, func(t *testing.T) {
			launcher := writeDiscoveryLauncher(t, t.TempDir(), "grok.cmd", body+"\n")
			if _, err := VerifyExecutable(t.Context(), "grok", launcher); err == nil || !strings.Contains(err.Error(), "cannot inspect") {
				t.Fatalf("verification = %v, want uninspectable mode refused", err)
			}
		})
	}
}
