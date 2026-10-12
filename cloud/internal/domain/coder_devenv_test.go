package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func boolPointer(value bool) *bool { return &value }

// The dev-environment fields round-trip through the stored config with
// explicit effective values: the default worker user, checkout reuse on by
// default only for the workspace user, and the ready-check timeout default.
func TestOrgCoderConfigDevEnvironmentRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		in          OrgCoderConfig
		wantUser    string
		wantReuse   bool
		wantProbe   string
		wantTimeout int
	}{
		{name: "defaults", in: OrgCoderConfig{}, wantUser: CoderWorkerUserAOWorker},
		{name: "workspace user reuses by default", in: OrgCoderConfig{WorkerUser: " Workspace "},
			wantUser: CoderWorkerUserWorkspace, wantReuse: true},
		{name: "workspace user may opt out", in: OrgCoderConfig{WorkerUser: "workspace", ReuseExistingCheckout: boolPointer(false)},
			wantUser: CoderWorkerUserWorkspace},
		{name: "ao-worker never reuses", in: OrgCoderConfig{WorkerUser: "ao-worker", ReuseExistingCheckout: boolPointer(true)},
			wantUser: CoderWorkerUserAOWorker},
		{name: "probe gets the default timeout", in: OrgCoderConfig{ReadyProbe: "  grep -q '^Ready' ~/.bootstrap-status  "},
			wantUser: CoderWorkerUserAOWorker, wantProbe: "grep -q '^Ready' ~/.bootstrap-status",
			wantTimeout: DefaultCoderReadyProbeTimeoutSeconds},
		{name: "explicit timeout kept", in: OrgCoderConfig{ReadyProbe: "true", ReadyProbeTimeoutSeconds: 120},
			wantUser: CoderWorkerUserAOWorker, wantProbe: "true", wantTimeout: 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.in.BaseURL = "https://coder.example.com"
			encoded, err := EncodeOrgCoderConfig(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := DecodeOrgCoderConfig(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.WorkerUser != tc.wantUser || cfg.ReadyProbe != tc.wantProbe || cfg.ReadyProbeTimeoutSeconds != tc.wantTimeout {
				t.Fatalf("config = %+v", cfg)
			}
			if cfg.ReuseExistingCheckout == nil || *cfg.ReuseExistingCheckout != tc.wantReuse || cfg.ReusesExistingCheckout() != tc.wantReuse {
				t.Fatalf("reuseExistingCheckout = %v, want %v", cfg.ReuseExistingCheckout, tc.wantReuse)
			}
			var raw map[string]any
			if err := json.Unmarshal(encoded, &raw); err != nil {
				t.Fatal(err)
			}
			if raw["workerUser"] != tc.wantUser || raw["reuseExistingCheckout"] != tc.wantReuse {
				t.Fatalf("stored JSON = %s", encoded)
			}
		})
	}
}

// A config stored before the fields existed decodes to the historical
// behavior.
func TestOrgCoderConfigLegacyRowDecodesToDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := DecodeOrgCoderConfig(json.RawMessage(`{"baseUrl":"https://coder.example.com","owner":"ao","templateId":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerUser != CoderWorkerUserAOWorker || cfg.ReusesExistingCheckout() || cfg.ReadyProbe != "" || cfg.ReadyProbeTimeoutSeconds != 0 {
		t.Fatalf("legacy config = %+v", cfg)
	}
}

func TestOrgCoderConfigValidateDevEnvironment(t *testing.T) {
	t.Parallel()
	valid := []OrgCoderConfig{
		{WorkerUser: CoderWorkerUserAOWorker},
		{WorkerUser: CoderWorkerUserWorkspace, ReadyProbe: "test -f ~/.ready", ReadyProbeTimeoutSeconds: 30},
		{WorkerUser: CoderWorkerUserWorkspace, ReadyProbe: strings.Repeat("x", MaxCoderReadyProbeLength), ReadyProbeTimeoutSeconds: 3600},
	}
	for _, cfg := range valid {
		if err := cfg.ValidateDevEnvironment(); err != nil {
			t.Errorf("%+v rejected: %v", cfg, err)
		}
	}
	invalid := map[string]OrgCoderConfig{
		"unknown user":    {WorkerUser: "root"},
		"probe too long":  {WorkerUser: CoderWorkerUserAOWorker, ReadyProbe: strings.Repeat("x", MaxCoderReadyProbeLength+1)},
		"probe with NUL":  {WorkerUser: CoderWorkerUserAOWorker, ReadyProbe: "true\x00"},
		"timeout too low": {WorkerUser: CoderWorkerUserAOWorker, ReadyProbe: "true", ReadyProbeTimeoutSeconds: 29},
		"timeout too big": {WorkerUser: CoderWorkerUserAOWorker, ReadyProbe: "true", ReadyProbeTimeoutSeconds: 3601},
	}
	for name, cfg := range invalid {
		if err := cfg.ValidateDevEnvironment(); err == nil {
			t.Errorf("%s: accepted %+v", name, cfg)
		}
	}
}

func TestValidateCoderTemplateParameterValues(t *testing.T) {
	t.Parallel()
	if err := ValidateCoderTemplateParameterValues(map[string]string{
		"region": "eu", "repos": `["a","b"]`, "gpu": "true", "empty": "",
	}); err != nil {
		t.Fatalf("valid parameters rejected: %v", err)
	}
	tooMany := map[string]string{}
	for i := 0; i <= MaxCoderTemplateParameters; i++ {
		tooMany[strings.Repeat("p", i+1)] = "v"
	}
	invalid := map[string]map[string]string{
		"too many":        tooMany,
		"empty name":      {"": "v"},
		"padded name":     {" region": "v"},
		"control in name": {"re\ngion": "v"},
		"value too large": {"region": strings.Repeat("x", MaxCoderTemplateParameterValueSize+1)},
	}
	for name, parameters := range invalid {
		if err := ValidateCoderTemplateParameterValues(parameters); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUnknownCoderTemplateParameters(t *testing.T) {
	t.Parallel()
	unknown := UnknownCoderTemplateParameters(
		map[string]string{"region": "eu", "zeta": "1", "alpha": "2"},
		[]string{"region", "size"},
	)
	if !reflect.DeepEqual(unknown, []string{"alpha", "zeta"}) {
		t.Fatalf("unknown = %v", unknown)
	}
	if got := UnknownCoderTemplateParameters(map[string]string{"region": "eu"}, []string{"region"}); got != nil {
		t.Fatalf("unknown = %v, want none", got)
	}
}

// Settings may set coder.parameters; the object replaces the stored one as a
// whole (so a value reset to its default disappears) and must hold strings.
func TestProjectSettingsCoderParametersReplaceAsAWhole(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"coder":{"templateId":"2a2e262c-b31c-4202-946d-a19ad45d1fd2","workspaceNamePrefix":"dev","parameters":{"region":"eu","gpu":"true"}}}`)
	merged, err := MergeProjectSettingsConfig(existing, json.RawMessage(`{"coder":{"parameters":{"region":"us"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := DecodeProjectCoderConfig(merged)
	if !ok {
		t.Fatalf("merged config lost coder: %s", merged)
	}
	if !reflect.DeepEqual(cfg.Parameters, map[string]string{"region": "us"}) {
		t.Fatalf("parameters = %v, want only region=us", cfg.Parameters)
	}
	if cfg.TemplateID == "" || cfg.WorkspaceNamePrefix != "dev" {
		t.Fatalf("other coder fields were lost: %+v", cfg)
	}
	for _, patch := range []string{
		`{"config":{"coder":{"parameters":{"region":1}}}}`,
		`{"config":{"coder":{"parameters":["region"]}}}`,
		`{"config":{"coder":{"parameters":{"":"x"}}}}`,
		`{"config":{"coder":{"templateId":"2a2e262c-b31c-4202-946d-a19ad45d1fd2"}}}`,
	} {
		if _, err := ParseProjectSettingsPatch(json.RawMessage(patch)); err == nil {
			t.Errorf("patch %s accepted", patch)
		}
	}
	if _, err := ParseProjectSettingsPatch(json.RawMessage(`{"config":{"coder":{"parameters":{}}}}`)); err != nil {
		t.Fatalf("clearing parameters rejected: %v", err)
	}
}
