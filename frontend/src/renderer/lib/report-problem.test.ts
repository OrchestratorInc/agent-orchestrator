import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { aoBridge } from "./bridge";
import { newChatDraftScope, readChatSessionDraft, writeChatComposerText } from "./chat-drafts";
import { collectReportProblemDiagnostics, dropReportPrompt, sessionNameFromBrief, formatReportTimestamp, REPORT_PROBLEM_PROMPT, reportProblemDestinationUrl, sanitizeReportText, startProblemReport, type ReportProblemDiagnostics } from "./report-problem";

const diagnostics: ReportProblemDiagnostics = {
	appVersion: "1.2.3-test", buildMode: "dev", daemonState: "ready",
	generatedAt: "2026-07-02T00:00:00.000Z", platform: "darwin-arm64", routeSurface: "session_detail",
};

afterEach(() => { vi.restoreAllMocks(); window.location.hash = ""; });

const reportBody = (input: ReportProblemDiagnostics) => new URL(reportProblemDestinationUrl(input)).searchParams.get("body")!;

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

	it("includes Orchestrator Cloud's address, release and its last three failures, with no message, path or body", async () => {
		const before = window.ao!.cloudCp.diagnostics;
		const failure = (minute: number, status: number, code?: string) => ({
			at: `2026-07-02T00:0${minute}:00.000Z`, origin: status === 0 ? "network" as const : "cloud" as const, status,
			...(code ? { code } : {}), requestId: `0f8fad5b-d9cb-469f-a165-7086772895${minute}0`,
		});
		window.ao!.cloudCp.diagnostics = vi.fn().mockResolvedValue({
			baseUrl: "https://api.aoagents.dev",
			release: "cp-2026.11.01",
			recentErrors: [failure(1, 500), failure(2, 503, "WORKER_UNAVAILABLE"), failure(3, 0, "CLOUD_UNREACHABLE"), failure(4, 429, "WEEKLY_LIMIT_REACHED")],
		});
		try {
			const collected = await collectReportProblemDiagnostics(new Date("2026-07-02T00:00:00.000Z"));
			expect(collected.cloud?.recentErrors).toHaveLength(3);
			const body = reportBody(collected);
			expect(body).toContain("- Orchestrator Cloud: https://api.aoagents.dev");
			expect(body).toContain("- Orchestrator Cloud release: cp-2026.11.01");
			expect(body).not.toContain("0f8fad5b-d9cb-469f-a165-708677289510");
			expect(body).toContain("- Orchestrator Cloud error: 2026-07-02T00:02:00.000Z origin=cloud status=503 code=WORKER_UNAVAILABLE requestId=0f8fad5b-d9cb-469f-a165-708677289520");
			expect(body).toContain("- Orchestrator Cloud error: 2026-07-02T00:03:00.000Z origin=network (not reached) status=0 code=CLOUD_UNREACHABLE requestId=0f8fad5b-d9cb-469f-a165-708677289530");
			expect(body).toContain("status=429 code=WEEKLY_LIMIT_REACHED");
		} finally {
			window.ao!.cloudCp.diagnostics = before;
		}
	});

	it("drops anything in Orchestrator Cloud diagnostics that is not a bounded field", () => {
		const body = reportBody({
			...diagnostics,
			cloud: {
				baseUrl: "https://user:secret@cp.example.com/mount/?token=abc#x",
				release: "release with spaces",
				recentErrors: [{ at: "nope", origin: "cloud", status: 9999, code: "not a code", requestId: "laptop.local/abc" }],
			},
		});
		expect(body).toContain("- Orchestrator Cloud: https://cp.example.com/mount");
		expect(body).toContain("- Orchestrator Cloud release: unknown");
		expect(body).toContain("- Orchestrator Cloud error: unknown time origin=cloud status=unknown code=none requestId=none");
		expect(body).not.toMatch(/secret|token=abc|laptop\.local|release with spaces/);
		// No Orchestrator Cloud section when it was never configured or used.
		expect(reportBody(diagnostics)).not.toContain("Orchestrator Cloud");
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

describe("startProblemReport", () => {
	const draft = () => readChatSessionDraft(newChatDraftScope()).composer.text;
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ newTaskRequest: null, newChatDraftRevision: 0 });
	});

	it("keeps text already typed under the prompt", async () => {
		writeChatComposerText(newChatDraftScope(), "keep this draft");
		await expect(startProblemReport("ready")).resolves.toBeUndefined();
		expect(draft()).toBe(`${REPORT_PROBLEM_PROMPT}\n\nkeep this draft`);
	});

	it("does not stack the prompt when run twice but reloads the composer", async () => {
		await startProblemReport("ready");
		await startProblemReport("ready");
		expect(draft()).toBe(REPORT_PROBLEM_PROMPT);
		expect(useUiStore.getState().newChatDraftRevision).toBe(2);
	});

	it("asks for a new chat that only a report can use", async () => {
		await startProblemReport("ready");
		expect(useUiStore.getState().newTaskRequest).toMatchObject({ projectId: STANDALONE_WORKSPACE_ID, report: true });
	});

	it("takes the prompt out of a draft for a normal new chat and keeps what was typed", async () => {
		writeChatComposerText(newChatDraftScope(), "keep this draft");
		await startProblemReport("ready");
		dropReportPrompt(newChatDraftScope());
		expect(draft()).toBe("keep this draft");
		expect(useUiStore.getState().newChatDraftRevision).toBe(2);
	});

	it("leaves a draft without the prompt alone", () => {
		writeChatComposerText(newChatDraftScope(), "just my words");
		dropReportPrompt(newChatDraftScope());
		expect(draft()).toBe("just my words");
		expect(useUiStore.getState().newChatDraftRevision).toBe(0);
	});

	it("names a report session after the action, with or without typed text", () => {
		expect(sessionNameFromBrief(REPORT_PROBLEM_PROMPT)).toBe("Report a problem");
		expect(sessionNameFromBrief(`${REPORT_PROBLEM_PROMPT}\n\nThe sidebar froze`)).toBe("Report a problem");
		expect(sessionNameFromBrief(`  ${"a".repeat(120)}`)).toBe("a".repeat(100));
	});

	it("leaves the draft alone when the daemon is down", async () => {
		vi.spyOn(aoBridge.triage, "launch").mockResolvedValue({ ok: false, error: "report.desktopRequired", command: "ao triage" });
		const failure = await startProblemReport("stopped");
		expect(failure).toContain("ao triage");
		expect(draft()).toBe("");
		expect(useUiStore.getState().newTaskRequest).toBeNull();
	});

});
