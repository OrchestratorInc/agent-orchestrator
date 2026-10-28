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

	it("leaves AO-generated relays unmarked", async () => {
		await sendMessage(cfg, "w-1", "Review summary");
		expect(sentBody()).toEqual({ message: "Review summary" });
	});
});

describe("terminal composer", () => {
	// The composer is a large screen component; pin the call itself so a refactor
	// can't silently drop the flag and bring the <ao-session-delivery> echo back.
	it("sends the user's text as userAuthored", () => {
		const source = readFileSync(new URL("./session/TerminalSessionScreen.tsx", import.meta.url), "utf8");
		expect(source).toContain("await sendMessage(activeConfig, id, text, { userAuthored: true });");
	});
});
