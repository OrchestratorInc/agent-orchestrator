package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAlternativesSplitsTopLevelAlternation(t *testing.T) {
	cases := map[string][]string{
		`^Test(A|B)$`:        {`^TestA$`, `^TestB$`},
		`^(TestA|TestB)$`:    {`^TestA$`, `^TestB$`},
		`Destroy|Discard`:    {`Destroy`, `Discard`},
		`^TestCue`:           {`^TestCue`},
		`^TestOnlyOne$`:      {`^TestOnlyOne$`},
		`Cloudflared|Tunnel`: {`Cloudflared`, `Tunnel`},
		// A quantified group is kept whole; non-capturing and nested groups
		// expand; | inside a class is not alternation.
		`^Test(A|B)?$`:     {`^Test(A|B)?$`},
		`^Test(?:A|[|]B)$`: {`^TestA$`, `^Test[|]B$`},
		`^Test(A(B|C)|D)$`: {`^TestAB$`, `^TestAC$`, `^TestD$`},
		// The part after an unbracketed slash selects subtests.
		`^TestA$/one|^TestB$/two`:  {`^TestA$`, `^TestB$`},
		`^Test(A/x|B)$`:            {`^TestA/x$`, `^TestB$`},
		`^Test[[:alpha:]/]+$/case`: {`^Test[[:alpha:]/]+$`},
	}
	for pattern, want := range cases {
		alts, err := alternatives(pattern)
		if err != nil {
			t.Fatalf("alternatives(%q): %v", pattern, err)
		}
		got := alts
		if !reflect.DeepEqual(got, want) {
			t.Errorf("alternatives(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestParseInvocation(t *testing.T) {
	inv, ok := parseInvocation(`        run: go test -race -count=1 -run '^Test(A|B)$' ./internal/x/ ./internal/y/...`)
	if !ok {
		t.Fatal("expected an invocation")
	}
	if inv.pattern != `^Test(A|B)$` {
		t.Errorf("pattern = %q", inv.pattern)
	}
	if want := []string{"./internal/x/", "./internal/y/..."}; !reflect.DeepEqual(inv.packages, want) {
		t.Errorf("packages = %q, want %q", inv.packages, want)
	}
	if _, ok := parseInvocation(`run: go test -race -count=1 ./internal/x/`); ok {
		t.Error("a go test without -run is not checked")
	}
	if inv, ok := parseInvocation(`(cd backend && go test ./internal/p -run TestLive -count 1)`); !ok || inv.pattern != "TestLive" {
		t.Errorf("unquoted -run: got %+v, %v", inv, ok)
	}
	if got := splitCommands(`go test ./a -run '^TestA$'; go test ./b -run '^Test(B|C)$' && echo 'x;y'`); len(got) != 4 || got[3] != ` echo 'x;y'` {
		t.Errorf("splitCommands = %q", got)
	}
	if inv, ok := parseInvocation(`(cd backend && go test ./internal/p -run TestLive)`); !ok || inv.pattern != "TestLive" {
		t.Errorf("unquoted -run before a closing paren: got %+v, %v", inv, ok)
	}
}

func TestCheckReportsOnlyMissingAlternatives(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal", "x", "x_test.go"), "package x\n\nimport \"testing\"\n\nfunc TestPresent(t *testing.T) {}\n")
	// Build-constrained tests still count: CI may run them on another OS.
	// go ignores files starting with _ or ., so their tests do not count.
	writeFile(t, filepath.Join(root, "internal", "x", "_old_test.go"), "package x\n\nimport \"testing\"\n\nfunc TestRenamed(t *testing.T) {}\n")
	writeFile(t, filepath.Join(root, "internal", "x", "sub", "w_windows_test.go"), "//go:build windows\n\npackage sub\n\nimport \"testing\"\n\nfunc TestWindowsOnly(t *testing.T) {}\n")

	inv := invocation{source: "ci.yml:1", pattern: `^Test(Present|WindowsOnly|Renamed)$`, packages: []string{"./internal/x/..."}}
	problems := check(inv, root)
	if len(problems) != 1 || !strings.Contains(problems[0], "Renamed") {
		t.Fatalf("problems = %q, want one about Renamed", problems)
	}

	// A subtest selector after the slash does not hide a missing second test.
	slash := invocation{source: "ci.yml:2", pattern: `^TestPresent$/one|^TestRenamed$/two`, packages: []string{"./internal/x"}}
	if problems := check(slash, root); len(problems) != 1 || !strings.Contains(problems[0], "Renamed") {
		t.Fatalf("problems = %q, want one about Renamed", problems)
	}

	inv.packages = []string{"./internal/x"}
	if problems := check(inv, root); len(problems) != 2 {
		t.Fatalf("without /... the subpackage test is out of scope; problems = %q", problems)
	}

	inv.packages = []string{"./internal/missing"}
	if problems := check(inv, root); len(problems) != 1 || !strings.Contains(problems[0], "not found") {
		t.Fatalf("problems = %q, want a missing-package report", problems)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadInvocationsDecodesFoldedRunScalars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ci.yml")
	writeFile(t, path, `jobs:
  a:
    steps:
      - run: >-
          go test -run '^TestFolded$'
          ./internal/x/
      - run: |
          echo hi
          go test -count=1 \
            -run '^TestContinued$' ./internal/y/
      - run: go test -run TestPlain ./internal/z/; go test -run TestOther ./internal/w/
`)
	invs, err := readInvocations(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, inv := range invs {
		got = append(got, inv.pattern+" "+strings.Join(inv.packages, " "))
	}
	want := []string{"^TestFolded$ ./internal/x/", "^TestContinued$ ./internal/y/", "TestPlain ./internal/z/", "TestOther ./internal/w/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("invocations = %q, want %q", got, want)
	}
}
