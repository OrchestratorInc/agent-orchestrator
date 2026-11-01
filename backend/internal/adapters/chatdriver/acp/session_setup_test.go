package acp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNormalizeAdditionalDirectoriesWithoutCapability(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	child := filepath.Join(root, "child")
	nested := filepath.Join(child, "nested")
	external := filepath.Join(base, "external")
	sibling := root + "-sibling"
	for _, directory := range []string{nested, external, sibling} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cwd  string
		dirs []string
		want string
	}{
		{name: "no directories", cwd: root},
		{name: "contained children", cwd: root, dirs: []string{child, nested}},
		{name: "duplicate cwd and children", cwd: root, dirs: []string{root, root + string(os.PathSeparator) + ".", child, child}},
		{name: "normalized child", cwd: root, dirs: []string{child + string(os.PathSeparator) + "."}},
		{name: "external", cwd: root, dirs: []string{external}, want: "does not support additional workspace directories"},
		{name: "mixed scope", cwd: root, dirs: []string{child, external}, want: "does not support additional workspace directories"},
		{name: "sibling prefix", cwd: root, dirs: []string{sibling}, want: "does not support additional workspace directories"},
		{name: "relative", cwd: root, dirs: []string{"child"}, want: "must be absolute"},
		{name: "empty path", cwd: root, dirs: []string{""}, want: "must be absolute"},
		{name: "missing child", cwd: root, dirs: []string{filepath.Join(root, "missing")}, want: "resolve"},
		{name: "file", cwd: root, dirs: []string{file}, want: "directory"},
		{name: "missing cwd", cwd: filepath.Join(base, "missing"), dirs: []string{filepath.Join(base, "missing")}, want: "resolve"},
		{name: "cwd is a file", cwd: file, dirs: []string{file}, want: "directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeAdditionalDirectories(tc.cwd, tc.dirs, false)
			if tc.want != "" {
				if err == nil || (tc.want != "" && !strings.Contains(err.Error(), tc.want)) {
					t.Fatalf("directories = %v, error = %v, want refusal containing %q", got, err, tc.want)
				}
				return
			}
			if err != nil || len(got) != 0 {
				t.Fatalf("redundant scope = %v, %v, want cwd alone", got, err)
			}
		})
	}
}

func TestNormalizeAdditionalDirectoriesSymlinkContainment(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		ok   bool
	}{
		{name: "internal alias", kind: "inside", ok: true},
		{name: "cwd alias", kind: "cwd", ok: true},
		{name: "outward symlink", kind: "outside"},
		{name: "nested outward symlink", kind: "nested"},
		{name: "external alias pointing inside", kind: "external"},
		{name: "dangling symlink", kind: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "workspace")
			child := filepath.Join(root, "child")
			external := filepath.Join(base, "external")
			for _, directory := range []string{child, filepath.Join(external, "nested")} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			link, target := filepath.Join(root, "link"), child
			cwd, directory := root, link
			switch tc.kind {
			case "cwd":
				link, target = filepath.Join(base, "alias"), root
				cwd, directory = link, filepath.Join(link, "child")
			case "outside", "nested":
				target = external
				if tc.kind == "nested" {
					directory = filepath.Join(link, "nested")
				}
			case "external":
				link = filepath.Join(base, "alias")
				directory = link
			case "missing":
				target = filepath.Join(base, "missing")
			}
			if err := os.Symlink(target, link); err != nil {
				if errors.Is(err, os.ErrPermission) {
					t.Skipf("symlink creation unavailable: %v", err)
				}
				t.Fatal(err)
			}
			got, err := normalizeAdditionalDirectories(cwd, []string{directory}, false)
			if tc.ok && (err != nil || len(got) != 0) {
				t.Fatalf("redundant canonical scope = %v, %v", got, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("unsafe or unresolved scope was omitted: %v", got)
			}
		})
	}
}

func TestNormalizeAdditionalDirectoriesSupportedPreservesScope(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	missing := filepath.Join(root, "not-created")
	got, err := normalizeAdditionalDirectories(root, []string{
		root, external + string(os.PathSeparator) + ".", missing, external,
	}, true)
	if err != nil || !reflect.DeepEqual(got, []string{external, missing}) {
		t.Fatalf("supported additional directories = %v, %v", got, err)
	}
	if _, err := normalizeAdditionalDirectories(root, []string{"relative"}, true); err == nil {
		t.Fatal("supported provider accepted a relative directory")
	}
}

func TestACPAdditionalDirectoriesLifecycle(t *testing.T) {
	for _, mode := range []string{"new", "resume", "load", "fork-resume", "fork-load"} {
		for _, scope := range []string{"contained", "cwd", "unsupported external", "supported external"} {
			t.Run(mode+"/"+scope, func(t *testing.T) {
				root := t.TempDir()
				child := filepath.Join(root, "child")
				if err := os.Mkdir(child, 0o755); err != nil {
					t.Fatal(err)
				}
				directories := []string{root, child, child + string(os.PathSeparator) + "."}
				var want []string
				if scope == "cwd" {
					directories = []string{root, root + string(os.PathSeparator) + "."}
				}
				if strings.Contains(scope, "external") {
					external := t.TempDir()
					directories = append(directories, external)
					want = []string{child, external}
				}
				caps := acpsdk.AgentCapabilities{LoadSession: strings.HasSuffix(mode, "load"),
					SessionCapabilities: acpsdk.SessionCapabilities{
						Resume: &acpsdk.SessionResumeCapabilities{}, Fork: &acpsdk.SessionForkCapabilities{},
					},
				}
				if scope == "supported external" {
					caps.SessionCapabilities.AdditionalDirectories = &acpsdk.SessionAdditionalDirectoriesCapabilities{}
				}
				agent := &fakeAgent{forkID: "child", capabilities: &caps}
				driver := forkTestDriver(agent)
				cfg := ports.ChatStartConfig{WorkspacePath: root, AdditionalDirectories: directories}
				var conv ports.ChatConversation
				var err error
				switch mode {
				case "new":
					conv, err = driver.Start(t.Context(), cfg)
				case "resume", "load":
					conv, err = driver.Resume(t.Context(), ports.ChatResumeConfig{
						WorkspacePath: root, AdditionalDirectories: directories, ProviderConversationID: "owned:provider",
					})
				default:
					conv, err = driver.ForkIntoHost(t.Context(), "owned:parent", "", cfg)
				}
				if conv != nil {
					defer func() { _ = conv.Close() }()
				}
				if scope == "unsupported external" {
					if err == nil || !strings.Contains(err.Error(), "does not support additional workspace directories") {
						t.Fatalf("external scope was not refused: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				agent.mu.Lock()
				defer agent.mu.Unlock()
				if scope == "unsupported external" {
					if agent.newParams.Cwd != "" || agent.resumeCalls != 0 || agent.loadCalls != 0 || agent.forkCalls != 0 {
						t.Fatal("unsupported scope reached a provider session operation")
					}
					return
				}
				var requests []any
				if strings.HasPrefix(mode, "fork-") {
					requests = append(requests, agent.forkParams)
				}
				switch {
				case mode == "new":
					requests = append(requests, agent.newParams)
				case strings.HasSuffix(mode, "load"):
					requests = append(requests, agent.loadParams)
				default:
					requests = append(requests, agent.resumeParams)
				}
				for _, request := range requests {
					raw, err := json.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					var params struct {
						Cwd                   string   `json:"cwd"`
						AdditionalDirectories []string `json:"additionalDirectories"`
					}
					if err := json.Unmarshal(raw, &params); err != nil {
						t.Fatal(err)
					}
					if params.Cwd != root || !reflect.DeepEqual(params.AdditionalDirectories, want) {
						t.Fatalf("provider directory scope = %s, want cwd %q and directories %v", raw, root, want)
					}
					if len(want) == 0 && strings.Contains(string(raw), `"additionalDirectories"`) {
						t.Fatalf("redundant additional directories were sent: %s", raw)
					}
				}
			})
		}
	}
}
