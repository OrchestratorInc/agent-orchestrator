package agentlogin

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var nativeURL = regexp.MustCompile(`https://[^\s<>"'\x00-\x20\x1b]+[\s]`)
var deviceCode = regexp.MustCompile(`(?m)^[ \t]*([A-Z0-9]{4}-[A-Z0-9]{4,6})[ \t]*\r?\n`)
var setupToken = regexp.MustCompile(`(?:^|[\s])((?:sk-ant-oat01-)[A-Za-z0-9_-]{32,512})[\s]`)

type outputParser struct {
	harness string
	output  []byte
}

func (p *outputParser) add(data []byte) error {
	if len(p.output)+len(data) > maxOutputBytes {
		return errors.New("native login output exceeded its limit")
	}
	p.output = append(p.output, data...)
	return nil
}

func (p *outputParser) text() string {
	// Reparse the bounded buffer so split ANSI sequences cannot turn hidden
	// OSC links or incomplete controls into phone-visible provider instructions.
	var text strings.Builder
	for i := 0; i < len(p.output); i++ {
		if p.output[i] != 0x1b {
			text.WriteByte(p.output[i])
			continue
		}
		i++
		if i >= len(p.output) {
			break
		}
		switch p.output[i] {
		case '[':
			for i++; i < len(p.output); i++ {
				if p.output[i] >= 0x40 && p.output[i] <= 0x7e {
					break
				}
			}
		case ']':
			for i++; i < len(p.output); i++ {
				if p.output[i] == 0x07 {
					break
				}
				if p.output[i] == 0x1b && i+1 < len(p.output) && p.output[i+1] == '\\' {
					i++
					break
				}
			}
		}
	}
	return text.String()
}

func (p *outputParser) waiting() (progress, bool) {
	text := p.text()
	for _, match := range nativeURL.FindAllString(text, -1) {
		raw := strings.TrimSpace(match)
		if !safeAuthorizationURL(p.harness, raw) {
			continue
		}
		event := progress{Status: "waiting", AuthorizationURL: raw, RequiresCode: p.harness == "claude-code"}
		if p.harness == "codex" {
			// Only the standalone native device-code line is public; arbitrary
			// numbers, token text and diagnostics never become user messages.
			code := deviceCode.FindStringSubmatch(text)
			if len(code) != 2 {
				return progress{}, false
			}
			event.UserCode = code[1]
		}
		return event, true
	}
	return progress{}, false
}

func (p *outputParser) token() string {
	if p.harness != "claude-code" {
		return ""
	}
	text := p.text()
	index := strings.Index(text, "Your long-lived authentication token")
	if index < 0 {
		return ""
	}
	match := setupToken.FindStringSubmatch(text[index:])
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func safeAuthorizationURL(harness, raw string) bool {
	if len(raw) > 8192 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || u.RawPath != "" {
		return false
	}
	if harness == "codex" {
		return u.Hostname() == "auth.openai.com" && u.Path == "/codex/device" && u.RawQuery == ""
	}
	if harness != "claude-code" || !((u.Hostname() == "claude.com" && u.Path == "/cai/oauth/authorize") || (u.Hostname() == "claude.ai" && u.Path == "/oauth/authorize") || (u.Hostname() == "console.anthropic.com" && u.Path == "/oauth/authorize")) {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		if len(values) != 1 || len(values[0]) > 2048 || strings.ContainsAny(values[0], "\r\n\x00\x1b") {
			return false
		}
		switch key {
		case "client_id", "code", "code_challenge", "code_challenge_method", "redirect_uri", "response_type", "scope", "state":
		default:
			return false
		}
	}
	redirect := query.Get("redirect_uri")
	return query.Get("code") == "true" && query.Get("response_type") == "code" && query.Get("client_id") != "" && query.Get("code_challenge") != "" && query.Get("code_challenge_method") == "S256" && query.Get("state") != "" && (redirect == "https://platform.claude.com/oauth/code/callback" || redirect == "https://console.anthropic.com/oauth/code/callback")
}
