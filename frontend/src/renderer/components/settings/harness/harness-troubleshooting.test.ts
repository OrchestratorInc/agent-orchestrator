import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import { collectTroubleshootingSnapshot, troubleshootingIssueDraft, troubleshootingPrompt, troubleshootingSnapshot, troubleshootingVerified } from "./harness-troubleshooting";

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
		snapshot.authentication.freshness = "stale";
		expect(troubleshootingVerified(snapshot)).toBe(false);
	});

	it("keeps observations distinct from evidence and labels a failed latest check", () => {
		const before = troubleshootingSnapshot(agentReadiness("codex"), "0.13.0", "linux");
		const draft = troubleshootingIssueDraft("installation", before, before, true);
		expect(draft).toContain("does not establish root cause");
		expect(draft).toContain("most recent verification attempt failed");
		expect(draft).toContain("[Describe what you did");
	});
});

describe("fresh verification", () => {
	it("waits for installation verification before probing authentication and taking the snapshot", async () => {
		vi.useFakeTimers();
		const job = { target: "codex", status: "verifying", startedAt: "2026-10-23T00:00:00Z" };
		const post = vi.spyOn(apiClient, "POST").mockResolvedValueOnce({ data: job } as never).mockResolvedValueOnce({ data: { supported: true } } as never);
		vi.spyOn(apiClient, "GET").mockResolvedValueOnce({ data: { jobs: [{ ...job, status: "succeeded" }] } } as never).mockResolvedValueOnce({ data: { agents: [agentReadiness("codex")] } } as never);
		const pending = collectTroubleshootingSnapshot("codex", new QueryClient(), "0.13.0", "win32");
		await Promise.resolve();
		expect(post).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1000);
		const result = await pending;
		expect(post).toHaveBeenNthCalledWith(1, "/api/v1/agents/{agent}/verify", { params: { path: { agent: "codex" } } });
		expect(post).toHaveBeenNthCalledWith(2, "/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
		expect(troubleshootingVerified(result)).toBe(true);
	});

	it("does not export a stale snapshot when the probe fails", async () => {
		vi.spyOn(apiClient, "POST").mockResolvedValueOnce({ data: { status: "succeeded" } } as never).mockRejectedValueOnce(new Error("probe unavailable"));
		const get = vi.spyOn(apiClient, "GET");
		await expect(collectTroubleshootingSnapshot("codex", new QueryClient(), "0.13.0", "win32")).rejects.toThrow("probe unavailable");
		expect(get).not.toHaveBeenCalled();
	});
});
