package triage

import (
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestRedactKnownSecretsAndPrivateLocations(t *testing.T) {
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("synthetic-redaction-fixture")}))
	keyLines := strings.SplitN(privateKey, "\n", 3)
	cases := []struct {
		name, input, private string
	}{
		{"openai", "failure with sk-proj-abcdefghijklmnopq123", "sk-proj-abcdefghijklmnopq123"},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz123456", "ghp_abcdefghijklmnopqrstuvwxyz123456"},
		{"bearer", "Authorization: Bearer only-this-test-secret", "only-this-test-secret"},
		{"env", "OPENAI_API_KEY=only-this-test-key", "only-this-test-key"},
		{"aws env", "AWS_SECRET_ACCESS_KEY=only-this-test-aws-key", "only-this-test-aws-key"},
		{"json", `{"password":"only-this-test-password"}`, "only-this-test-password"},
		{"home mac", "open /Users/alice/project/secrets.env failed", "alice/project"},
		{"home linux", "open /home/alice/project/secrets.env failed", "alice/project"},
		{"home windows", `open C:\Users\alice\project\secrets.env failed`, `alice\project`},
		{"local url", "request http://127.0.0.1:3456/private?key=test failed", "127.0.0.1"},
		{"lan url", "request https://192.168.1.10/private failed", "192.168.1.10"},
		{"ipv6 url", "request http://[::1]:3456/private failed", "::1"},
		{"local hostname", "request http://workstation:3456/private failed", "workstation"},
		{"mdns hostname", "request http://workstation.local:3456/private failed", "workstation.local"},
		{"url password", "https://alice:test-password@example.com/path", "test-password"},
		{"database url password", "postgres://alice:test-password@example.com/database", "test-password"},
		{"url token", "https://example.com/path?access_token=test-query-value", "test-query-value"},
		{"private key", privateKey, keyLines[1]},
		{"truncated private key", strings.Join(keyLines[:2], "\n"), keyLines[1]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if strings.Contains(got, tc.private) || got == tc.input {
				t.Fatalf("sensitive value remains in %q", got)
			}
		})
	}
	text := "cpuMeasured=false CPU 0.00 and https://github.com/OrchestratorInc/agent-orchestrator/issues/10"
	if got := Redact(text); got != text {
		t.Fatalf("ordinary facts changed: %q", got)
	}
}

func TestRedactEvidenceKeepsMeasurementsAndFiltersNestedValues(t *testing.T) {
	type credentials struct {
		APIKey string `json:"apiKey"`
		CPU    int    `json:"cpuCount"`
	}
	input := []Evidence{{ID: "machine", Source: "/Users/alice/private/file", Text: "Bearer text-secret", Values: map[string]any{
		"cpuMeasured": false, "cpuPercent": float64(0), "cpuCount": 8, "availableBytes": uint64(123), "load1": nil,
		"nested": []any{map[string]any{"apiKey": "key-secret", "source": "/home/alice/private/file"}, json.RawMessage(`{"cpuMeasured":false,"cpuPercent":0,"refreshToken":"refresh-secret"}`)},
		"typed":  map[string]string{"Authorization": "unlabelled-secret"}, "struct": credentials{"struct-secret", 4},
		"error": "failed at /Users/alice/file with token=error-secret",
	}}}
	data, err := sanitizeJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	var out []Evidence
	if err := decodeJSON(data, &out, false); err != nil {
		t.Fatal(err)
	}
	values := out[0].Values
	if values["cpuMeasured"] != false || values["cpuPercent"] != json.Number("0") || values["cpuCount"] != json.Number("8") || values["availableBytes"] != json.Number("123") || values["load1"] != nil {
		t.Fatalf("measurement values or types changed: %#v", values)
	}
	for _, secret := range []string{"alice", "text-secret", "key-secret", "refresh-secret", "unlabelled-secret", "struct-secret", "error-secret"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("serialized evidence contains %q: %s", secret, data)
		}
	}
	if input[0].Source != "/Users/alice/private/file" || input[0].Values["nested"].([]any)[0].(map[string]any)["apiKey"] != "key-secret" {
		t.Fatal("redaction mutated the caller's evidence")
	}
}

func TestRedactConfiguredHomeAndMalformedStructuredData(t *testing.T) {
	t.Setenv("HOME", "/opt/custom-home/test-user")
	t.Setenv("USERPROFILE", "/opt/custom-home/test-user")
	if got := Redact("failed /opt/custom-home/test-user/project/file"); strings.Contains(got, "test-user") {
		t.Fatalf("configured home remains: %q", got)
	}
	for _, value := range []any{json.RawMessage(`{"secret":"broken`), make(chan int)} {
		if _, err := sanitizeJSON([]Evidence{{Values: map[string]any{"snapshot": value}}}); err == nil {
			t.Fatal("unsafe structured value survived")
		}
	}
}
