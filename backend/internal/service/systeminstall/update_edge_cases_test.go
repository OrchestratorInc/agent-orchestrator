package systeminstall

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestShimOwnershipRequiresAnExecutableArgumentInsideExactPackage(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
	for _, pkg := range []string{"codex", "codex-extra"} {
		bin := filepath.Join(root, "node_modules", "@openai", pkg, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "codex.js"), []byte("cli"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name, body string
		want       bool
	}{
		{"cmd", `@"%dp0%\node_modules\@openai\codex\bin\codex.js" %*`, true},
		{"powershell", `& "node$exe" "$basedir/node_modules/@openai/codex/bin/codex.js" $args`, true},
		{"shell", `exec node "$basedir/node_modules/@openai/codex/bin/codex.js" "$@"`, true},
		{"prefix collision", `@"%dp0%\node_modules\@openai\codex-extra\bin\codex.js" %*`, false},
		{"comment", `# "$basedir/node_modules/@openai/codex/bin/codex.js"`, false},
		{"echo argument", `exec echo "$basedir/node_modules/@openai/codex/bin/codex.js"`, false},
		{"assignment", `target="$basedir/node_modules/@openai/codex/bin/codex.js"`, false},
		{"path traversal", `exec node "$basedir/node_modules/@openai/codex/../codex-extra/bin/codex.js"`, false},
		{"missing target", `exec node "$basedir/node_modules/@openai/codex/missing.js"`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			shim := filepath.Join(root, "codex.cmd")
			if err := os.WriteFile(shim, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			owned, _ := nodeShimTargetsPackage(shim, packageRoot)
			if owned != tt.want {
				t.Fatalf("owned = %t, want %t", owned, tt.want)
			}
		})
	}
}

func TestUVInventoryCorrelatesPackageSectionAndResolvedExecutable(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "vibe")
	link := filepath.Join(root, "vibe-link")
	if err := os.WriteFile(binary, []byte("cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, tt := range []struct {
		name, output string
		want         bool
	}{
		{"resolved symlink", "mistral-vibe v1.2.3\n- vibe (" + link + ")\n", true},
		{"different tool section", "mistral-vibe v1.2.3\n- vibe (/missing)\nother-tool v2.0.0\n- vibe (" + binary + ")\n", false},
		{"path suffix", "mistral-vibe v1.2.3\n- vibe (" + binary + "-extra)\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolListContains(tt.output, "mistral-vibe", binary); got != tt.want {
				t.Fatalf("owned = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestPipxInventoryRequiresTheListedExecutable(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 2)
	for _, pkg := range []string{"mistral-vibe", "other-tool"} {
		bin := filepath.Join(root, "pipx", "venvs", pkg, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(bin, "vibe")
		if err := os.WriteFile(path, []byte("cli"), 0o755); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	for _, tagged := range []bool{false, true} {
		var pathValue any = paths[0]
		if tagged {
			pathValue = map[string]string{"__type__": "Path", "__Path__": paths[0]}
		}
		encoded, err := json.Marshal(pathValue)
		if err != nil {
			t.Fatal(err)
		}
		owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, _ []string, stdout, _ io.Writer) error {
			_, err := io.WriteString(stdout, `{"venvs":{"mistral-vibe":{"metadata":{"main_package":{"package":"mistral-vibe","app_paths":[`+string(encoded)+`]}}}}}`)
			return err
		}))
		for i, path := range paths {
			owned, err := owner(context.Background(), path, "pipx", "mistral-vibe", false)
			if err != nil || owned != (i == 0) {
				t.Fatalf("tagged=%t path=%s owned=%t err=%v", tagged, path, owned, err)
			}
		}
	}
}

func TestWingetInventoryRequiresTheExactPackageDirectory(t *testing.T) {
	root := t.TempDir()
	for _, tt := range []struct {
		directory string
		want      bool
	}{
		{"GitHub.Copilot_Microsoft.Winget.Source_8wekyb3d8bbwe", true},
		{"GitHub.CopilotPreview_Microsoft.Winget.Source_8wekyb3d8bbwe", false},
		{"Vendor.Other_Microsoft.Winget.Source_8wekyb3d8bbwe", false},
	} {
		t.Run(tt.directory, func(t *testing.T) {
			dir := filepath.Join(root, "WinGet", "Packages", tt.directory)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(dir, "copilot.exe")
			if err := os.WriteFile(binary, []byte("cli"), 0o755); err != nil {
				t.Fatal(err)
			}
			owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, _ []string, stdout, _ io.Writer) error {
				_, err := io.WriteString(stdout, "GitHub Copilot  GitHub.Copilot  1.2.0  winget\n")
				return err
			}))
			owned, err := owner(context.Background(), binary, "winget", "GitHub.Copilot", false)
			if err != nil || owned != tt.want {
				t.Fatalf("owned=%t want=%t err=%v", owned, tt.want, err)
			}
		})
	}
}

func TestPrereleaseRegistryAliasesMustBeUnambiguous(t *testing.T) {
	for _, tt := range []struct {
		name, tags string
		wantOK     bool
	}{
		{"next publishes beta", `{"latest":"1.9.0","next":"2.0.0-beta.3"}`, true},
		{"aliases agree", `{"preview":"2.0.0-beta.3","next":"2.0.0-beta.3"}`, true},
		{"different family", `{"next":"2.0.0-rc.3"}`, false},
		{"aliases disagree", `{"preview":"2.0.0-beta.4","next":"2.0.0-beta.3"}`, false},
		{"only stable tag", `{"latest":"2.0.0-beta.3"}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return managerHTTPResponse(http.StatusOK, `{"dist-tags":`+tt.tags+`}`), nil
			})}
			current, _ := parseUpdateVersion("2.0.0-beta.1")
			result, err := npmRegistryVersion(context.Background(), client, "@openai/codex", current, "beta")
			if (err == nil) != tt.wantOK || tt.wantOK && result.Latest != "2.0.0-beta.3" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestVersionProbeRejectsAmbiguousOrPartialVersions(t *testing.T) {
	for _, tt := range []struct {
		input, want string
	}{
		{"Node 22.1.0 warning\ncodex 1.2.3", ""},
		{"codex 1.2.3\nruntime 22.1.0", ""},
		{"codex 1.2.3 1.2.4", ""},
		{"codex 1.2.3\nversion 1.2.3", "1.2.3"},
		{"rust-v0.160.1", "0.160.1"},
		{"cli 1.2.3.4.5", ""},
		{"cli 1.2-beta..2", ""},
	} {
		got, ok := findUpdateVersion(tt.input)
		if ok != (tt.want != "") || got.display != tt.want {
			t.Errorf("findUpdateVersion(%q) = %q, %t; want %q", tt.input, got.display, ok, tt.want)
		}
	}
}
