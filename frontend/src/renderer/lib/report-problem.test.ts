import { afterEach, describe, expect, it, vi } from "vitest";
import { collectReportProblemDiagnostics, formatReportTimestamp, reportProblemDestinationUrl, sanitizeReportText, type ReportProblemDiagnostics } from "./report-problem";

const diagnostics: ReportProblemDiagnostics = {
	appVersion: "1.2.3-test", buildMode: "dev", daemonState: "ready",
	generatedAt: "2026-07-02T00:00:00.000Z", platform: "darwin-arm64", routeSurface: "session_detail",
};

afterEach(() => { vi.restoreAllMocks(); window.location.hash = ""; });

describe("GitHub report fallback", () => {
	it("prefills a new issue with compact redacted diagnostics", () => {
		const url = new URL(reportProblemDestinationUrl({
			...diagnostics,
			daemonMessage: "Failed /Users/alice/private/repo at http://127.0.0.1:5173/?token=private with API_KEY=secret",
		}));
		expect(`${url.origin}${url.pathname}`).toBe("https://github.com/OrchestratorInc/agent-orchestrator/issues/new");
		expect(url.searchParams.get("title")).toBe("Report a problem");
		const body = url.searchParams.get("body")!;
		for (const expected of ["What happened?", "App version: 1.2.3-test", "Daemon: ready", "Route surface: session_detail", "[redacted-local-path]", "[redacted-local-url]", "[redacted-secret]"]) expect(body).toContain(expected);
		for (const privateText of ["alice", "127.0.0.1", "API_KEY=secret"]) expect(body).not.toContain(privateText);
	});

	it("shows the generated time as readable local time with a UTC reference", () => {
		expect(formatReportTimestamp("2026-10-21T20:50:24.025Z", "America/Los_Angeles")).toBe(
			"Oct 21, 2026, 1:50:24 PM PDT (2026-10-21 20:50:24 UTC)",
		);
		expect(formatReportTimestamp("2026-10-21T20:50:24.025Z", "Asia/Kolkata")).toBe(
			"Oct 22, 2026, 2:20:24 AM GMT+5:30 (2026-10-21 20:50:24 UTC)",
		);
		expect(formatReportTimestamp("unknown")).toBe("unknown");
		expect(new URL(reportProblemDestinationUrl(diagnostics)).searchParams.get("body")).toContain("(2026-07-02 00:00:00 UTC)");
	});

	it("bounds a verbose daemon message after redacting it", () => {
		const url = new URL(reportProblemDestinationUrl({ ...diagnostics, daemonMessage: "message ".repeat(1_000) }));
		expect(url.searchParams.get("body")!.length).toBeLessThan(1_500);
	});

	it.each([
		["C:\\Users\\alice\\repo", "C:\\Users\\alice"],
		["file:///Users/alice/private/index.html?api_key=abc", "/Users/alice"],
		['{"token": "json-token-secret", "api_key": "json-api-key-secret"}', "json-token-secret"],
		["Authorization: Bearer header-token-secret", "header-token-secret"],
		['password="first second"', "second"],
		['{"passwd":"first second"}', "second"],
		["https://user:pass@host/path", "user:pass"],
		["https://token:private@host/path", "private"],
		["https://user:pass@word@host/path", "word"],
		[`ghp_${"a".repeat(30)}`, `ghp_${"a".repeat(30)}`],
		[`github_pat_${"a".repeat(30)}`, `github_pat_${"a".repeat(30)}`],
	])("redacts %s", (input, privateText) => {
		expect(sanitizeReportText(input)).not.toContain(privateText);
	});

	it("derives route surface from the hash-history route", async () => {
		window.ao!.app.getVersion = vi.fn().mockResolvedValue("1.2.3-test");
		window.ao!.daemon.getStatus = vi.fn().mockResolvedValue({ state: "ready" });
		window.location.hash = "#/projects/demo/sessions/demo-1";
		expect((await collectReportProblemDiagnostics()).routeSurface).toBe("session_detail");
	});

	it("keeps the fallback available when diagnostic calls fail", async () => {
		window.ao!.app.getVersion = vi.fn().mockRejectedValue(new Error("offline"));
		window.ao!.daemon.getStatus = vi.fn().mockRejectedValue(new Error("stopped"));
		const url = new URL(reportProblemDestinationUrl(await collectReportProblemDiagnostics()));
		expect(url.searchParams.get("body")).toContain("App version: unknown");
		expect(url.searchParams.get("body")).toContain("Daemon: unknown");
	});
});

it.each(["client_secret", "refreshToken", "access_token", "id_token", "private_key", "pairing code", "pairing_password", "session-secret", "GH_TOKEN", "MY_API_KEY_VALUE", "apiToken"])("redacts %s in every diagnostic form", (key) => {
 for (const value of ["SENTINELVALUE", "abCDEF0123+/".repeat(4), "abcdef0123456789".repeat(3)]) {
  for (const input of [
   `{"${key}":"${value}"}`, `{"${key}":${value}}`, `${key}="${value}"`, `${key}=${value}`,
   `${key}: '${value}'`, `${key}: ${value}`, `--${key} "${value}"`, `--${key} ${value}`, `--${key}="${value}"`, `--${key}=${value}`,
   String.raw`error="{\"${key}\":\"${value}\"}"`,
  ]) {
   expect(sanitizeReportText(input), input).not.toContain(value);
   expect(sanitizeReportText(input), input).toContain("[redacted-secret]");
  }
 }
});
it.each(["pairing 123456", "pairing code 1234567", "code: 12345678"])("redacts %s", (input) => {
 expect(sanitizeReportText(input)).not.toMatch(/\d/);
});
