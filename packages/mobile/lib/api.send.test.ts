import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import { readFileSync } from "node:fs";
import { sendMessage } from "./api";
import type { ServerConfig } from "./config";

const cfg: ServerConfig = { host: "ao.test", httpPort: "3011", muxPort: "3011", secure: false, password: "secret12" };

describe("sendMessage", () => {
	beforeEach(() => {
		vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 })));
	});
	afterEach(() => vi.unstubAllGlobals());

	function sentBody(): unknown {
		const [, init] = vi.mocked(fetch).mock.calls[0];
		return JSON.parse(String(init?.body));
	}

	it("marks user-typed text as userAuthored so the daemon does not wrap it as an AO delivery", async () => {
		await sendMessage(cfg, "w-1", "Hi", { userAuthored: true });
		const [url] = vi.mocked(fetch).mock.calls[0];
		expect(url).toBe("http://ao.test:3011/api/v1/sessions/w-1/send");
		expect(sentBody()).toEqual({ message: "Hi", userAuthored: true });
	});

	it("leaves the message unmarked when the caller does not opt in", async () => {
		await sendMessage(cfg, "w-1", "Coordination");
		expect(sentBody()).toEqual({ message: "Coordination" });
	});
});

// These are large screen components; pin each call so a refactor can't silently
// drop the flag and bring the <ao-session-delivery> echo back. Desktop sends the
// same review messages as userAuthored (SessionInspector sendMessageToWorker).
describe("user-initiated sends", () => {
	it.each([
		["the terminal composer", "./session/TerminalSessionScreen.tsx", "await sendMessage(activeConfig, id, text, { userAuthored: true });"],
		["Send review to worker", "../app/review/[sessionId].tsx", "await sendMessage(config, sessionId, formatReviewSummaryMessage(run), { userAuthored: true });"],
		["Send feedback to worker", "../app/sheets/review-actions.tsx", "await sendMessage(config, sessionId, message, { userAuthored: true });"],
	])("%s sends as userAuthored", (_name, path, call) => {
		expect(readFileSync(new URL(path, import.meta.url), "utf8")).toContain(call);
	});
});
