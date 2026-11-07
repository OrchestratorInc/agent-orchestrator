import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import {
	collectTroubleshootingSnapshot, fetchTroubleshootingFindings, parseTroubleshootingFindings, TROUBLESHOOTING_FINDINGS_FILE, troubleshootingIssueDraft, troubleshootingIssueUrl,
	troubleshootingNextStep, troubleshootingPrompt, troubleshootingReportAdvice, troubleshootingSnapshot, troubleshootingVerified, type TroubleshootingFindings,
} from "./harness-troubleshooting";

const findingsFixture = (patch: Partial<TroubleshootingFindings> = {}): TroubleshootingFindings => ({
	cause: "setup", outcome: "fixed", nativeCliWorks: true, summary: "The daemon PATH lacked the npm global bin.",
	evidence: ["codex --version works in the login shell"], repairs: ["Added the npm bin to ~/.zprofile"], uncertainty: "", ...patch,
});
const verifiedSnapshot = (authentication: "authorized" | "unauthorized" = "authorized") => {
	const snapshot = troubleshootingSnapshot(agentReadiness("codex", "Codex", { authentication }), "0.13.0", "linux");
	snapshot.installationVerification = "succeeded";
	return snapshot;
};

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

describe("harness troubleshooting evidence", () => {
	it("exports classifications without identities, paths, raw errors or credentials", () => {
		const agent = agentReadiness("codex");
		agent.authentication.account = { email: "private@example.test", name: "Private User", source: "/Users/private/auth.json", sourceKind: "file" };
		agent.authentication.reason = "Bearer secret-token";
		agent.installation.reason = "PASSWORD=secret";
		agent.authentication.diagnostics = [{ method: "provider_validation", code: "credential_rejected", cached: false, checkedAt: "2026-10-23T00:00:00Z", durationMs: 50, httpStatus: 401, reason: "sk-secret", nextStep: "token=secret", source: "/home/private/.config/auth.json" }];
		const snapshot = troubleshootingSnapshot(agent, "0.13.0", "win32");
		const text = troubleshootingPrompt("login", snapshot);
		for (const secret of ["private@example", "Private User", "/Users/", "/home/", "secret-token", "PASSWORD", "sk-secret", "token=secret"]) expect(text).not.toContain(secret);
		expect(snapshot.authentication).toMatchObject({ credentialSourceKind: "file", checks: [{ code: "credential_rejected", httpStatus: 401 }] });
		expect(text).toContain("not independently verified");
		expect(text).toContain("Ask the user before changing login state");
	});

	it.each(["configured", "unknown", "unauthorized"] as const)("does not call %s verified", (authentication) => {
		const snapshot = troubleshootingSnapshot(agentReadiness("codex", "Codex", { authentication }), "0.13.0", "linux");
		snapshot.installationVerification = "succeeded";
		expect(troubleshootingVerified(snapshot)).toBe(false);
	});

	it("requires fresh observations and a successful install job", () => {
		const snapshot = troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "linux");
		expect(troubleshootingVerified(snapshot)).toBe(false);
		snapshot.installationVerification = "succeeded";
		expect(troubleshootingVerified(snapshot)).toBe(true);
		expect(troubleshootingNextStep(snapshot)).toBe("ready");
		expect(troubleshootingNextStep(snapshot, true)).toBe("incomplete");
		snapshot.authentication.freshness = "stale";
		expect(troubleshootingVerified(snapshot)).toBe(false);
		expect(troubleshootingNextStep(snapshot)).toBe("incomplete");
	});

	it("keeps observations distinct from evidence and labels a failed latest check", () => {
		const before = troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "linux");
		const draft = troubleshootingIssueDraft("installation", before, before, true);
		expect(draft).toContain("does not establish root cause");
		expect(draft).toContain("most recent verification attempt failed");
		expect(draft).toContain("[Describe what you did");
	});

	it("records install method and version from advisory evidence without its binary path", () => {
		const snapshot = troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "darwin", {
			agentId: "codex", status: "current", checkedAt: "2026-10-23T00:00:00Z", currentVersion: "codex-cli 0.50.1",
			source: "npm", maintenanceMethod: "npm", binaryPath: "/Users/private/.npm-global/bin/codex",
		});
		expect(snapshot.installation).toMatchObject({ harnessVersion: "0.50.1", source: "npm", maintenanceMethod: "npm", updateStatus: "current" });
		expect(JSON.stringify(snapshot)).not.toContain("/Users/private");
		const unsafe = troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "darwin", { agentId: "codex", status: "unknown", checkedAt: "2026-10-23T00:00:00Z", source: "/opt/homebrew/bin/codex" });
		expect(unsafe.installation).toMatchObject({ harnessVersion: "unknown", source: "unknown" });
		expect(troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "darwin").installation.harnessVersion).toBeNull();
	});

	it("asks the helper to start with ao doctor and record structured findings", () => {
		const prompt = troubleshootingPrompt("installation", verifiedSnapshot());
		expect(prompt).toContain("ao doctor --json");
		expect(prompt).toContain(TROUBLESHOOTING_FINDINGS_FILE);
		expect(prompt).toContain("suspected_ao_bug");
		expect(prompt).toContain("Do not publish or file issues");
	});
});

describe("helper findings", () => {
	it("bounds, classifies and redacts helper-written text", () => {
		const findings = parseTroubleshootingFindings(JSON.stringify({
			cause: "suspected_ao_bug", outcome: "invented", nativeCliWorks: "yes",
			summary: "Login for private@example.test works; token sk-abc123 at /Users/private/.codex/auth.json",
			evidence: Array.from({ length: 20 }, (_, index) => `observation ${index}`), repairs: ["Bearer abc.def"], uncertainty: 42,
		}));
		expect(findings).toMatchObject({ cause: "suspected_ao_bug", outcome: "needs_user", nativeCliWorks: null, uncertainty: "" });
		expect(findings?.evidence).toHaveLength(10);
		const text = JSON.stringify(findings);
		for (const secret of ["private@example", "sk-abc123", "/Users/private", "abc.def"]) expect(text).not.toContain(secret);
	});

	it.each(["not json", "[]", "{}", JSON.stringify({ summary: "   " })])("rejects malformed findings %s", (text) => {
		expect(parseTroubleshootingFindings(text)).toBeUndefined();
	});

	it("reads the findings artifact and distinguishes missing from unreadable files", async () => {
		const fetch = vi.spyOn(globalThis, "fetch")
			.mockResolvedValueOnce(new Response("", { status: 404 }))
			.mockResolvedValueOnce(new Response("not json"))
			.mockResolvedValueOnce(new Response(JSON.stringify(findingsFixture())));
		await expect(fetchTroubleshootingFindings("helper 1")).resolves.toEqual({ status: "missing" });
		await expect(fetchTroubleshootingFindings("helper 1")).resolves.toEqual({ status: "invalid" });
		await expect(fetchTroubleshootingFindings("helper 1")).resolves.toEqual({ status: "ready", findings: findingsFixture() });
		expect(String(fetch.mock.calls[0][0])).toContain(`/api/v1/sessions/helper%201/artifact-files/${TROUBLESHOOTING_FINDINGS_FILE}`);
	});

	it("recommends a report only when the evidence points at AO", () => {
		const failing = verifiedSnapshot("unauthorized");
		expect(troubleshootingReportAdvice(verifiedSnapshot())).toBe("not_needed");
		expect(troubleshootingReportAdvice(verifiedSnapshot(), findingsFixture({ cause: "suspected_ao_bug" }))).toBe("recommended");
		expect(troubleshootingReportAdvice(failing, findingsFixture({ outcome: "not_fixed", nativeCliWorks: true }))).toBe("recommended");
		expect(troubleshootingReportAdvice(failing, findingsFixture({ outcome: "not_fixed", nativeCliWorks: null }), true)).toBe("recommended");
		expect(troubleshootingReportAdvice(failing, findingsFixture({ outcome: "needs_user", nativeCliWorks: false }), true)).toBe("optional");
		expect(troubleshootingReportAdvice(failing)).toBe("optional");
		expect(troubleshootingReportAdvice(failing, findingsFixture({ cause: "provider", nativeCliWorks: false }))).toBe("optional");
		expect(troubleshootingReportAdvice(verifiedSnapshot(), findingsFixture({ cause: "provider", nativeCliWorks: false }))).toBe("not_needed");
	});

	it("puts helper findings in the draft as a hypothesis and titles a suspected AO bug", () => {
		const before = verifiedSnapshot("unauthorized");
		const draft = troubleshootingIssueDraft("login", before, undefined, false, findingsFixture({ cause: "suspected_ao_bug", nativeCliWorks: true }), "Claude Code");
		expect(draft.split("\n\n")[0]).toBe("AO does not recognize a valid codex login");
		expect(draft).toContain("Helper findings (written by Claude Code; a hypothesis, not a confirmed root cause)");
		expect(draft).toContain("- Added the npm bin to ~/.zprofile");
		expect(draft).not.toContain("[Describe changes you approved");
		expect(troubleshootingIssueDraft("login", before).split("\n\n")[0]).toBe("Harness setup problem: codex");
	});

	it("prefills a GitHub issue and falls back to pasting a draft too long for a URL", () => {
		const short = troubleshootingIssueUrl("Harness setup problem: codex\n\nBody text");
		expect(short.pasteRequired).toBe(false);
		expect(new URL(short.url).searchParams.get("title")).toBe("Harness setup problem: codex");
		expect(new URL(short.url).searchParams.get("body")).toBe("Body text");
		const long = troubleshootingIssueUrl(`Title\n\n${"x".repeat(9000)}`);
		expect(long.pasteRequired).toBe(true);
		expect(long.url.length).toBeLessThan(8000);
		expect(new URL(long.url).searchParams.get("title")).toBe("Title");
	});
});

describe("fresh verification", () => {
	it("waits for installation verification before probing authentication and taking the snapshot", async () => {
		vi.useFakeTimers();
		const onStage = vi.fn();
		const job = { target: "codex", status: "verifying", startedAt: "2026-10-23T00:00:00Z" };
		const post = vi.spyOn(apiClient, "POST").mockResolvedValueOnce({ data: job } as never).mockResolvedValueOnce({ data: { supported: true } } as never);
		vi.spyOn(apiClient, "GET").mockResolvedValueOnce({ data: { jobs: [{ ...job, status: "succeeded" }] } } as never).mockResolvedValueOnce({ data: { agents: [agentReadiness("codex")] } } as never)
			.mockResolvedValueOnce({ error: { message: "advisory unavailable" } } as never);
		const pending = collectTroubleshootingSnapshot("codex", new QueryClient(), "0.13.0", "win32", onStage);
		await Promise.resolve();
		expect(post).toHaveBeenCalledTimes(1);
		expect(onStage.mock.calls).toEqual([["installation"]]);
		await vi.advanceTimersByTimeAsync(1000);
		const result = await pending;
		expect(post).toHaveBeenNthCalledWith(1, "/api/v1/agents/{agent}/verify", { params: { path: { agent: "codex" } } });
		expect(post).toHaveBeenNthCalledWith(2, "/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
		expect(troubleshootingVerified(result)).toBe(true);
		expect(onStage.mock.calls).toEqual([["installation"], ["authentication"], ["results"]]);
	});

	it("does not export a stale snapshot when the probe fails", async () => {
		vi.spyOn(apiClient, "POST").mockResolvedValueOnce({ data: { status: "succeeded" } } as never).mockRejectedValueOnce(new Error("probe unavailable"));
		const get = vi.spyOn(apiClient, "GET");
		await expect(collectTroubleshootingSnapshot("codex", new QueryClient(), "0.13.0", "win32")).rejects.toThrow("probe unavailable");
		expect(get).not.toHaveBeenCalled();
	});
});
