package chattraffic

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderMarkdown renders a report. Every string it prints is a fixed label, an
// allowlisted identifier, a validated hex digest, or a number, and each passes
// through escapeMarkdown regardless.
func RenderMarkdown(r Report) string {
	var b strings.Builder
	b.WriteString("## Chat traffic benchmark\n\n")
	fmt.Fprintf(&b, "**Result: %s** (exit %d). Baseline `%s`, candidate `%s`%s. %s %s/%s.\n\n",
		escapeMarkdown(strings.ToUpper(r.Status)), r.ExitCode,
		escapeMarkdown(shortRef(r.Baseline.SourceRef)), escapeMarkdown(shortRef(r.Candidate.SourceRef)),
		dirtyLabel(r.Candidate.Dirty), escapeMarkdown(r.Candidate.GoVersion),
		escapeMarkdown(r.Candidate.GOOS), escapeMarkdown(r.Candidate.GOARCH))
	if hasSeverity(r.Reasons, SeverityUnverified) {
		b.WriteString("**Unverified:** the renderer source differs from what the client simulator models " +
			"(`--allow-client-change`). Request counts and refresh behavior may not match the real client; " +
			"this comparison is not a pass.\n\n")
	}
	b.WriteString("Bytes are counted at the simulated client's TCP sockets over loopback: HTTP/1.1 status lines, " +
		"headers, chunked framing, and bodies. TLS, TCP/IP, HTTP/2 and WebSocket tunnel framing are not included. " +
		"`remote-lan` bodies are gzip-encoded by the daemon's own LAN middleware; `local` bodies are never encoded. " +
		"Request and frame counts come from a deterministic scheduled replay, not wall-clock product timing.\n\n")

	b.WriteString("| Scenario | Requests | SSE frames | Download (B) | Body (B) | JSON (B) | Δ download | Download limit | Status |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, row := range r.Rows {
		download := metricNamed(row, "downloadBytes")
		fmt.Fprintf(&b, "| %s · %s · %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdown(row.Provider), escapeMarkdown(row.Phase), escapeMarkdown(row.Transport),
			candidateValue(row, "requests"), candidateValue(row, "sseFrames"), candidateValue(row, "downloadBytes"),
			candidateValue(row, "bodyBytes"), candidateValue(row, "jsonBytes"),
			deltaLabel(download), groupDigits(download.Limit), rowStatusLabel(row))
	}

	if len(r.Reasons) > 0 {
		b.WriteString("\n**Reasons**\n\n")
		for _, reason := range r.Reasons {
			line := "- `" + escapeMarkdown(reason.Code) + "` (" + escapeMarkdown(reason.Severity) + ")"
			if reason.Scenario != "" {
				line += " " + escapeMarkdown(reason.Scenario)
			}
			if reason.Metric != "" {
				line += " " + escapeMarkdown(reason.Metric)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n<details><summary>All metrics</summary>\n\n")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "**%s · %s · %s** (semantic state %s)\n\n", escapeMarkdown(row.Provider),
			escapeMarkdown(row.Phase), escapeMarkdown(row.Transport), matchLabel(row.SemanticMatch))
		b.WriteString("| Metric | Baseline | Candidate | Δ | Limit | Allowed | Status |\n|---|---:|---:|---:|---:|---:|---|\n")
		for _, metric := range row.Metrics {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", escapeMarkdown(metric.Name),
				groupDigits(metric.Baseline), groupDigits(metric.Candidate), deltaLabel(metric),
				groupDigits(metric.Limit), groupDigits(metric.Allowed), escapeMarkdown(metric.Status))
		}
		b.WriteString("\n")
	}
	b.WriteString("</details>\n\n")
	fmt.Fprintf(&b, "Fixtures `%s`, limits `%s`, harness `%s`, max increase %s.\n",
		escapeMarkdown(shortDigest(r.FixturesSha256)), escapeMarkdown(shortDigest(r.LimitsSha256)),
		escapeMarkdown(shortDigest(r.Candidate.HarnessSha256)), permilleLabel(r.MaxIncreasePermille))
	return b.String()
}

func metricNamed(row ReportRow, name string) MetricRow {
	for _, metric := range row.Metrics {
		if metric.Name == name {
			return metric
		}
	}
	return MetricRow{Name: name}
}

func candidateValue(row ReportRow, name string) string {
	return groupDigits(metricNamed(row, name).Candidate)
}

func deltaLabel(metric MetricRow) string {
	if !metric.RelativeAvailable {
		if metric.Delta == 0 {
			return "0"
		}
		return "n/a"
	}
	sign := "+"
	if metric.DeltaPermille < 0 {
		sign = "−"
	}
	return sign + permilleLabel(abs(metric.DeltaPermille))
}

func permilleLabel(permille int64) string {
	return strconv.FormatInt(permille/10, 10) + "." + strconv.FormatInt(permille%10, 10) + "%"
}

func rowStatusLabel(row ReportRow) string {
	if row.Status == StatusPass {
		return "pass"
	}
	return "**" + escapeMarkdown(row.Status) + "**"
}

func matchLabel(match bool) string {
	if match {
		return "matches"
	}
	return "**differs**"
}

func dirtyLabel(dirty bool) string {
	if dirty {
		return " (with uncommitted changes)"
	}
	return ""
}

func shortRef(ref string) string {
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}

func shortDigest(digest string) string {
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

func abs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

// groupDigits formats an integer with thousands separators.
func groupDigits(value int64) string {
	digits := strconv.FormatInt(abs(value), 10)
	var b strings.Builder
	if value < 0 {
		b.WriteString("-")
	}
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return b.String()
}

// escapeMarkdown neutralizes characters that could change table structure or
// inject markup.
func escapeMarkdown(s string) string {
	return strings.NewReplacer(
		"\\", "\\\\", "|", "\\|", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]",
		"<", "&lt;", ">", "&gt;", "&", "&amp;", "\r", " ", "\n", " ",
	).Replace(s)
}
