package mcpapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReadDocumentBindsResourceAndPolicy(t *testing.T) {
	raw := json.RawMessage(`{"contents":[{"uri":"ui://app","mimeType":"text/html;profile=mcp-app","text":"<h1>App</h1>","_meta":{"ui":{"csp":{"connectDomains":["https://api.example.com","http://127.0.0.1:3001","http://10.0.0.1","https://x.local","https://good.com; default-src *"],"resourceDomains":["https://*.cdn.example.com"]}}}}]}`)
	doc, err := ReadDocument(raw, "ui://app")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.CSP.ConnectDomains) != 1 || doc.CSP.ConnectDomains[0] != "https://api.example.com" {
		t.Fatalf("unsafe policy: %+v", doc.CSP)
	}
	policy := doc.Policy()
	for _, want := range []string{"sandbox allow-scripts allow-forms", "default-src 'none'", "connect-src https://api.example.com", "frame-src 'none'", "form-action 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy missing %s: %s", want, policy)
		}
	}
	if strings.Contains(policy, "allow-same-origin") {
		t.Fatal("app gained host origin")
	}
	if _, err := ReadDocument(raw, "ui://different"); err == nil {
		t.Fatal("accepted another app's resource")
	}
}

func TestReadDocumentRejectsUnsupportedResource(t *testing.T) {
	for _, raw := range []string{
		`{"contents":[{"uri":"ui://a","mimeType":"text/html","text":"x"}]}`,
		`{"contents":[{"uri":"ui://a","mimeType":"text/html;profile=mcp-app","text":""}]}`,
		`{"contents":[{"uri":"ui://a","mimeType":"text/html;profile=mcp-app","blob":"??"}]}`,
	} {
		if _, err := ReadDocument(json.RawMessage(raw), "ui://a"); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	doc, err := ReadDocument(json.RawMessage(`{"contents":[{"uri":"ui://a","mimeType":"text/html;profile=mcp-app","blob":"PGgxPk9LPC9oMT4="}]}`), "ui://a")
	if err != nil || doc.HTML != "<h1>OK</h1>" {
		t.Fatalf("blob: %+v %v", doc, err)
	}
}

func TestDocumentDelegatesOnlyDeclaredPermissions(t *testing.T) {
	doc, err := ReadDocument(json.RawMessage(`{"contents":[{"uri":"ui://a","mimeType":"text/html;profile=mcp-app","text":"app","_meta":{"ui":{"permissions":{"camera":{},"clipboardWrite":{},"microphone":true,"geolocation":null,"unknown":{}},"csp":{"connectDomains":["http://127.1","http://0177.0.0.1","http://0x7f.0.0.1","https://api.example.com"]}}}}]}`), "ui://a")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Permissions) != 2 || doc.PermissionsPolicy() != "clipboard-read=(), camera=*, microphone=(), geolocation=(), clipboard-write=*" {
		t.Fatalf("permissions: %+v %s", doc.Permissions, doc.PermissionsPolicy())
	}
	if len(doc.CSP.ConnectDomains) != 1 || doc.CSP.ConnectDomains[0] != "https://api.example.com" {
		t.Fatalf("noncanonical private address accepted: %+v", doc.CSP)
	}
}

func TestContextTextReplacesAndBoundsAppState(t *testing.T) {
	text, err := ContextText(json.RawMessage(`{"content":[{"type":"text","text":"selected: blue"}],"structuredContent":{"page":2}}`))
	if err != nil || text != "selected: blue\n{\"page\":2}" {
		t.Fatalf("context %q %v", text, err)
	}
	text, err = ContextText(json.RawMessage(`{}`))
	if err != nil || text != "" {
		t.Fatal("empty context must clear state")
	}
	for _, raw := range []string{`{"content":[{"type":"image","data":"abc"}]}`, `{"structuredContent":[]}`, `{"content":[{"type":"text","text":"` + strings.Repeat("x", MaxContextBytes+1) + `"}]}`} {
		if _, err := ContextText(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted unsupported/oversized context")
		}
	}
}
