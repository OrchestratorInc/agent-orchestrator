// @vitest-environment node
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { patchClaudeContextWindow } from "./build-acp-runtime-helpers.mjs";

const directories = [];
afterEach(() => {
	vi.useRealTimers();
	for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

// The resume resolver is the pinned upstream implementation, including its
// env/settings override branches. Other snippets are the packaging anchors.
const upstream = readFileSync(new URL("./fixtures/claude-context-models.js.txt", import.meta.url), "utf8");
const fixture = `${upstream}
function createSession(creationOpts, seededWindow) {
    return {
        contextWindowSize: seededWindow.size,
        contextWindowAuthoritative: seededWindow.authoritative,
    };
}
async function switchModel(session, value) {
    if (session.models.currentModelId !== value) {
        const seeded = { size: 200000, authoritative: false };
        session.contextWindowSize = seeded.size;
        session.contextWindowAuthoritative = seeded.authoritative;
    }
    session.models.currentModelId = value;
}
function result(session) {
    // AO: use the SDK's context snapshot.
}
function stream(session) {
    return { used: 841800, size: session.contextWindowSize, };
}
`;
const models = [
	{ value: "opus", displayName: "Opus 5.5" },
	{ value: "claude-opus-5-5", displayName: "Opus 5.5" },
	{ value: "small", displayName: "Small" },
];
function harness(env = {}, source = fixture) {
	const path = join(mkdtempSync(join(tmpdir(), "ao-context-")), "acp-agent.js");
	directories.push(join(path, ".."));
	writeFileSync(path, source);
	patchClaudeContextWindow(path);
	const resolve = (items, value) => items.find((m) => m.value === value);
	const code = readFileSync(path, "utf8");
	const api = new Function("process", "resolveModelPreference", "resolveSettingsModel", "matchResumedModel", `${code}\nreturn { getAvailableModels, createSession, switchModel, stream, result };`)(
		{ env }, resolve, resolve, resolve,
	);
	const logger = { error: vi.fn() };
	return { ...api, logger, path };
}
function query(window = 1_000_000) {
	return { setModel: vi.fn(async () => {}), getContextUsage: vi.fn(async () => ({ model: "claude-opus-5-5", rawMaxTokens: window })) };
}
async function resume(h, q, model = "opus", resumed = true) {
	return h.getAvailableModels(q, models, models, { getSettings: () => ({ model }) }, h.logger, resumed);
}

describe("Claude context window across host recreation", () => {
	it.each(["settings", "environment"])("reads the limit after reapplying a %s model pin", async (pin) => {
		const h = harness(pin === "environment" ? { ANTHROPIC_MODEL: "opus" } : {});
		const q = query();
		const loaded = await resume(h, q);
		expect(loaded.resumedContextWindow).toBe(1_000_000);
		expect(q.setModel).toHaveBeenCalledWith("opus");
		expect(q.setModel.mock.invocationCallOrder[0]).toBeLessThan(q.getContextUsage.mock.invocationCallOrder[0]);
		const session = h.createSession({ resume: "native" }, { size: loaded.resumedContextWindow, authoritative: true });
		expect(h.stream(session)).toEqual({ used: 841800, size: 1_000_000 });
	});

	it("keeps the refreshed limit when AO selects the explicit model after resume", async () => {
		const h = harness();
		const session = { ...h.createSession({ resume: "native" }, { size: 1_000_000, authoritative: true }), models: { currentModelId: "opus" }, query: query() };
		await h.switchModel.call(h, session, "claude-opus-5-5");
		expect(h.stream(session).size).toBe(1_000_000);
	});

	it("does not query fresh sessions before their first prompt or report a guessed 200K", async () => {
		const h = harness();
		const q = query();
		const loaded = await resume(h, q, "opus", false);
		expect(loaded.resumedContextWindow).toBeNull();
		const session = { ...h.createSession({}, { size: 200000, authoritative: false }), query: q, models: { currentModelId: "opus" } };
		await h.switchModel.call(h, session, "claude-opus-5-5");
		expect(q.getContextUsage).not.toHaveBeenCalled();
		expect(h.stream(session).size).toBe(0);
	});

	it("refreshes a real model change after a fresh session has completed a turn", async () => {
		const h = harness();
		const session = { ...h.createSession({}, { size: 1_000_000, authoritative: true }), query: query(200000), models: { currentModelId: "opus" } };
		h.result(session);
		await h.switchModel.call(h, session, "small");
		expect(h.stream(session).size).toBe(200000);
		expect(session.contextWindowAuthoritative).toBe(true);
	});

	it("does not re-query an unchanged model or retain the old model's limit on failure", async () => {
		const h = harness();
		const session = { ...h.createSession({ resume: "native" }, { size: 1_000_000, authoritative: true }), query: query(), models: { currentModelId: "opus" } };
		await h.switchModel.call(h, session, "opus");
		expect(session.query.getContextUsage).not.toHaveBeenCalled();
		session.query.getContextUsage.mockRejectedValue(new Error("unavailable"));
		await h.switchModel.call(h, session, "small");
		expect(h.stream(session).size).toBe(0);
	});

	it.each([0, -1, NaN, Infinity, undefined])("rejects invalid SDK limit %s", async (window) => {
		const h = harness();
		const q = query();
		q.getContextUsage.mockResolvedValue({ rawMaxTokens: window });
		expect((await resume(h, q)).resumedContextWindow).toBeNull();
	});

	it("keeps resume usable when the context request fails", async () => {
		const h = harness();
		const q = query();
		q.getContextUsage.mockRejectedValue(new Error("unavailable"));
		expect((await resume(h, q)).modelState.currentModelId).toBe("opus");
		expect(h.logger.error).toHaveBeenCalled();
	});

	it("bounds the extra request and ignores a late response", async () => {
		vi.useFakeTimers();
		const h = harness();
		const q = query();
		let resolve;
		q.getContextUsage.mockReturnValue(new Promise((r) => { resolve = r; }));
		const pending = resume(h, q);
		await vi.advanceTimersByTimeAsync(2000);
		const loaded = await pending;
		expect(loaded.resumedContextWindow).toBeNull();
		resolve({ rawMaxTokens: 1000000 });
		await Promise.resolve();
		expect(loaded.resumedContextWindow).toBeNull();
		expect(vi.getTimerCount()).toBe(0);
	});

	it("preserves the upstream unpinned and rejected-pin resume behavior", async () => {
		const h = harness();
		for (const pin of [undefined, "opus"]) {
			const q = query();
			q.setModel.mockRejectedValue(new Error("pin rejected"));
			const loaded = await resume(h, q, pin === undefined ? null : pin);
			expect(loaded.modelState.currentModelId).toBe("claude-opus-5-5");
			expect(loaded.resumedContextWindow).toBe(1000000);
		}
	});

	it("is idempotent and fails closed when packaging anchors change", () => {
		const h = harness();
		expect(patchClaudeContextWindow(h.path)).toBe(false);
		for (const anchor of [
			"await query.setModel(currentModel.value);",
			"contextWindowSize: seededWindow.size,",
			"session.contextWindowAuthoritative = seeded.authoritative;",
			"// AO: use the SDK's context snapshot.",
			"size: session.contextWindowSize,",
		]) {
			writeFileSync(h.path, fixture.replace(anchor, ""));
			expect(() => patchClaudeContextWindow(h.path)).toThrow("context window");
			expect(readFileSync(h.path, "utf8")).toBe(fixture.replace(anchor, ""));
		}
	});
});
