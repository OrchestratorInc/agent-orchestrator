import { describe, expect, it, vi } from "vitest";
import { CloudApiError, type CloudClient } from "@aoagents/cloud-client";
import { createCloudWorkspaceSource } from "./workspace";

const summary = (path: string, status = "modified") => ({ path, status, additions: 2, deletions: 1, binary: false, size: 10, editable: true, fileFingerprint: `fp:${path}` });
const review = () => ({
	workspaceVersion: "v2", compareBaseRef: "main", compareMode: "base", ahead: 1, behind: 0,
	files: [summary("same.ts", "unmodified"), summary("src/a.ts"), summary("new.ts", "untracked")],
	sections: { committed: [], staged: [summary("src/a.ts")], unstaged: [], untracked: [summary("new.ts", "untracked")] },
	commits: [], summary: { files: 2, additions: 4, deletions: 1 }, truncated: false,
});
const detail = (status = "modified") => ({ ...summary("new.ts", status), content: "hello\n", diff: "+hello", deleted: false, contentTruncated: false, diffTruncated: false, workspaceVersion: "v2" });
function setup(overrides: Record<string, unknown> = {}, isCurrent = () => true) {
	const client = {
		getWorkspaceReview: vi.fn(async () => review()),
		getSession: vi.fn(async () => ({ session: { desiredState: "running", runtimeConnected: true, isTerminated: false } })),
		getWorkspaceReviewFile: vi.fn(async () => detail()),
		getWorkspaceReviewTree: vi.fn(async () => ({ path: "src", entries: [{ name: "new.ts", path: "src/new.ts", type: "file", status: "untracked" }], truncated: false })),
		searchWorkspaceReview: vi.fn(async () => ({ query: "new", results: [summary("new.ts", "untracked")], truncated: true, nextCursor: "next" })),
		sendMessage: vi.fn(async (_org: string, _id: string, _message: string, _options: { idempotencyKey: string }) => ({})),
		...overrides,
	};
	return { client, source: createCloudWorkspaceSource({ client: client as unknown as CloudClient, orgId: "org", scopeKey: "account-scope", isCurrent }) };
}
describe("Cloud Files adapter", () => {
	it("maps real review revisions and sections without counting unchanged inventory as changes", async () => {
		const { source, client } = setup();
		const manifest = await source.getManifest("s/1");
		expect(manifest).toMatchObject({ sessionId: "s/1", workspaceVersion: "v2", compareBaseRef: "main", ahead: 1, behind: 0, degraded: false, summary: { files: 2, additions: 4, deletions: 1 } });
		expect(manifest.files.map(({ path, status, fileFingerprint }) => [path, status, fileFingerprint])).toEqual([["src/a.ts", "modified", "fp:src/a.ts"], ["new.ts", "added", "fp:new.ts"]]);
		expect(manifest.sections.untracked[0].status).toBe("added");
		expect(client.getWorkspaceReview).toHaveBeenCalledWith("org", "s/1", undefined);
	});
	it("reads content plus diff and requests the synthetic untracked diff when necessary", async () => {
		const file = vi.fn().mockResolvedValueOnce(detail("untracked")).mockResolvedValueOnce({ ...detail("untracked"), diff: "@@ -0,0 +1 @@\n+hello" });
		const { source } = setup({ getWorkspaceReviewFile: file });
		expect(await source.getFile("s1", "new.ts")).toMatchObject({ status: "added", diff: "@@ -0,0 +1 @@\n+hello", fileFingerprint: "fp:new.ts", workspaceVersion: "v2" });
		expect(file.mock.calls).toEqual([["org", "s1", { path: "new.ts", scope: "combined" }, undefined], ["org", "s1", { path: "new.ts", scope: "untracked" }, undefined]]);
	});
	it("preserves explicit diff sections and rename context", async () => {
		const { source, client } = setup({ getWorkspaceReviewFile: vi.fn(async () => ({ ...detail("renamed"), previousPath: "old.ts" })) });
		expect(await source.getFile("s1", "new.ts", "staged")).toMatchObject({ status: "renamed", previousPath: "old.ts" });
		expect(client.getWorkspaceReviewFile).toHaveBeenCalledWith("org", "s1", { path: "new.ts", scope: "staged" }, undefined);
	});
	it("keeps Cloud images on desktop's binary fallback instead of using local image credentials", async () => {
		const { source } = setup({ getWorkspaceReviewFile: vi.fn(async () => ({ ...detail(), binary: true, imageMediaType: "image/png", content: "" })) });
		expect(source.imageSource).toBeUndefined();
		expect(await source.getFile("s1", "image.png")).toMatchObject({ binary: true, imageMediaType: undefined });
	});
	it("routes tree and path search through Cloud with cancellation and normalized statuses", async () => {
		const { source, client } = setup(); const options = { signal: new AbortController().signal };
		expect(await source.getTree("s1", "src", options)).toMatchObject({ path: "src", entries: [{ status: "added" }] });
		expect(await source.search("s1", "new", options)).toMatchObject({ results: [{ path: "new.ts", status: "added" }], truncated: true });
		expect(client.getWorkspaceReviewTree).toHaveBeenCalledWith("org", "s1", { path: "src" }, options);
		expect(client.searchWorkspaceReview).toHaveBeenCalledWith("org", "s1", { query: "new", limit: 50 }, options);
	});
	it("isolates sibling repository reads and feedback while preserving main repository paths", async () => {
		const repositories = [{ name: "main", main: true, present: true }, { name: "shared-lib", present: true }];
		const { source, client } = setup({ getWorkspaceReview: vi.fn(async (_org, _id, options) => ({ ...review(), ...(options?.repository ? {} : { repositories }) })) });
		const sibling = source.forRepository!("shared-lib");
		expect(sibling).toBe(source.forRepository!("shared-lib"));
		expect(sibling.scopeKey).not.toBe(source.scopeKey);
		expect((await sibling.getManifest("s1")).repositories).toEqual(repositories);
		await sibling.getFile("s1", "new.ts"); await sibling.getTree("s1", "src"); await sibling.search("s1", "new");
		expect(client.getWorkspaceReview).toHaveBeenCalledWith("org", "s1", { repository: "shared-lib" });
		expect(client.getWorkspaceReviewFile).toHaveBeenCalledWith("org", "s1", { path: "new.ts", scope: "combined", repository: "shared-lib" }, undefined);
		expect(client.getWorkspaceReviewTree).toHaveBeenCalledWith("org", "s1", { path: "src", repository: "shared-lib" }, undefined);
		expect(client.searchWorkspaceReview).toHaveBeenCalledWith("org", "s1", { query: "new", limit: 50, repository: "shared-lib" }, undefined);
		await sibling.sendFeedback("s1", [{ target: { path: "a.ts", previousPath: "b.ts", side: "file" }, feedback: "fix this" }]);
		expect(client.sendMessage.mock.calls[0][2]).toContain("- Path: ../shared-lib/a.ts");
		expect(client.sendMessage.mock.calls[0][2]).toContain("- Previous path: ../shared-lib/b.ts");
		expect(sibling.forRepository!()).toBe(source);
	});
	it.each(["..", "../secret", "/etc", "a/b", "a\\b"])("rejects invalid sibling repository %s before requesting files", (name) => {
		const { source } = setup();
		expect(() => source.forRepository!(name)).toThrow("Invalid Cloud repository name.");
	});
	it.each(["getManifest", "getFile", "getTree", "search"] as const)("never starts %s after its account retires", async (method) => {
		const { source, client } = setup({}, () => false);
		await expect((source[method] as (id: string, path: string) => Promise<unknown>)("s1", "a.ts")).rejects.toThrow(/account changed/i);
		for (const call of Object.values(client)) expect(call).not.toHaveBeenCalled();
	});
	it("rejects late file contents after an account switch", async () => {
		let current = true; let resolve!: (value: unknown) => void;
		const { source } = setup({ getWorkspaceReviewFile: () => new Promise((done) => { resolve = done; }) }, () => current);
		const pending = source.getFile("s1", "a.ts"); current = false; resolve(detail());
		await expect(pending).rejects.toThrow(/account changed/i);
	});
	it("does not read after cancellation or adopt an ignored cancelled response", async () => {
		const controller = new AbortController(); controller.abort();
		const { source, client } = setup();
		await expect(source.getManifest("s1", { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
		expect(client.getWorkspaceReview).not.toHaveBeenCalled();
	});
	it("discards a file response if the native request was cancelled while Cloud was reading", async () => {
		let finish!: (value: unknown) => void;
		const { source } = setup({ getWorkspaceReviewFile: () => new Promise((done) => { finish = done; }) });
		const controller = new AbortController(); const pending = source.getFile("s1", "a.ts", undefined, { signal: controller.signal });
		controller.abort(); finish(detail());
		await expect(pending).rejects.toMatchObject({ name: "AbortError" });
	});
	it("explains disconnected Cloud files without resuming a sandbox or showing pairing errors", async () => {
		const { source } = setup({ getWorkspaceReview: async () => { throw new CloudApiError(409, { code: "WORKER_UNAVAILABLE", error: "conflict", message: "The session worker is not connected.", requestId: "internal-reference" }); } });
		await expect(source.getManifest("s1")).rejects.toThrow(/Cloud worker.*connected/i);
		const denied = setup({ getWorkspaceReview: async () => { throw new CloudApiError(403, { error: "forbidden", code: "FORBIDDEN", message: "Forbidden", requestId: "ref" }); } });
		await expect(denied.source.getManifest("s1")).rejects.toThrow(/Cloud workspace/i);
	});
});

describe("Cloud file review delivery", () => {
	const comments = [{ target: { path: "a.ts", side: "new" as const, line: 3, previousPath: "old.ts", workspaceVersion: "v1", fileFingerprint: "f1" }, feedback: "rename this" }];
	it("uses the owning session message route with annotation context and a stable retry key", async () => {
		const send = vi.fn().mockRejectedValueOnce(new Error("response lost")).mockResolvedValue({});
		const { source } = setup({ sendMessage: send }); const accepted = vi.fn();
		await expect(source.sendFeedback("s1", comments, accepted)).rejects.toThrow("response lost");
		expect(accepted).not.toHaveBeenCalled();
		expect(await source.sendFeedback("s1", comments, accepted)).toBe(1);
		expect(send.mock.calls[0][0]).toBe("org"); expect(send.mock.calls[0][1]).toBe("s1");
		expect(send.mock.calls[0][2]).toContain("- Previous path: old.ts");
		expect(send.mock.calls[0][2]).toContain("- Workspace version: v1");
		expect(send.mock.calls[1]).toEqual(send.mock.calls[0]);
		expect(send.mock.calls[0][3].idempotencyKey).toEqual(expect.any(String));
		expect(accepted).toHaveBeenCalledExactlyOnceWith(1);
		await source.sendFeedback("s1", comments);
		expect(send.mock.calls[2][3].idempotencyKey).not.toBe(send.mock.calls[0][3].idempotencyKey);
	});
	it("does not send a later batch or acknowledge comments after account retirement", async () => {
		let current = true;
		const send = vi.fn(async () => { current = false; });
		const { source } = setup({ sendMessage: send }, () => current); const accepted = vi.fn();
		await expect(source.sendFeedback("s1", Array.from({ length: 5 }, (_, i) => ({ target: { path: `${i}.ts`, side: "file" as const }, feedback: "x".repeat(1800) })), accepted)).rejects.toThrow(/account changed/i);
		expect(send).toHaveBeenCalledOnce(); expect(accepted).not.toHaveBeenCalled();
	});
	it("retries only the unacknowledged review with the original batch body and key after partial delivery", async () => {
		const comments = Array.from({ length: 5 }, (_, i) => ({ target: { path: `${i}.ts`, side: "file" as const }, feedback: "x".repeat(1800) }));
		const send = vi.fn().mockResolvedValueOnce({}).mockRejectedValueOnce(new Error("response lost")).mockResolvedValue({});
		const { source } = setup({ sendMessage: send }); let delivered = 0;
		await expect(source.sendFeedback("s1", comments, (count) => { delivered += count; })).rejects.toThrow("response lost");
		expect(delivered).toBeGreaterThan(0); expect(delivered).toBeLessThan(comments.length);
		const original = send.mock.calls[1];
		expect(await source.sendFeedback("s1", comments.slice(delivered))).toBe(comments.length - delivered);
		expect(send.mock.calls[2]).toEqual(original);
		expect(send.mock.calls.slice(2).some((call) => call[3].idempotencyKey === send.mock.calls[0][3].idempotencyKey)).toBe(false);
	});
	it("recovers an uncertain original batch before sending newly appended comments", async () => {
		const original = [...comments, { target: { path: "b.ts", side: "file" as const }, feedback: "fix b" }];
		const added = { target: { path: "c.ts", side: "file" as const }, feedback: "fix c" };
		const send = vi.fn().mockRejectedValueOnce(new Error("response lost")).mockResolvedValue({});
		const { source } = setup({ sendMessage: send }); const accepted = vi.fn();
		await expect(source.sendFeedback("s1", original)).rejects.toThrow("response lost");
		expect(await source.sendFeedback("s1", [...original, added], accepted)).toBe(3);
		expect(send.mock.calls[1]).toEqual(send.mock.calls[0]);
		expect(send.mock.calls[2][2]).toContain("- Path: c.ts");
		expect(send.mock.calls[2][2]).not.toContain("- Path: a.ts");
		expect(accepted.mock.calls).toEqual([[2], [1]]);
	});
	it("does not regroup overlapping comments when an uncertain batch was edited or partly removed", async () => {
		const original = [...comments, { target: { path: "b.ts", side: "file" as const }, feedback: "fix b" }];
		const send = vi.fn().mockRejectedValueOnce(new Error("response lost")).mockResolvedValue({});
		const { source } = setup({ sendMessage: send });
		await expect(source.sendFeedback("s1", original)).rejects.toThrow("response lost");
		await expect(source.sendFeedback("s1", comments)).rejects.toThrow(/earlier review.*conversation/i);
		expect(send).toHaveBeenCalledOnce();
	});
	it.each([
		{ desiredState: "paused", runtimeConnected: false, isTerminated: false },
		{ desiredState: "running", runtimeConnected: false, isTerminated: false },
		{ desiredState: "running", runtimeConnected: true, isTerminated: true },
	])("keeps review comments unsent rather than waking an unavailable session: %j", async (session) => {
		const { source, client } = setup({ getSession: async () => ({ session }) }); const accepted = vi.fn();
		await expect(source.sendFeedback("s1", comments, accepted)).rejects.toThrow(/resume or reconnect/i);
		expect(client.sendMessage).not.toHaveBeenCalled(); expect(accepted).not.toHaveBeenCalled();
	});
});
