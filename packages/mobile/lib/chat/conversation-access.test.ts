import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { SessionSource } from "../environment/types";
import { conversationAccess } from "./conversation-access";

const source = (kind: "local" | "cloud"): SessionSource => ({
	kind,
	...(kind === "cloud" ? { scopeKey: "account-one" } : {}),
	listProjects: vi.fn(),
	listSessions: vi.fn(),
	createSession: vi.fn(),
	deleteSession: vi.fn(),
	getConversationPage: vi.fn(),
	sendMessage: vi.fn(),
	cancelTurn: vi.fn(),
	subscribeEvents: vi.fn(),
	resumeSession: vi.fn(),
});

const config = {
	host: "mac.local",
	httpPort: "9090",
	password: "secret",
	secure: false,
} as ServerConfig;

describe("conversationAccess", () => {
	it("opens scoped Cloud conversations without a Local daemon and uses one event owner", () => {
		const cloud = source("cloud");
		expect(conversationAccess({ config: null, source: cloud, sessionId: "orch-1" })).toEqual({
			source: cloud,
			cacheKey: JSON.stringify(["cloud", "account-one", "orch-1"]),
			pollInterval: null,
			subscribeToEvents: true,
		});
	});
	it("never reuses another account's same-session cache or an unscoped Cloud adapter", () => {
		const a = source("cloud"); const b = { ...a, scopeKey: "account-two" };
		expect(conversationAccess({ config: null, source: a, sessionId: "s1" })?.cacheKey).not.toBe(conversationAccess({ config: null, source: b, sessionId: "s1" })?.cacheKey);
		expect(conversationAccess({ config: null, source: { ...a, scopeKey: undefined }, sessionId: "s1" })).toBeUndefined();
	});

	it("keeps Local conversations scoped to their paired daemon and event stream", () => {
		const local = source("local");
		const access = conversationAccess({ config, source: local, sessionId: "worker-1" });
		expect(access).toMatchObject({ source: local, pollInterval: null, subscribeToEvents: true });
		expect(access?.cacheKey).toContain("mac.local:9090/secret/worker-1");
	});

	it("does not expose a Local source before its daemon configuration resolves", () => {
		expect(conversationAccess({ config: null, source: source("local"), sessionId: "worker-1" })).toBeUndefined();
		expect(conversationAccess({ config, source: undefined, sessionId: "worker-1" })).toBeUndefined();
	});
});
