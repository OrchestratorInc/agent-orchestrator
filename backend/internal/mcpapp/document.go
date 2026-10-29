// Package mcpapp validates MCP Apps resources independently of their provider.
package mcpapp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ProtocolVersion and byte limits bound the supported MCP Apps payloads.
const (
	MaxDocumentBytes = 5 << 20
	MaxDataBytes     = 8 << 20
	MaxContextBytes  = 16 << 10
	ProtocolVersion  = "2026-01-26"
)

// CSP contains the external origins declared by an app resource.
type CSP struct {
	ConnectDomains  []string `json:"connectDomains,omitempty"`
	ResourceDomains []string `json:"resourceDomains,omitempty"`
	FrameDomains    []string `json:"frameDomains,omitempty"`
	BaseURIDomains  []string `json:"baseUriDomains,omitempty"`
}

// Document is the saved HTML together with its validated resource policy.
type Document struct {
	Permissions   map[string]json.RawMessage `json:"permissions,omitempty"`
	HTML          string                     `json:"html"`
	CSP           CSP                        `json:"csp"`
	PrefersBorder bool                       `json:"prefersBorder,omitempty"`
}

// ReadDocument binds the resource and its policy together. A cached document
// never acquires a more permissive policy from a later server response.
func ReadDocument(raw json.RawMessage, uri string) (Document, error) {
	if len(raw) > MaxDocumentBytes*2 {
		return Document{}, errors.New("app resource exceeds the size limit")
	}
	var response struct {
		Contents []struct {
			URI      string `json:"uri"`
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
			Blob     string `json:"blob"`
			Meta     struct {
				UI struct {
					Permissions   map[string]json.RawMessage `json:"permissions"`
					CSP           CSP                        `json:"csp"`
					PrefersBorder bool                       `json:"prefersBorder"`
				} `json:"ui"`
			} `json:"_meta"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return Document{}, err
	}
	for _, content := range response.Contents {
		media, params, err := mime.ParseMediaType(content.MIMEType)
		if content.URI != uri || err != nil || media != "text/html" || params["profile"] != "mcp-app" {
			continue
		}
		html := content.Text
		if html == "" && content.Blob != "" {
			decoded, err := base64.StdEncoding.DecodeString(content.Blob)
			if err != nil {
				return Document{}, errors.New("app resource has invalid base64")
			}
			html = string(decoded)
		}
		if len(html) > MaxDocumentBytes || !utf8.ValidString(html) || strings.TrimSpace(html) == "" {
			return Document{}, errors.New("app resource must be nonempty UTF-8 HTML under 5 MiB")
		}
		csp := content.Meta.UI.CSP
		csp.ConnectDomains = publicDomains(csp.ConnectDomains)
		csp.ResourceDomains = publicDomains(csp.ResourceDomains)
		csp.FrameDomains = publicDomains(csp.FrameDomains)
		csp.BaseURIDomains = publicDomains(csp.BaseURIDomains)
		permissions := make(map[string]json.RawMessage)
		for _, key := range []string{"camera", "microphone", "geolocation", "clipboardWrite"} {
			var value map[string]json.RawMessage
			if json.Unmarshal(content.Meta.UI.Permissions[key], &value) == nil && value != nil {
				permissions[key] = json.RawMessage(`{}`)
			}
		}
		return Document{HTML: html, CSP: csp, Permissions: permissions, PrefersBorder: content.Meta.UI.PrefersBorder}, nil
	}
	return Document{}, errors.New("the server returned no matching MCP app document")
}

// PermissionsPolicy delegates declared features, subject to the browser/OS
// permission prompt. No undeclared feature, or clipboard read, is delegated.
func (d Document) PermissionsPolicy() string {
	policies := make([]string, 1, 5)
	policies[0] = "clipboard-read=()"
	for _, feature := range []struct{ key, name string }{{"camera", "camera"}, {"microphone", "microphone"}, {"geolocation", "geolocation"}, {"clipboardWrite", "clipboard-write"}} {
		grant := "()"
		if _, ok := d.Permissions[feature.key]; ok {
			grant = "*"
		}
		policies = append(policies, feature.name+"="+grant)
	}
	return strings.Join(policies, ", ")
}

var originPattern = regexp.MustCompile(`^(https?|wss?)://(\*\.)?[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)*(?::[0-9]{1,5})?$`)

func publicDomains(domains []string) []string {
	var out []string
	for _, origin := range domains {
		if len(out) >= 32 {
			break
		}
		if len(origin) > 256 || !originPattern.MatchString(origin) {
			continue
		}
		u, err := url.Parse(strings.Replace(origin, "*.", "", 1))
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		// Browsers canonicalize abbreviated/octal/hex IPv4 spellings that Go's
		// strict IP parser intentionally rejects (127.1, 0177.0.0.1, etc.).
		if _, err := netip.ParseAddr(host); err != nil {
			last := host[strings.LastIndex(host, ".")+1:]
			if numericHostLabel.MatchString(last) {
				continue
			}
		}
		// Explicit private origins cannot give an app access to AO or LAN services.
		if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
			continue
		}
		if ip, err := netip.ParseAddr(host); err == nil && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			continue
		}
		out = append(out, origin)
	}
	return out
}

var numericHostLabel = regexp.MustCompile(`^(?:\d+|0x[0-9a-f]+)$`)

// Policy is an HTTP header, so an app cannot remove it with document scripts.
// No same-origin access, workers, or automatic forms.
func (d Document) Policy() string {
	list := func(items []string) string {
		if len(items) == 0 {
			return "'none'"
		}
		return strings.Join(items, " ")
	}
	r := strings.Join(d.CSP.ResourceDomains, " ")
	return strings.Join([]string{
		"sandbox allow-scripts allow-forms", "default-src 'none'", "script-src 'unsafe-inline' " + r,
		"style-src 'unsafe-inline' " + r, "img-src data: blob: " + r, "font-src data: " + r,
		"media-src data: blob: " + r, "connect-src " + list(d.CSP.ConnectDomains),
		"frame-src " + list(d.CSP.FrameDomains), "base-uri " + list(d.CSP.BaseURIDomains),
		"object-src 'none'", "worker-src 'none'", "form-action 'none'",
	}, "; ")
}

// ContextText accepts the text/structured capabilities AO advertises. It refuses
// unsupported content rather than silently losing an app's context.
func ContextText(params json.RawMessage) (string, error) {
	if len(params) > MaxContextBytes*2 {
		return "", errors.New("app context exceeds 16 KiB")
	}
	var p struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", err
	}
	var parts []string
	for _, b := range p.Content {
		if b.Type != "text" {
			return "", errors.New("app context supports text only")
		}
		parts = append(parts, b.Text)
	}
	if len(p.StructuredContent) > 0 && string(p.StructuredContent) != "null" {
		var object map[string]json.RawMessage
		if json.Unmarshal(p.StructuredContent, &object) != nil || object == nil {
			return "", errors.New("structured content must be an object")
		}
		parts = append(parts, string(p.StructuredContent))
	}
	text := strings.Join(parts, "\n")
	if len(text) > MaxContextBytes {
		return "", errors.New("app context exceeds 16 KiB")
	}
	return text, nil
}
